# Tier 1 — deploy & acceptance checklist

Run after merging Tier 1 changes to `main`. Requires dev EKS kube context (`dev-ti-eks-analytics-platform`) and AWS ECR push access.

> **Sprint 5 complete (2026-05-27).** All 20 backlog items shipped. Current image tag: `sprint5-final`.
> Dev cluster now uses external RDS — see `docs/dev-eks-deploy.md` for k8s secret names and connection details.
> Staging namespace: `agentserver-staging` on the same dev cluster (`values-staging-eks.yaml` at repo root).

## Build & deploy

```bash
./scripts/build/build-one.sh agentserver sprint5-final
# values-dev-eks.yaml targets tag: sprint5-final (updated Sprint 5)

export AWS_PROFILE=<analytics-profile>
export AWS_REGION=us-east-1
aws eks update-kubeconfig --name dev-ti-eks-analytics-platform --region us-east-1

helm upgrade --install agentserver ./deploy/helm/agentserver \
  -n agentserver --create-namespace -f values-dev-eks.yaml

kubectl rollout status deploy/agentserver -n agentserver
```

## Automated tests (local / CI)

```bash
go build -tags goolm ./...
go vet -tags goolm ./...
go test -tags goolm ./internal/sandbox/ ./internal/server/ -short

TEST_DATABASE_URL='postgres://...' go test -tags goolm -race \
  ./internal/sandbox/ ./internal/server/ \
  -run 'ResolveComposition|ProvisionSandbox_Composition'
```

## Manual acceptance

1. **Composition picker** — Sandboxes → Create → attach draft soul + skill → verify `sandbox_compositions` row and pod mounts.
2. **OpenClaw soul** — `kubectl exec` → `cat /home/agent/.openclaw/soul.md`; `env | grep SOUL`; agent answers in persona.
3. **Rate limits** — 11× `POST /api/playground/skills/{id}/dry-run` in 60s → 11th returns `429` + `Retry-After: 6`.
4. **Constants** — `rg '"openclaw"|"hermes"' internal/ --glob '*.go'` → only `types.go` and test fixtures.
