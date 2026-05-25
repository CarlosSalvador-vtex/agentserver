# Sandbox Architecture — Kubernetes Mapping

How sandboxes map to Kubernetes resources in agentserver.

## Q: Quando um sandbox é criado, um novo pod é criado exclusivamente para isso?

Sim. Cada sandbox é um pod dedicado, isolado dentro do namespace do workspace.

---

## Padrão de mapeamento

| Layer | Recurso K8s | Nome | Lifecycle |
|---|---|---|---|
| Workspace | Namespace | `agent-ws-<short_id>` | Vida do workspace |
| Sandbox | CRD `sandboxes.agents.x-k8s.io` | `agent-sandbox-<short_id>` | Vida do sandbox |
| Sandbox runtime | Pod | `agent-sandbox-<short_id>` | Criado pelo controller a partir do CRD |

**Exemplo concreto:**

```
workspace UUID 7afe5449-... → namespace agent-ws-7afe5449
sandbox  UUID d95c33dd-... → CRD + Pod agent-sandbox-d95c33dd em agent-ws-7afe5449
```

---

## Isolamento por sandbox

Cada pod sandbox recebe:

- **CPU/memória dedicados** — quotas vindas do `Resources.Limits` no CRD (ex: 500m cpu / 1Gi memory)
- **PVC próprio para session data** — `session-data-agent-sandbox-<id>` (RWO, 5 Gi default)
- **Variáveis de ambiente próprias** — `OPENCLAW_INJECT_CFG`, `HERMES_HOME`, `ANTHROPIC_BASE_URL`, tokens de proxy, etc.
- **Subdomain único** — `<prefix>-<short_id>.<baseDomain>` (ex: `claw-dy49fit3.agentserver.analytics.vtex.com`, `hermes-XXXX.agentserver.analytics.vtex.com`)
- **Tolerations** herdadas do `SANDBOX_TOLERATIONS` (env var injetada no agentserver) para schedulear em nodes com taint

---

## Storage por sandbox

```
agent-ws-<id>/
├── PVC: agent-ws-<id>-disk          (workspace, compartilhado entre sandboxes)
└── PVC: session-data-agent-sandbox-<id>  (por-sandbox, isolado)
```

| PVC | Escopo | Access mode (dev EKS) | O que guarda |
|---|---|---|---|
| `agent-ws-<id>-disk` | Workspace | RWO (gp3) ou RWM (EFS) | Arquivos do workspace, código compartilhado |
| `session-data-agent-sandbox-<id>` | Sandbox | RWO (gp3) | Home do agente, configs runtime, estado |

> **Nota dev EKS:** workspace disk forçado a RWO via `WORKSPACE_DRIVE_ACCESS_MODES=ReadWriteOnce` porque gp3/EBS não suporta RWM. Para múltiplos sandboxes em nodes diferentes acessarem o mesmo workspace disk, instalar EFS CSI driver e mudar para RWM.

---

## Lifecycle states

### Create
1. POST `/api/workspaces/<wid>/sandboxes` → agentserver gera UUID + short ID
2. Cria CRD `Sandbox` no namespace do workspace
3. Cria PVC `session-data-<id>`
4. `agent-sandbox-controller` materializa o pod a partir do podTemplate do CRD
5. Pod entra `Pending` → `ContainerCreating` → `Running`

### Pause (idle timeout ou manual)
- Pod é **deletado**
- CRD permanece (status `paused`)
- PVC `session-data-<id>` **mantido**
- Sandbox row no DB com `status=paused`

### Resume
- Recria o pod a partir do CRD intacto
- Monta o PVC `session-data-<id>` de volta (estado preservado)
- Pod sobe com mesmas configs

### Delete
- CRD removido (cascade deleta pod)
- PVC `session-data-<id>` **destruído** (perde estado)
- Workspace disk (`agent-ws-<id>-disk`) **mantido** (compartilhado)
- DB row removida

---

## Comandos úteis (dev EKS)

```bash
CTX="arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform"

# Listar pods de sandboxes em um workspace
kubectl --context $CTX get pods -n agent-ws-7afe5449

# Ver o CRD do sandbox
kubectl --context $CTX get sandbox agent-sandbox-d95c33dd -n agent-ws-7afe5449 -o yaml

# Ver PVCs (workspace + sessions)
kubectl --context $CTX get pvc -n agent-ws-7afe5449

# Events de scheduling/provisioning
kubectl --context $CTX get events -n agent-ws-7afe5449 --sort-by='.lastTimestamp'

# Logs do pod do sandbox (container `agent`)
kubectl --context $CTX logs agent-sandbox-d95c33dd -n agent-ws-7afe5449
```
