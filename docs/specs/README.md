---
title: "Agent Platform Multi-Tenant — OpenClaw/Hermes em Kubernetes"
date: 2026-05-25
tags: [project, multitenant, b2b, kubernetes, openclaw, hermes, whatsapp, voice, agentserver, saas]
author: Carlos
---


# Agent Platform Multi-Tenant — OpenClaw/Hermes em Kubernetes

Plataforma B2B que permite empresas criarem agentes de IA isolados (OpenClaw ou Hermes) para atendimento via WhatsApp e voz, rodando em Kubernetes com isolamento por tenant. Não depende de AWS Bedrock AgentCore.

## Visão

**Cliente B2B** assina a plataforma → cria um workspace → escolhe runtime (OpenClaw para conversação rica / Hermes para automação skill-heavy) → conecta canais (WhatsApp business number, voz via Twilio+Vapi, Discord, Telegram) → configura skills/persona → bot atende clientes em produção.

**Isolamento** por tenant: namespace K8s próprio, secrets isolados, S3/Postgres prefixados, cota de recursos.

**Pricing**: tiered por número de agents ativos + minutos de voz + mensagens WhatsApp.

## Projetos de referência

### 1. sample-host-openclaw-on-amazon-bedrock-agentcore (AWS)
[github.com/aws-samples/sample-host-openclaw-on-amazon-bedrock-agentcore](https://github.com/aws-samples/sample-host-openclaw-on-amazon-bedrock-agentcore)

- **Modelo**: per-user serverless containers em AgentCore Runtime
- **Isolamento**: Firecracker microVMs (uma por sessão)
- **Workspace**: `.openclaw/` sincronizado com S3 por user prefix
- **Identidade**: STS session-scoped credentials restringem acesso S3/DynamoDB ao namespace do usuário
- **Canais**: Telegram + Slack (sem WhatsApp/voz)
- **Arquitetura**: Router Lambda → DynamoDB identity → InvokeAgentRuntime → microVM
- **Latência**: ~5-15s response (lazy init), ~1-2min full startup background

**Por que não usar direto**: dependência AWS Bedrock AgentCore vendor-lock + custo per-session microVM + sem WhatsApp/voz.

### 2. sample-host-hermesagent-on-amazon-bedrock-agentcore (AWS)
[github.com/aws-samples/sample-host-hermesagent-on-amazon-bedrock-agentcore](https://github.com/aws-samples/sample-host-hermesagent-on-amazon-bedrock-agentcore)

- **Modelo**: idem OpenClaw mas com Hermes (40+ ferramentas)
- **Truque-chave**: monkey-patch `app/hermes/main.py` substitui `anthropic.Anthropic` por `anthropic.AnthropicBedrock` → roteia Claude via Bedrock sem chave Anthropic direta
- **Isolamento**: idem (Firecracker per session)
- **Canais**: Telegram, Slack, Discord, Feishu, WeChat (via ECS Fargate gateway)
- **Serviços AWS**: API Gateway + Lambda + ECS Fargate + S3 + DynamoDB + Secrets Manager + Bedrock

**Por que não usar direto**: mesma razão da #1.

### 3. agentserver (base — upstream)
[github.com/agentserver/agentserver](https://github.com/agentserver/agentserver)

> **Fork ativo deste projeto**: [github.com/CarlosSalvador-vtex/agentserver](https://github.com/CarlosSalvador-vtex/agentserver) — onde a customização B2B / K8s multitenant está sendo construída.

- **Stack**: Go (82.7%) + TS/React (14.1%) + Python (2.4%) + PostgreSQL
- **Runtime**: K8s nativo via Helm + Agent Sandbox + gVisor para isolamento multi-tenant
- **Multi-tenant**: namespaces K8s + RBAC (owner/maintainer/dev/guest)
- **Canais**: Telegram, WeChat/Weixin, Matrix (sem WhatsApp/voz)
- **Licença**: Apache-2.0 (permissiva, OK pra fork B2B)
- **Status**: ativo, 185 releases, último mai/2026, desenvolvimento contínuo

**Por que partir daí**: já tem multi-tenancy K8s + RBAC + isolamento gVisor + boa estrutura para extensão.

## Diferenças do projeto vs referências

| Aspecto | AWS samples | agentserver upstream | **Este projeto** |
|---------|-------------|---------------------|------------------|
| Runtime | Bedrock AgentCore (Firecracker) | K8s + gVisor | **K8s + gVisor** |
| Cloud | AWS exclusivo | Cloud-agnostic | **Cloud-agnostic + K8s on-prem** |
| Agent runtime | 1 fixo (OpenClaw OU Hermes) | gVisor sandbox | **Escolha por tenant: OpenClaw OU Hermes** |
| WhatsApp | ❌ | ❌ | **✓ via Twilio/Z-API/Evolution** |
| Voz | ❌ | ❌ | **✓ via Vapi (subprojeto)** |
| Modelo de uso | per-user no-pay (samples) | self-host pessoal | **B2B SaaS multitenant** |
| Modelo LLM | Bedrock Claude | qualquer | **per-tenant choice (Bedrock/zai/Anthropic/local)** |

## Arquitetura proposta

```
┌─────────────────────────────────────────────────────────────────┐
│ Control Plane (1 cluster K8s shared)                            │
│                                                                  │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────┐  │
│  │ Web UI       │  │ API Gateway  │  │ Tenant Provisioner   │  │
│  │ (React/TS)   │  │ (Go)         │  │ (Helm/K8s operator)  │  │
│  └──────────────┘  └──────────────┘  └──────────────────────┘  │
│                            │                                     │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │ Channel Router (Go)                                        │ │
│  │  WhatsApp webhooks → tenant lookup → tenant pod            │ │
│  │  Voice (Vapi webhook) → tenant lookup → tenant pod         │ │
│  │  Discord/Telegram → tenant lookup → tenant pod             │ │
│  └────────────────────────────────────────────────────────────┘ │
│                            │                                     │
│  Postgres (control plane) | Redis | S3/MinIO (workspaces)       │
└─────────────────────────────────────────────────────────────────┘
                             │
        ┌────────────────────┼────────────────────┐
        ▼                    ▼                    ▼
┌──────────────────┐ ┌──────────────────┐ ┌──────────────────┐
│ tenant-acme      │ │ tenant-foo       │ │ tenant-bar       │
│ (namespace)      │ │ (namespace)      │ │ (namespace)      │
│                  │ │                  │ │                  │
│ ┌──────────────┐ │ │ ┌──────────────┐ │ │ ┌──────────────┐ │
│ │ openclaw pod │ │ │ │ hermes pod   │ │ │ │ hermes pod   │ │
│ │ + gVisor     │ │ │ │ + gVisor     │ │ │ │ + gVisor     │ │
│ │              │ │ │ │              │ │ │ │              │ │
│ │ /workspace   │ │ │ │ /.hermes     │ │ │ │ /.hermes     │ │
│ └──────────────┘ │ │ └──────────────┘ │ │ └──────────────┘ │
│ PVC (workspace)  │ │ PVC (workspace)  │ │ PVC (workspace)  │
│ Secrets isolados │ │ Secrets isolados │ │ Secrets isolados │
│ ResourceQuota    │ │ ResourceQuota    │ │ ResourceQuota    │
└──────────────────┘ └──────────────────┘ └──────────────────┘
```

### Componentes principais

1. **Web UI** (React/TS, herda do agentserver)
   - Onboarding de tenant
   - Configuração de canais (conectar WhatsApp business, número Twilio, etc.)
   - Editor de skills/personas
   - Dashboard de métricas (mensagens, custos, latência)

2. **API Gateway** (Go, REST + WebSocket)
   - Auth: JWT + tenant_id claim
   - Quotas e rate limiting por tenant
   - Audit log centralizado

3. **Tenant Provisioner** (K8s operator custom OU Helm)
   - Recebe `CreateTenant` request
   - Cria namespace `tenant-{slug}`
   - Aplica `NetworkPolicy` (isolamento de rede entre tenants)
   - Aplica `ResourceQuota` (CPU/RAM/storage caps)
   - Provisiona pod OpenClaw OU Hermes conforme escolha
   - Cria PVC para workspace persistente
   - Cria Secrets isolados (LLM API keys, WhatsApp tokens, etc.)

4. **Channel Router** (Go, central)
   - WhatsApp: webhook → resolve tenant via número receptor → forward pro pod do tenant
   - Voice (Vapi): webhook similar
   - Telegram/Discord: tokens isolados por tenant
   - **Importante**: cada tenant tem credenciais próprias dos canais (não compartilha)

5. **Storage**
   - **Postgres (control plane)**: tenants, users, billing, audit log
   - **S3/MinIO**: workspaces por tenant (`s3://workspaces/{tenant_id}/...`)
   - **Redis**: sessões, rate limits, distributed locks

6. **Per-tenant pods**
   - OpenClaw: imagem oficial `npm install -g openclaw` + workspace montado
   - Hermes: imagem com `hermes-agent` Python + skills montadas
   - Isolamento: gVisor runtime class (`runtimeClassName: gvisor`)
   - Network policy: deny all + allow apenas Channel Router + LLM provider

## Multi-tenancy strategy

### Isolamento

| Layer | Mecanismo |
|-------|-----------|
| Compute | Namespace K8s separado por tenant |
| Process | gVisor sandbox (syscall filtering) |
| Network | NetworkPolicy deny-all + allowlist |
| Storage | PVC dedicado + S3 prefix por tenant |
| Secrets | K8s Secret no namespace do tenant |
| LLM | API key próprio (tenant traz suas chaves OU plataforma fatura) |
| Auth | JWT scoped a tenant_id, RBAC dentro do tenant (owner/dev/guest) |
| Audit | Log centralizado com tenant_id em toda linha |

### Trade-offs

**Namespace por tenant** (escolhido):
- ✓ Isolamento forte, K8s-native
- ✓ Backup/restore por tenant trivial
- ✓ Tenant pode ser migrado de cluster
- ✗ Overhead de admin (muitos namespaces)
- ✗ Não escala pra milhares de tenants num cluster só

**Alternativas consideradas e rejeitadas**:
- Pod por tenant no mesmo namespace: isolamento fraco, gVisor ajuda mas não substitui boundary K8s
- Database row-level tenancy: tem que reescrever OpenClaw/Hermes, fora de escopo
- Cluster por tenant: cara demais, só justifica em enterprise tier

## Canais suportados (roadmap)

### Phase 1 (MVP)
- **WhatsApp Business API** via Z-API ou Evolution API (ambas brasileiras, mais baratas que Twilio WhatsApp)
- **Discord / Telegram** (herdado do agentserver, já funciona)

### Phase 2
- **Voz** (outbound + inbound) — ver subprojeto [voice-agent-cobranca](./voice-agent-cobranca.md) como vertical inicial
- **Slack** (B2B comum)

### Phase 3
- **SMS** (Twilio)
- **Email** (Postmark/Resend)
- **Instagram DM, Facebook Messenger** (Meta cloud API)

## Verticais / Casos de uso B2B

Cada vertical = um conjunto de skills pré-configuradas + persona + canais sugeridos.

1. **Cobrança / debt collection** — ver [voice-agent-cobranca](./voice-agent-cobranca.md) (sub-projeto)
2. **Atendimento e-commerce VTEX/Shopify** — track pedido, devolução, troca
3. **Agendamento médico/saúde** — confirmar consulta, remarcar
4. **Imobiliário** — qualificar lead, agendar visita
5. **Educação** — atendimento secretaria escola
6. **SDR / vendas outbound** — qualificar leads frios

## Stack tecnológico

Herdado de `agentserver`:
- Go (control plane, API, router)
- TypeScript/React (Web UI)
- PostgreSQL (state)
- Helm (deploy K8s)

Adições:
- **Agent runtimes**: OpenClaw npm + Hermes Python (containerizados)
- **gVisor** (runtimeClassName) para syscall sandbox
- **Z-API / Evolution API** clients para WhatsApp
- **Vapi SDK** para voz
- **MinIO** se on-prem (alternativa a S3)
- **Prometheus + Grafana** observability
- **OpenTelemetry** para tracing distribuído

## Modelo de negócio (rascunho)

| Tier | Preço/mês | Inclui |
|------|-----------|--------|
| Starter | R$ 99 | 1 agent, 1000 msgs WhatsApp, 30min voz, suporte comunidade |
| Pro | R$ 499 | 5 agents, 10k msgs, 300min voz, skills customizáveis |
| Business | R$ 2.499 | 25 agents, 100k msgs, 2000min voz, SLA 99%, suporte dedicado |
| Enterprise | sob demanda | unlimited, on-prem, suporte 24/7 |

**Custos variáveis** (passar pro cliente ou absorver):
- LLM tokens: $0.5-3 / 1M tokens (modelo dependente)
- WhatsApp: ~R$ 0.05/msg conversation
- Voz: ~R$ 0.30/min (Vapi + Twilio)

## Compliance e segurança

- **LGPD**: data residency BR (cluster K8s em região BR), opt-in explícito, audit log retenção
- **SOC 2** (long-term)
- **Tenant isolation**: certificar com pentest periódico
- **Anti-prompt-injection**: input sanitization no Channel Router antes de chegar ao LLM
- **Rate limiting**: por tenant + por usuário-final

## Riscos e mitigação

| Risco | Probabilidade | Mitigação |
|-------|---------------|-----------|
| WhatsApp banir número (anti-spam) | Alta | Z-API/Evolution oferecem warm-up, ou WhatsApp Business API oficial Twilio (caro mas seguro) |
| Tenant escapar gVisor sandbox | Baixa | Defesa em profundidade (NetworkPolicy + RBAC + audit) |
| LLM cost overrun por tenant | Alta | Hard quota por tenant + alertas |
| Cobrança via voz cair em multa | Média | Compliance LGPD/CDC desde início (ver subprojeto voice) |
| Latência alta em hora de pico | Alta | HPA por tenant + warm pod pool |
| Vendor lock LLM provider | Média | Litellm/portkey como proxy, troca provider sem refactor |

## Roadmap macro

### M1 — Foundation (4 semanas)
- [x] Fork agentserver (github.com/CarlosSalvador-vtex/agentserver)
- [ ] Estudar arquitetura upstream
- [ ] PoC: rodar 1 OpenClaw + 1 Hermes em K8s local (kind/minikube) num namespace cada
- [ ] Channel router básico (Telegram only, ainda)
- [ ] Web UI: criar tenant, ver status

### M2 — WhatsApp + Multi-tenant real (6 semanas)
- [ ] Integração Z-API / Evolution API
- [ ] Tenant Provisioner como K8s Operator
- [ ] gVisor + NetworkPolicy
- [ ] Postgres schema multi-tenant
- [ ] Auth + JWT scoped

### M3 — Voz (subprojeto) (4 semanas)
- [ ] Ver [voice-agent-cobranca](./voice-agent-cobranca.md)
- [ ] Vapi integration no Channel Router
- [ ] Template cobrança como primeiro vertical

### M4 — Production hardening (8 semanas)
- [ ] Helm chart oficial
- [ ] Prometheus + Grafana dashboards
- [ ] Billing engine (Stripe/Iugu)
- [ ] Audit log + compliance LGPD
- [ ] Pentest interno
- [ ] Beta com 3-5 clientes

### M5 — GA (4 semanas)
- [ ] Web UI polida
- [ ] Docs públicas
- [ ] Marketplace de skills (pluginsistema)
- [ ] Launch

## Subprojetos

- [voice-agent-cobranca](./voice-agent-cobranca.md) — vertical de voz para cobrança, primeiro caso de uso da camada de voz

## Documentos do projeto

- [frontend-spec](./frontend-spec.md) — especificação do frontend (React/TS, shadcn, TanStack)

## Decisões pendentes

1. **Naming**: `agent-platform-multitenant` é placeholder. Brand B2B precisa de nome curto (Clawhub? Hermeshub? Botstack?).
2. **OpenClaw vs Hermes default**: oferecer ambos OU forçar um? Trade-off curva de aprendizado vs flexibilidade.
3. **LLM**: trazer modelo próprio (tenant paga API key dele) OU plataforma intermedia (margem + lock-in)?
4. **WhatsApp provider**: Z-API (mais barato, BR) vs Twilio (oficial, internacional)?
5. **Cluster**: gerenciado (EKS/GKE) vs self-managed (k3s on-prem) vs híbrido?

## Conceitos Relacionados

- [voice-agent-cobranca](./voice-agent-cobranca.md) — subprojeto de voz
- **2026-05-22-hermes-vs-openclaw-latency** — comparativo de performance dos runtimes
- **2026-05-22-openclaw-vs-hermes-skill-patterns** — diferença de modelo de skills
- **microvms-firecracker-kubernetes** — sandbox alternativo a gVisor
- **agent-governance-toolkit** — guardrails em produção

## Referências

- [sample-host-openclaw-on-amazon-bedrock-agentcore (GitHub AWS)](https://github.com/aws-samples/sample-host-openclaw-on-amazon-bedrock-agentcore)
- [sample-host-hermesagent-on-amazon-bedrock-agentcore (GitHub AWS)](https://github.com/aws-samples/sample-host-hermesagent-on-amazon-bedrock-agentcore)
- [agentserver — K8s multi-tenant agent platform (GitHub upstream)](https://github.com/agentserver/agentserver)
- [**Fork ativo deste projeto** (GitHub Carlos)](https://github.com/CarlosSalvador-vtex/agentserver)
- [Z-API (WhatsApp Brazil)](https://z-api.io)
- [Evolution API (WhatsApp open source)](https://github.com/EvolutionAPI/evolution-api)
- [gVisor — application kernel for containers](https://gvisor.dev/)
- [Vapi voice AI](https://vapi.ai)
- [Twilio Programmable Messaging WhatsApp](https://www.twilio.com/whatsapp)
