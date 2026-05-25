# Deploy agentserver — dev-ti-eks-analytics-platform

Estado atual do deploy no cluster EKS de dev. Documento de continuidade para futuras sessões.

---

## Repositório

Fork: https://github.com/CarlosSalvador-vtex/agentserver
Clone local: `/Users/carlos.neto/Documents/pessoal/obsidian/05-PROJECTS/agentserver-study/`

Upstream original: `agentserver/agentserver` (MIT)

---

## Cluster

| Campo | Valor |
|---|---|
| Nome | `dev-ti-eks-analytics-platform` |
| Região | `us-east-1` |
| Conta AWS | `344729309528` |
| OIDC ID | `6B9DCB1BF66AC2A4C72DFAC1D1A32965` |
| kubectl context | `arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform` |

**Node taints:** todos os worker nodes têm `internal-workers=true:NoSchedule`.  
Todos os charts precisam de tolerations para schedulear.

---

## Namespaces

| Namespace | Conteúdo |
|---|---|
| `agentserver` | Stack principal (agentserver + litellm) |
| `agent-sandbox-system` | agent-sandbox-controller (CRD controller) |

---

## Stack deployada

```
kubectl --context arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform \
  get pods -n agentserver
```

| Pod | Função |
|---|---|
| `agentserver` | App principal + UI web |
| `agentserver-postgresql` | PostgreSQL bundled |
| `agentserver-llmproxy` | Token tracking/quota por workspace |
| `agentserver-sandboxproxy` | Proxy de tráfego dos sandboxes |
| `agentserver-imbridge` | IM bridge (WeChat/Telegram — não usado) |
| `litellm` | Proxy LLM → AWS Bedrock (via IRSA) |
| `agent-sandbox-controller` (ns: agent-sandbox-system) | Controller do CRD `agents.x-k8s.io/sandboxes` |

---

## Acesso

**URL (interna VPN VTEX):** https://agentserver.analytics.vtex.com

**Port-forward local:**
```bash
kubectl --context arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform \
  port-forward svc/agentserver 8080:8080 -n agentserver
# → http://localhost:8080
```

**ALB:** `internal-agentserver-dev-1984423208.us-east-1.elb.amazonaws.com` (internal)  
**Route53:** CNAME `agentserver.analytics.vtex.com` → ALB (zona `analytics.vtex.com`, ID: `Z01187271ZBXV1TH65BV0`)

---

## Fluxo LLM

```
sandbox (OpenClaw) → llmproxy → LiteLLM → AWS Bedrock → Claude Sonnet 4.6
                                                ↕ inference profile cross-region
                                          us-east-1 / us-east-2 / us-west-2
```

### Modelos disponíveis

| Model name (LiteLLM) | Backend |
|---|---|
| `bedrock/claude-sonnet-4-6` | Inference Profile `62r7btpf0s40` (Claude Sonnet 4.6) |
| `bedrock/claude-3-sonnet` | `anthropic.claude-3-sonnet-20240229-v1:0` |
| `bedrock/claude-3-haiku` | `anthropic.claude-3-haiku-20240307-v1:0` |

### Inference Profile

- **Nome:** `OpenClaw-Claude-Sonnet-4-6`
- **ARN:** `arn:aws:bedrock:us-east-1:344729309528:application-inference-profile/62r7btpf0s40`
- **Tipo:** APPLICATION (cross-region: us-east-1, us-east-2, us-west-2)
- **LiteLLM workaround:** usar `aws_bedrock_model_id` em `litellm_params` porque `bedrock_converse` não é suportado como `custom_llm_provider` no LiteLLM router v1.82.6

### IRSA

- **Role:** `dev-ti-eks-analytics-platform-litellm-bedrock-role`
- **Policy inline:** `bedrock-invoke-claude`
- **Scripts:** `deploy/helm/litellm/iam/`

---

## Arquivos de configuração

| Arquivo | Propósito |
|---|---|
| `values-dev-eks.yaml` | Overrides do chart `agentserver` para dev cluster |
| `values-litellm-dev-eks.yaml` | Overrides do chart `litellm` (IRSA role ARN + tolerations) |
| `deploy/helm/litellm/` | Chart LiteLLM customizado (não existe chart oficial adequado) |
| `deploy/helm/litellm/iam/` | IAM trust-policy + policy + README |

---

## Helm releases

```bash
CTX="arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform"

# Ver releases
helm list -n agentserver --kube-context $CTX

# Upgrade agentserver
helm upgrade agentserver deploy/helm/agentserver \
  -n agentserver --kube-context $CTX \
  -f values-dev-eks.yaml

# Upgrade litellm
helm upgrade litellm deploy/helm/litellm \
  -n agentserver --kube-context $CTX \
  -f values-litellm-dev-eks.yaml
```

---

## Customizações no upstream (patches vs agentserver/agentserver)

Todos os diffs estão em commits nossos no fork. Mudanças no chart `agentserver`:

### 1. Tolerations em todos os pod specs

Adicionado `{{- with .Values.tolerations }}` nos templates:
- `deployment.yaml` (app principal)
- `postgresql.yaml` (StatefulSet)
- `llmproxy.yaml`
- `sandboxproxy.yaml`
- `imbridge.yaml`
- `agent-sandbox-controller.yaml` (StatefulSet em `agent-sandbox-system`)

Valor em `values-dev-eks.yaml`:
```yaml
tolerations:
  - key: "internal-workers"
    value: "true"
    effect: "NoSchedule"
```

### 2. Custom annotations no ingress

`ingress.yaml` — adicionado bloco `{{- with .Values.ingress.annotations }}` para suportar ALB annotations via values (o chart original só suportava nginx + cert-manager).

### 3. LiteLLM chart (novo)

Chart novo em `deploy/helm/litellm/`. Funciona como proxy Anthropic-compatible → Bedrock:
- ServiceAccount com IRSA annotation
- ConfigMap com `model_list` (suporta `aws_bedrock_model_id` para inference profiles)
- Deployment + Service

---

## Configurações chave em values-dev-eks.yaml

```yaml
postgresql:
  image:
    repository: postgres       # override da mirror chinesa
    tag: "16-alpine"
  primary:
    persistence:
      storageClass: "gp3"      # cluster default EKS

platform:
  domain: "agentserver.analytics.vtex.com"

sandbox:
  baseDomain: "agentserver.analytics.vtex.com"  # requerido pelo sandboxproxy
  openclaw:
    image: "ghcr.io/agentserver/openclaw-agent:latest"

models:
  anthropicApiKey: "litellm-dev-key"            # master key do LiteLLM
  anthropicBaseUrl: "http://litellm.agentserver.svc.cluster.local:4000"

llmproxy:
  replicaCount: 1

ingress:
  enabled: true
  className: alb
  host: agentserver.analytics.vtex.com
  # ALB annotations: internal, cert *.analytics.vtex.com, subnets privadas

tolerations:
  - key: "internal-workers"
    value: "true"
    effect: "NoSchedule"
```

---

## Pendências

| Item | Prioridade | Detalhe |
|---|---|---|
| Secrets no git | 🟠 Alta | `internal.apiSecret`, `secretsPepper`, `masterKey` LiteLLM estão em texto plano em `values-dev-eks.yaml`. Mover para AWS Secrets Manager + External Secrets. |
| Wildcard DNS sandboxes | 🟡 Média | Sandboxes precisam de `*.agentserver.analytics.vtex.com` para routing via sandboxproxy. ALB já tem a regra, falta o DNS wildcard no Route53. |
| OpenClaw image versão | ⚪ Baixa | Usando `ghcr.io/agentserver/openclaw-agent:latest`. Pinnar versão específica para reproducibilidade. |
| Autenticação OIDC | ⚪ Baixa | Hydra desabilitado. Só password auth ativo. Configurar OIDC/SSO VTEX se necessário. |

---

## Testar LiteLLM → Bedrock

```bash
# Port-forward
kubectl --context arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform \
  port-forward svc/litellm 4000:4000 -n agentserver

# Teste Claude Sonnet 4.6 via inference profile
curl -X POST http://localhost:4000/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer litellm-dev-key" \
  -d '{"model":"bedrock/claude-sonnet-4-6","max_tokens":50,"messages":[{"role":"user","content":"ping"}]}'
```

---

## Histórico de decisões

| Decisão | Alternativa descartada | Motivo |
|---|---|---|
| Fork `agentserver/agentserver` | Build from scratch (nosso POC) | agentserver já resolve ~80% dos issues (#1-#14). 1-2 semanas vs 6-8. |
| LiteLLM como proxy Bedrock | Modificar llmproxy (Go) | Zero mudança no upstream; LiteLLM suporta Bedrock nativo via IRSA. |
| `aws_bedrock_model_id` param | `bedrock_converse` custom_llm_provider | LiteLLM 1.82.6 rejeita `bedrock_converse` no router; `aws_bedrock_model_id` funciona. |
| ALB interno + grupo dedicado | Shared ALB `ml-platform-eks-alb-shared` | Cluster dev não tem o shared ALB do prod configurado. |
| DNS `agentserver.analytics.vtex.com` | `agentserver.dev.vtex.io` | Zona `analytics.vtex.com` existe e tem cert wildcard disponível. |
