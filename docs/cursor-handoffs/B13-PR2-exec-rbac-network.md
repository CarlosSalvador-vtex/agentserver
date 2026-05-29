# B13 PR2 — make pods/exec work so the OpenClaw IM turn completes

> Follow-up to B13 PR1 (#148). The OpenClaw-direct IM turn is fully wired and proven up to
> the final `kubectl exec` into the sandbox pod, which fails. This PR makes that exec work.
> Found via live Telegram E2E on dev, 2026-05-29.

## Context — B13 PR1 is proven end-to-end except the exec

Live Telegram test (bot @vendas_sim1_cs_bot → workspace channel → bound OpenClaw sandbox):

| Step | Result |
|------|--------|
| Routing flips to `openclaw` (not codex/405) | ✅ |
| `GetSandboxForChannel` resolves the bound running sandbox | ✅ |
| Exact command built | ✅ `node openclaw.mjs agent --message "<text>" --json --session-id im-<hash>` |
| `K8sExec.ExecSimple` POSTs to the pod `/exec` | ❌ fails |

imbridge log:
```
imbridge: forward failed channel=aa6c9a0b from=... : exec: error sending request:
Post "https://10.43.0.1:443/api/v1/namespaces/agent-ws-22707304/pods/agent-sandbox-453bff36/exec?
command=node&command=openclaw.mjs&command=agent&command=--message&...&command=--json&...":
dial tcp 10.43.0.1:443: i/o timeout
```

So everything works except the exec call to the Kubernetes API.

## Two root causes to fix

### 1. RBAC — the runtime SA cannot `pods/exec`

```
kubectl auth can-i create pods/exec --as=system:serviceaccount:agentserver:agentserver  → no
```

Yet the chart's `deploy/helm/agentserver/templates/rbac.yaml` ClusterRole already lists
`pods/exec` (create, get), and the live `agentserver-sandbox` ClusterRole shows that rule,
bound via ClusterRoleBinding `agentserver-sandbox` → SA `agentserver/agentserver`. Despite
that, the effective permission is **denied**. The live ClusterRole was created 2026-05-25
and has not been updated since — `helm upgrade` during the dev deploys did not reconcile it
(it only changed the image). Net: the deployed RBAC is out of sync with the chart.

**Fix:** force the RBAC to reconcile so `can-i create pods/exec` returns `yes` for the
runtime SA. Options: `helm upgrade` that actually re-applies the ClusterRole (verify the
template renders + is applied; consider `--force` or deleting+recreating the stale
ClusterRole/Binding), or apply the rbac.yaml directly. Then confirm:
```
kubectl auth can-i create pods/exec --as=system:serviceaccount:agentserver:agentserver -n agent-ws-<wid>  → yes
```

### 2. Network — imbridge → API server exec stream times out

`dial tcp 10.43.0.1:443: i/o timeout` from the imbridge pod. `buildRESTConfig`
(`internal/imbridgesvc/exec.go`) uses `rest.InClusterConfig()` (correct), so the endpoint is
the in-cluster `kubernetes` service. The exec uses a streaming (SPDY/WebSocket) upgrade,
which can be blocked even when normal API calls work.

**Fix / verify:** confirm the imbridge pod can reach the API server for exec streams. Check
for egress restrictions, security groups, or that `10.43.0.1` is the correct/reachable
`kubernetes` service ClusterIP from that pod. (No NetworkPolicy exists in the `agentserver`
ns, so check node/SG/endpoint-level reachability and the exec stream specifically.)

## Decision point surfaced by this finding

`ExecSimple` runs in the **imbridge** service today (B13 PR1 put it there). The **agentserver**
main service already owns the sandbox manager + provisions/execs pods. If granting imbridge
robust K8s-exec access is awkward, the cleaner alternative is to move the exec to agentserver:
imbridge POSTs the inbound to a new agentserver internal endpoint (mirroring `/codex/turn`),
agentserver does `GetSandboxForChannel` → `ExecSimple` → returns the reply, imbridge delivers.
Pick whichever makes K8s-exec a first-class, RBAC-clean capability. (Either way, fix #1 RBAC.)

## Acceptance Criteria

1. `kubectl auth can-i create pods/exec --as=<runtime SA>` returns `yes` (cluster + agent-ws-*).
2. A Telegram message to an OpenClaw-bound channel runs `openclaw agent` in the pod and the
   reply is delivered back to the chat (no i/o timeout, no codex).
3. Multi-turn memory works (stable `--session-id`).
4. Automations fire→deliver works via the same exec path (B13 follow-up).
5. `go build -tags goolm ./...` + `go vet` pass; deploy applies the RBAC.

## Out of scope / follow-ups

- Persona authoring: the live test reconfirmed a **prompt.md-only skill does NOT load as an
  OpenClaw plugin** (`plugin not found: vendas-sim1`). Sim personas must go in a **soul**
  (systemPrompt) or a proper plugin (manifest + index.mjs), not a bare-prompt skill.
- Workspace PVC is ReadWriteOnce → only one running sandbox per workspace at a time
  (Multi-Attach error when two coexist). The sim's "5 bots per team" needs 5 workspaces, or
  RWX (EFS), or per-bot PVCs. Note for the sim plan.

## Test fixtures left in dev (for re-test after the fix)

- Telegram bot @vendas_sim1_cs_bot, workspace channel `aa6c9a0b-4053-428f-a8f3-74cb4c5079c6`
  (workspace `22707304-…`, routing_mode=openclaw), persistent sandbox `vendas-bot-tg`
  (`453bff36-…`) bound. Send a message to the bot to re-test once exec works.

## Files Reference

| File | Change |
|------|--------|
| `deploy/helm/agentserver/templates/rbac.yaml` | ensure pods/exec is applied to the runtime SA (reconcile stale live ClusterRole) |
| `internal/imbridgesvc/exec.go` | verify/repair exec stream reachability; or move exec to agentserver |
| (optional) new agentserver internal `/openclaw/turn` endpoint | if moving exec out of imbridge |

## Related

- B13 PR1 #148 (the openclaw routing + forwardToOpenclaw).
- `docs/cursor-handoffs/B13-openclaw-direct-im-turn.md` (#147).
- `docs/multibot-im-simulation-plan.md` (#144).
