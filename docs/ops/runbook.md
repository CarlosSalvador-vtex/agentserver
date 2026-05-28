# Ops runbook — dev EKS deploy and seed

Operational steps for the **dev** agentserver stack on `dev-ti-eks-analytics-platform`. For cluster topology, RDS wiring, LiteLLM, and Helm values reference, see [`docs/dev-eks-deploy.md`](../dev-eks-deploy.md).

---

## Prerequisites

| Tool | Purpose |
|---|---|
| `kubectl` | Deploy, rollout, port-forward, seed Job |
| `helm` | Install/upgrade `agentserver` chart |
| `docker` | Build/push image (`deploy-dev.sh`) |
| `aws` CLI | ECR login, Secrets Manager (optional) |
| `gh` | Fork workflow (optional) |

**Kubeconfig context (dev):**

```bash
export DEV_CONTEXT="arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform"
kubectl config use-context "$DEV_CONTEXT"
```

**AWS account:** `344729309528` · **Region:** `us-east-1` · **Namespace:** `agentserver`

**Node scheduling:** All worker nodes use taint `internal-workers=true:NoSchedule`. Every pod in this stack must tolerate that taint (chart `tolerations` in `values-dev-eks.yaml`; see [PR #83](https://github.com/CarlosSalvador-vtex/agentserver/pull/83)). Without it, pods stay `Pending`.

---

## First deploy (clone → build → Helm)

From a clean clone of the fork:

```bash
git clone https://github.com/CarlosSalvador-vtex/agentserver.git
cd agentserver

# 1. Build, push to ECR, helm upgrade --install (default tag: playground-v2 from values-dev-eks.yaml)
./scripts/deploy-dev.sh

# Optional: override image tag
./scripts/deploy-dev.sh my-custom-tag
```

What `deploy-dev.sh` does:

1. `docker build` → `344729309528.dkr.ecr.us-east-1.amazonaws.com/agentserver:<tag>`
2. `docker push` to ECR
3. `helm upgrade --install agentserver deploy/helm/agentserver -f values-dev-eks.yaml` in namespace `agentserver`
4. Waits for `deployment/agentserver` rollout

**First-time only — seed marketplace templates** (after DB/RDS and secrets exist):

```bash
./scripts/apply-seed-cobranca.sh
```

Or manually:

```bash
kubectl apply -f scripts/seed-cobranca-job.yaml -n agentserver --context "$DEV_CONTEXT"
kubectl wait --for=condition=complete job/seed-cobrana-templates -n agentserver --context "$DEV_CONTEXT" --timeout=120s
kubectl logs -n agentserver job/seed-cobrana-templates -f --context "$DEV_CONTEXT"
```

Verify UI: **Marketplace** should list soul **Agente de Cobrança** and skill **Negociação de Dívida**.

---

## Re-deploy / update

After code or chart changes:

```bash
# App image + Helm (same as first deploy)
./scripts/deploy-dev.sh

# LiteLLM only (if litellm config changed)
helm upgrade litellm deploy/helm/litellm \
  -n agentserver \
  -f values-litellm-dev-eks.yaml \
  --kube-context "$DEV_CONTEXT" \
  --wait --timeout 5m

# Helm only, no rebuild (e.g. values-dev-eks.yaml edit)
helm upgrade agentserver deploy/helm/agentserver \
  -n agentserver \
  -f values-dev-eks.yaml \
  --kube-context "$DEV_CONTEXT" \
  --wait --timeout 5m
```

Check rollout:

```bash
kubectl rollout status deployment/agentserver -n agentserver --context "$DEV_CONTEXT"
kubectl get pods -n agentserver --context "$DEV_CONTEXT"
```

---

## Seed templates

**Script:** `scripts/apply-seed-cobranca.sh`  
**In-cluster Job:** `scripts/seed-cobranca-job.yaml`  
**DB script:** `scripts/seed-cobranca-templates.py` (uses `agentserver-db-secret` → RDS)

### First seed (idempotent)

SQL inserts use `WHERE NOT EXISTS` — safe to run once after deploy.

```bash
./scripts/apply-seed-cobranca.sh
```

### Re-seed after skill content changes

The Job/SQL path does **not** update rows that already exist. To replace templates:

```sql
-- Connect to agentserver DB (e.g. via psql + agentserver-db-secret URL)
DELETE FROM skill_drafts
  WHERE name = 'Negociação de Dívida'
    AND workspace_id IS NULL
    AND author_user_id IS NULL;

DELETE FROM soul_drafts
  WHERE name = 'Agente de Cobrança'
    AND workspace_id IS NULL
    AND author_user_id IS NULL;
```

Then run `./scripts/apply-seed-cobranca.sh` again.

---

## Rollback

### Application (Helm)

```bash
# List revisions
helm history agentserver -n agentserver --kube-context "$DEV_CONTEXT"

# Roll back to previous revision (example: revision 3)
helm rollback agentserver 3 -n agentserver --kube-context "$DEV_CONTEXT"

kubectl rollout status deployment/agentserver -n agentserver --context "$DEV_CONTEXT"
```

Rollback **image only** without changing chart revision: redeploy a known-good tag:

```bash
./scripts/deploy-dev.sh <known-good-tag>
```

### Database (RDS)

RDS is **external** to the cluster (`postgresql.enabled: false` in dev values). Helm rollback does **not** revert database migrations or seed data. Restore from RDS snapshots/backups if needed; re-run seed only affects template rows as documented above.

---

## Key URLs

| Access | URL / command |
|---|---|
| **UI (VPN)** | https://agentserver.analytics.vtex.com |
| **Port-forward** | `kubectl port-forward svc/agentserver 8080:8080 -n agentserver --context "$DEV_CONTEXT"` → http://localhost:8080 |
| **ALB (internal)** | `internal-agentserver-dev-1984423208.us-east-1.elb.amazonaws.com` |
| **Route53** | CNAME `agentserver.analytics.vtex.com` → ALB (zone `analytics.vtex.com`) |

---

## Quick reference

```bash
CTX="$DEV_CONTEXT"
NS=agentserver

helm list -n $NS --kube-context $CTX
kubectl get pods -n $NS --kube-context $CTX
kubectl get ingress -n $NS --kube-context $CTX
```

Related docs: [`dev-eks-deploy.md`](../dev-eks-deploy.md) · backlog item **B2** in [`docs-organization-backlog.md`](../docs-organization-backlog.md).