# Sandbox API — Create / Delete

How to create and delete sandboxes in the agentserver via HTTP, without using the Web UI.

Base URL (dev EKS): `https://agentserver.analytics.vtex.com`

Auth: session cookie (`agentserver-token`) issued by `/api/auth/login`. All `/api/...` sandbox routes accept this cookie.

---

## 1. Authenticate

```bash
BASE="https://agentserver.analytics.vtex.com"

curl -sS -c /tmp/agentserver-cookies.txt \
  -X POST "$BASE/api/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"email":"YOU@example.com","password":"YOUR_PASSWORD"}'
# → {"status":"ok"}
```

`-c /tmp/agentserver-cookies.txt` saves the `agentserver-token` cookie. All subsequent calls pass `-b /tmp/agentserver-cookies.txt`.

Check who you are:
```bash
curl -sS -b /tmp/agentserver-cookies.txt "$BASE/api/auth/me"
# → {"id":"...","email":"...","role":"admin"}
```

---

## 2. List workspaces

Need the workspace ID (a UUID, e.g. `7afe5449-c704-412b-8714-b25351198115`) before creating a sandbox.

```bash
curl -sS -b /tmp/agentserver-cookies.txt "$BASE/api/workspaces"
```

Pick the `id` of the workspace you want.

---

## 3. Create a sandbox

```
POST /api/workspaces/{workspaceId}/sandboxes
```

Body fields:
| field | type | required | notes |
|---|---|---|---|
| `name` | string | no | defaults to "New Sandbox" |
| `type` | string | no | `opencode` (default), `openclaw`, `nanoclaw`, `claudecode`, `jupyter` |
| `cpu` | int (millicores) | no | e.g. `500` = 0.5 cores |
| `memory` | int (bytes) | no | e.g. `1073741824` = 1024 MB |
| `idle_timeout` | int (seconds) | no | e.g. `1800` = 30 min; `0` = never |
| `metadata` | object | no | per-sandbox metadata (e.g. `{"assistant_name":"Andy"}` for nanoclaw) |

Example — OpenClaw sandbox, 0.5 CPU, 1024 MB, 30 min idle:

```bash
WS="7afe5449-c704-412b-8714-b25351198115"

curl -sS -b /tmp/agentserver-cookies.txt \
  -X POST "$BASE/api/workspaces/$WS/sandboxes" \
  -H "Content-Type: application/json" \
  -d '{
    "name":"my-openclaw",
    "type":"openclaw",
    "cpu":500,
    "memory":1073741824,
    "idle_timeout":1800
  }'
```

Returns the created sandbox (HTTP 201):

```json
{
  "id":"12ce0ed1-b906-44c3-b4df-ea888087acff",
  "workspace_id":"7afe5449-...",
  "name":"my-openclaw",
  "type":"openclaw",
  "status":"creating",
  "cpu":500,
  "memory":1073741824,
  ...
}
```

Capture the `id` — that's the sandbox ID for all subsequent calls.

---

## 4. Inspect a sandbox

```bash
SBX="12ce0ed1-b906-44c3-b4df-ea888087acff"

curl -sS -b /tmp/agentserver-cookies.txt "$BASE/api/sandboxes/$SBX"
```

Status transitions: `creating` → `running` → (`pausing` → `paused` → `resuming` →) `running` → `deleting`.

Poll until `status == "running"` before opening or attaching.

---

## 5. Pause / resume

```bash
# Pause (releases CPU/memory; session disk preserved)
curl -sS -b /tmp/agentserver-cookies.txt -X POST "$BASE/api/sandboxes/$SBX/pause"

# Resume
curl -sS -b /tmp/agentserver-cookies.txt -X POST "$BASE/api/sandboxes/$SBX/resume"
```

---

## 6. Delete a sandbox

```
DELETE /api/sandboxes/{sandboxId}
```

```bash
curl -sS -b /tmp/agentserver-cookies.txt -X DELETE "$BASE/api/sandboxes/$SBX"
# → 204 No Content
```

Deletes the Kubernetes Sandbox CR, drops the session PVC, and removes the DB row. Workspace disk and traces are kept.

---

## 7. One-liner: create + wait + delete

```bash
BASE="https://agentserver.analytics.vtex.com"
WS="7afe5449-c704-412b-8714-b25351198115"
COOKIE="/tmp/agentserver-cookies.txt"

# Create
SBX=$(curl -sS -b $COOKIE -X POST "$BASE/api/workspaces/$WS/sandboxes" \
  -H "Content-Type: application/json" \
  -d '{"type":"openclaw","cpu":500,"memory":1073741824}' \
  | jq -r .id)
echo "created: $SBX"

# Wait until running
while true; do
  STATUS=$(curl -sS -b $COOKIE "$BASE/api/sandboxes/$SBX" | jq -r .status)
  echo "status: $STATUS"
  [[ "$STATUS" == "running" ]] && break
  [[ "$STATUS" == "error" ]] && exit 1
  sleep 5
done

# Delete
curl -sS -b $COOKIE -X DELETE "$BASE/api/sandboxes/$SBX"
echo "deleted: $SBX"
```

---

## Notes

- **Quotas**: workspace defaults cap `cpu`, `memory`, and `idle_timeout`. Check `GET /api/workspaces/{wid}/defaults` to see limits.
- **Dev cluster (`dev-ti-eks-analytics-platform`)**: nodes have 2 CPU each, so requesting 2 cores will leave the pod Pending. Use 0.5–1.0.
- **Auth via cookie** is the supported path for human/scripted access. Workspace API Keys (`/api/workspaces/{wid}/api-keys`) exist but are scoped for internal service-to-service calls (codex-app-gateway), not the sandbox CRUD endpoints.
- **Port-forward alternative** (for cluster-internal testing):
  ```bash
  CTX="arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform"
  kubectl --context $CTX port-forward svc/agentserver 8080:8080 -n agentserver
  # then use BASE="http://localhost:8080"
  ```
