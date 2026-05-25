---
title: "Frontend Spec — Agent Platform Multi-Tenant"
date: 2026-05-25
tags: [frontend, spec, react, typescript, ui, ux, multitenant, b2b, design-system]
author: Carlos
---


# Frontend Spec — Agent Platform Multi-Tenant

Especificação do frontend B2B SaaS. React/TS herdado de `agentserver`. Tailwind + shadcn/ui.

## Personas e roles

| Persona | Role | Acesso |
|---------|------|--------|
| **Carlos Owner (tenant admin)** | `owner` | Tudo dentro do tenant: agents, channels, billing, users, settings |
| **Dev integrador** | `developer` | Agents, skills, channels, conversations. NÃO vê billing. |
| **Operador suporte** | `operator` | Conversations (read + intervir), dashboards. NÃO toca em config. |
| **Convidado** | `guest` | Read-only em dashboard e conversations específicas |
| **Super-admin (platform)** | `platform_admin` | Cross-tenant: gestão de tenants, billing global, métricas plataforma |

RBAC herdado de agentserver (4 roles) + adicionar `operator` (gap relevante p/ atendimento) + `platform_admin` (camada superior).

## Stack frontend

| Camada | Escolha | Motivo |
|--------|---------|--------|
| Framework | React 19 + TypeScript 5.x | Herda agentserver |
| Build | Vite | Padrão moderno, rápido |
| Roteamento | TanStack Router | Type-safe, melhor que React Router 6 |
| Server state | TanStack Query v5 | Cache, mutations, optimistic UI |
| UI state | Zustand | Mais simples que Redux, suficiente |
| Styling | Tailwind CSS v4 | Já é a base de shadcn |
| Components | shadcn/ui | Copy-paste, sem vendor lock |
| Forms | React Hook Form + Zod | Validação ponta-a-ponta |
| Charts | Tremor + Recharts | Tremor pra dashboard B2B, Recharts pra custom |
| WebSocket | native + reconnecting-ws | Live updates de conversas/status |
| i18n | i18next | pt-BR primário + en-US secundário |
| Tema | next-themes (dark/light) | Padrão indústria |
| Icons | lucide-react | Consistente com shadcn |
| Date/time | date-fns + date-fns-tz | Timezone-aware (tenant pode ser US ou BR) |
| Testing | Vitest + Playwright + MSW | Unit + E2E + mock backend |

## Estrutura de pastas

```
web/
├── src/
│   ├── app/                   # rotas (TanStack file-based)
│   │   ├── _public/           # login, signup, marketing pages
│   │   ├── _admin/            # super-admin (platform_admin only)
│   │   └── _tenant/           # tenant-scoped (default)
│   │       ├── dashboard
│   │       ├── agents
│   │       ├── channels
│   │       ├── skills
│   │       ├── conversations
│   │       ├── analytics
│   │       ├── billing
│   │       └── settings
│   ├── components/
│   │   ├── ui/                # shadcn primitives
│   │   ├── shared/            # AppShell, Sidebar, Topbar, etc.
│   │   ├── agents/            # AgentCard, AgentDetail, RuntimePicker
│   │   ├── channels/          # ChannelGrid, ChannelConnectWizard
│   │   ├── skills/            # SkillBrowser, SkillEditor
│   │   └── conversations/     # ConversationList, ChatThread, MessageBubble
│   ├── lib/
│   │   ├── api/               # TanStack Query hooks per resource
│   │   ├── ws/                # WebSocket client
│   │   ├── auth/              # JWT, tenant_id context
│   │   └── utils.ts
│   ├── hooks/
│   ├── stores/                # Zustand
│   └── types/                 # gerados via OpenAPI ou tRPC
```

## Layout global (AppShell)

```
┌──────────────────────────────────────────────────────────────────┐
│ Topbar                                                            │
│ [Logo Tenant] [Workspace ▾]    [Search] [Notif] [Avatar Owner ▾] │
├──────────┬───────────────────────────────────────────────────────┤
│ Sidebar  │                                                        │
│          │                                                        │
│ 🏠 Home  │                                                        │
│ 🤖 Agents│                                                        │
│ 📡 Channs│                Main content area                       │
│ 🧩 Skills│                                                        │
│ 💬 Convos│                                                        │
│ 📊 Analyt│                                                        │
│ 💰 Bill  │                                                        │
│ ⚙ Settgs │                                                        │
│          │                                                        │
│ (rodapé) │                                                        │
│ Theme tg │                                                        │
│ Help     │                                                        │
└──────────┴───────────────────────────────────────────────────────┘
```

- Workspace switcher no topo (user pode pertencer a mais de 1 tenant)
- Sidebar colapsável (icon-only mode)
- Comando palette `Cmd+K` (busca global: agents, conversations, settings)

## Páginas / fluxos principais

### 1. Onboarding / Signup

**Fluxo wizard (5 passos)**:

1. **Conta** — email, senha, nome empresa
2. **Workspace** — slug do tenant (vai virar subdomain `acme.platform.com`)
3. **Runtime** — escolher OpenClaw ou Hermes (com tooltip explicando diferenças via **2026-05-22-openclaw-vs-hermes-skill-patterns**)
4. **Canal inicial** — conectar pelo menos 1 (WhatsApp, Discord, Telegram, Voice)
5. **Persona** — escolher template (atendimento, vendas, cobrança...) ou pular

**Tela 3 mockup**:
```
┌──────────────────────────────────────────────────────┐
│ 3/5 — Escolha o runtime do seu agente               │
├──────────────────────────────────────────────────────┤
│                                                       │
│  ┌─────────────────┐    ┌─────────────────┐         │
│  │ 🦞 OpenClaw     │    │ 🤖 Hermes       │         │
│  │                 │    │                  │         │
│  │ Conversação     │    │ Automação        │         │
│  │ rica, flexível  │    │ skill-heavy,     │         │
│  │                 │    │ scripts          │         │
│  │ ▸ Web tool      │    │ ▸ 40+ skills     │         │
│  │ ▸ Browser       │    │ ▸ K8s ops        │         │
│  │ ▸ Cron jobs     │    │ ▸ Multi-channel  │         │
│  │                 │    │                  │         │
│  │ Latência média  │    │ Latência menor   │         │
│  │ ~5-15s          │    │ em cold path     │         │
│  │                 │    │                  │         │
│  │ [ Selecionar ]  │    │ [ Selecionar ]  │         │
│  └─────────────────┘    └─────────────────┘         │
│                                                       │
│  Não tem certeza? Pode mudar depois.                │
│                                                       │
│         [Voltar]              [Continuar →]          │
└──────────────────────────────────────────────────────┘
```

### 2. Dashboard (Home)

**Métricas top** (Tremor cards):
- Mensagens últimas 24h / 7d (line chart)
- Conversations ativas (counter + delta)
- Agents online / offline (status pills)
- Custo do mês (LLM + canais) com forecast
- SLA p95 latência response

**Sessions abaixo**:
- Conversas ativas em tempo real (live via WS)
- Alertas (falhas, custos altos, channels caídos)

### 3. Agents

**List view** (`/agents`):
```
┌─────────────────────────────────────────────────────────┐
│ Agents                              [+ Criar agent]     │
├─────────────────────────────────────────────────────────┤
│  Nome          Runtime    Canais       Status   Ações   │
│  ──────────────────────────────────────────────────     │
│  Clara         OpenClaw   WA, Voice    🟢 Ativo  ⋯      │
│  Suporte       Hermes     Discord      🟢 Ativo  ⋯      │
│  Vendas        OpenClaw   WA, Tel      🟡 Sandbox⋯      │
└─────────────────────────────────────────────────────────┘
```

**Detail view** (`/agents/:id`):
- Tab `Visão geral` — métricas do agent
- Tab `Persona` — system prompt editor (Monaco)
- Tab `Skills` — toggle on/off por skill, com per-channel override
- Tab `Channels` — quais canais este agent atende
- Tab `Memory` — visualizar MEMORY.md (Obsidian-style preview)
- Tab `Logs` — request logs (com filter por channel/timeframe)
- Tab `Test playground` — chat embutido para testar antes de produção

### 4. Channels

**Grid view** (`/channels`):
```
┌────────────────────────────────────────────────────────┐
│ Channels conectados                  [+ Conectar canal]│
├────────────────────────────────────────────────────────┤
│  📱 WhatsApp Business      🟢 Conectado                │
│     +55 11 99999-9999  ·  Z-API  ·  Agent: Clara       │
│                                                          │
│  🎙 Voz (Vapi)             🟢 Conectado                │
│     +55 11 4002-8922  ·  Vapi  ·  Agent: Clara         │
│                                                          │
│  💬 Discord                🟢 Conectado                │
│     Server: Acme Corp  ·  Agent: Suporte               │
│                                                          │
│  📨 Telegram               🟡 Setup pendente           │
│     Bot @acme_bot  ·  Token inválido [Reconectar]      │
└────────────────────────────────────────────────────────┘
```

**Connect wizard** (clicar em "+ Conectar"):
- Step 1: escolher tipo (WhatsApp/Discord/Telegram/Voice/Slack/...)
- Step 2: provider-specific (Z-API vs Twilio para WhatsApp, etc.)
- Step 3: credenciais (com docs inline e link helper)
- Step 4: associar com qual agent
- Step 5: teste (manda msg de teste, espera echo)

### 5. Skills

**Browser view** (`/skills`):
- Lista de skills disponíveis (catalog público + custom do tenant)
- Filtros: categoria, runtime (OpenClaw/Hermes), official/community
- Cada skill = card com nome, descrição, tags, install count, rating

**Skill detail / editor**:
- README rendered (markdown)
- Frontmatter editor (form-driven, gera YAML)
- Body editor (Monaco markdown)
- Scripts dir (file tree + Monaco)
- "Install in agent" button → modal escolhendo qual agent

### 6. Conversations

**Inbox-style layout**:
```
┌────────────┬───────────────────────────────────────────┐
│ Filtros    │  +55 11 99... @ WhatsApp / Clara          │
│            │  ─────────────────────────────────────    │
│ [All ▼]    │  [Cliente] Oi, recebi meu boleto?         │
│ [WA]       │           15:23                            │
│ [Voice]    │                                            │
│ [Discord]  │             [Clara] Olá! Vou verificar... │
│            │             15:23                          │
│ ─────────  │                                            │
│ Conversas  │  [Cliente] Obrigado!                       │
│            │           15:24                            │
│ Cliente A  │                                            │
│ • 2 min    │                                            │
│            │  ───────────────────────────────────────  │
│ Cliente B  │  [Intervir] [Pausar bot] [Atribuir]        │
│ • 5 min    │                                            │
│ ...        │                                            │
└────────────┴───────────────────────────────────────────┘
```

- Lista esquerda: conversations ativas com unread count
- Painel central: thread completa com syntax highlight para tool calls
- Painel direito (collapsible): metadata (lead info, custo da conversa, sentimento)
- Operador pode **intervir** (assume controle, bot pausa) — caso crítico em cobrança/suporte
- Filtros por canal, status (bot ativo / humano / resolved), tag, data

### 7. Analytics

**Multi-tab dashboard**:
- **Volume**: msgs/dia, por canal, por agent
- **Latência**: p50/p90/p99, drill-down por skill
- **Qualidade**: % de conversas resolvidas pelo bot vs escaladas, CSAT
- **Custo**: $/conversa, breakdown LLM/canais/voz
- **Funnel** (B2B vertical): leads → qualificados → fechados (configurável por tenant)

### 8. Billing

- Plano atual + uso vs limite (progress bars)
- Próxima cobrança (data + valor estimado)
- Histórico de invoices (download PDF)
- Métodos de pagamento (Stripe/Iugu Elements)
- Upgrade/downgrade (com prorate preview)

### 9. Settings

- **Workspace**: nome, slug, timezone, idioma, branding (logo, cores customizadas)
- **Users + RBAC**: convidar, editar role, revogar
- **API keys**: gerar tokens pra integração externa
- **Webhooks**: configurar callbacks (msg recebida, conversa fechada, etc.)
- **Integrations**: Slack notifications, Linear, Jira (pra escalar tickets), CRMs
- **Compliance**: LGPD export, audit log download, data retention

### 10. Admin (super-admin only)

- Lista de todos os tenants (search, filter por status/tier/criação)
- Detail de tenant: uso, billing health, support tickets
- Provisionar novo tenant manualmente (debug)
- Métricas plataforma: tenants ativos, MRR, churn, infra cost por tenant
- Feature flags (rollout gradual)

## Design system

### Cores (semantic tokens)

```css
--color-primary: oklch(0.55 0.18 250);     /* azul tech */
--color-success: oklch(0.65 0.18 145);     /* verde */
--color-warning: oklch(0.78 0.15 75);      /* amarelo */
--color-danger:  oklch(0.62 0.22 25);      /* vermelho */
--color-muted:   oklch(0.55 0 0);          /* cinza */

/* Por runtime */
--color-openclaw: oklch(0.7 0.18 30);      /* laranja lagosta */
--color-hermes:   oklch(0.65 0.18 280);    /* roxo */
```

### Tipografia

- **UI**: Inter ou Geist Sans
- **Mono** (logs, code, terminais): JetBrains Mono ou Geist Mono

### Status pills

| Estado | Cor | Uso |
|--------|-----|-----|
| 🟢 Ativo | success | Agent rodando, channel conectado |
| 🟡 Atenção | warning | Setup pendente, quota >80% |
| 🔴 Erro | danger | Down, falha auth, banido |
| 🔵 Sandbox | primary | Modo teste |
| ⚪ Pausado | muted | User pausou |

### Princípios visuais

- **Density**: alta em listas (dashboards de operação), média em settings, baixa em onboarding
- **Spacing**: 4px base, múltiplos
- **Bordas**: 8px radius default, 4px em itens densos
- **Shadows**: sutis (oklch alpha low), só pra cards elevados
- **Animations**: 150-250ms ease-out, suaves. Sem motion para data tables (perf)

## WebSocket / realtime

**Conexão**: 1 WS por sessão, autenticada com JWT, multiplexada por topics.

**Topics**:
- `tenant:{id}:agents:status` — mudanças de status de agent
- `tenant:{id}:conversations:new` — nova mensagem recebida
- `tenant:{id}:conversations:{conv_id}` — stream de uma conversa específica
- `tenant:{id}:alerts` — alertas em tempo real (quota, falha)

**Reconnect**: exponential backoff + resume from last_event_id.

## API contract

Backend Go expõe REST + WS. OpenAPI 3.1 gera types TS automaticamente.

Recursos REST:
- `GET /api/tenants/me` — tenant atual
- `GET/POST/PATCH/DELETE /api/agents`
- `GET/POST/PATCH/DELETE /api/channels`
- `GET/POST/PATCH/DELETE /api/skills`
- `GET /api/conversations?status=&channel=`
- `POST /api/conversations/:id/intervene`
- `GET /api/analytics/volume?range=`
- ...

Auth: Bearer JWT em todas, exceto `/auth/*`. JWT carrega `tenant_id` claim, backend valida no middleware.

## Estado / data flow

### Server state (TanStack Query)

```ts
// Exemplo: hook listar agents
function useAgents() {
  return useQuery({
    queryKey: ['agents'],
    queryFn: () => api.get('/api/agents').then(r => r.data),
    staleTime: 30_000,
  });
}

// Mutation com optimistic update
function useUpdateAgent() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (agent: Agent) => api.patch(`/api/agents/${agent.id}`, agent),
    onMutate: async (next) => {
      await qc.cancelQueries({ queryKey: ['agents'] });
      const prev = qc.getQueryData<Agent[]>(['agents']);
      qc.setQueryData<Agent[]>(['agents'], (old) =>
        old?.map(a => a.id === next.id ? next : a)
      );
      return { prev };
    },
    onError: (_e, _v, ctx) => qc.setQueryData(['agents'], ctx?.prev),
    onSettled: () => qc.invalidateQueries({ queryKey: ['agents'] }),
  });
}
```

### UI state (Zustand)

Coisas pequenas: sidebar collapsed, theme, comando palette aberto, filtros locais.

### Auth state

JWT em httpOnly cookie + tenant_id em context. Refresh token rotativo.

## Acessibilidade

- WCAG 2.2 AA mínimo
- Keyboard nav em todas as listas e modals
- aria-live em notifications e WS updates
- Contraste mínimo 4.5:1 em texto
- Focus visible em todos os interativos
- Skip-to-content link
- Suporte a screen reader em tabelas (proper headers)

## i18n

- pt-BR primário (mercado-alvo Brasil)
- en-US secundário (clientes internacionais)
- Strings em `locales/{lang}/{namespace}.json`
- Namespaces: `common`, `agents`, `channels`, `skills`, `conversations`, `billing`, `settings`
- Datas/números via `Intl.*` API com locale do tenant

## Performance

- **Code splitting** por rota (TanStack Router suporta nativo)
- **Prefetch** em hover de links de navegação
- **Virtualize** lists longas (Conversations, Logs) com TanStack Virtual
- **Image optimization**: AVIF/WebP, lazy
- **Bundle budget**: <250KB initial gzipped, <50KB per route chunk
- **Lighthouse alvo**: 95+ Performance, 100 A11y/Best Practices/SEO

## Telemetria

- **Posthog** ou **Plausible** (privacy-first) para product analytics
- Eventos importantes: tenant_created, agent_created, channel_connected, first_message_sent, upgrade_clicked, cancel_intent
- Sentry pra error tracking (com tenant_id como tag)

## Mockups / wireframes a produzir

| # | Tela | Prioridade |
|---|------|------------|
| 1 | Login + signup | P0 |
| 2 | Onboarding wizard 5 steps | P0 |
| 3 | Dashboard | P0 |
| 4 | Agents list + detail (tabs) | P0 |
| 5 | Channels grid + connect wizard | P0 |
| 6 | Conversations inbox | P0 |
| 7 | Skills browser | P1 |
| 8 | Analytics multi-tab | P1 |
| 9 | Billing | P1 |
| 10 | Settings (5 sub-screens) | P1 |
| 11 | Admin (super-admin) | P2 |
| 12 | Empty states + error states | P2 |
| 13 | Mobile responsive | P2 |

Ferramenta sugerida: **Figma** + **shadcn-ui Figma kit**.

## Roadmap FE alinhado com macro

| Milestone | Telas P0 entregues | Tempo |
|-----------|---------------------|-------|
| M1 | Login, dashboard básico, agents list | 4 sem |
| M2 | Channels (WhatsApp), connect wizard, agents detail | 6 sem |
| M3 | Conversations inbox + intervir + voice integration UI | 4 sem |
| M4 | Analytics, billing, settings, polish | 6 sem |
| M5 | Admin, mobile, i18n EN, polish final | 4 sem |

## Decisões pendentes FE

1. **TanStack Router vs Next.js App Router** — Next traz SSR/SEO out-of-box, TanStack é melhor pra SPA dashboard puro. Para B2B SaaS dashboard, TanStack é mais alinhado.
2. **shadcn/ui vs Mantine vs Chakra v3** — shadcn é a tendência (copy-paste, sem dep), mas curva de customização inicial maior.
3. **Charts**: Tremor (curva zero) vs Recharts (mais flexível) vs Visx (mais poder, mais código).
4. **Embeddable widget** (chat embarcado no site do cliente final): escopo Phase 4 ou Phase 1?
5. **White-label**: tenant pode customizar logo/cores. Quão fundo (favicon, email templates, subdomain custom)?
6. **Real-time conversations**: SSE vs WebSocket? WS dá mais flexibilidade mas SSE é mais simples e auto-reconecta.

## Conceitos Relacionados

- [README](./README.md) — projeto pai
- [voice-agent-cobranca](./voice-agent-cobranca.md) — vertical de voz
- **2026-05-22-openclaw-vs-hermes-skill-patterns** — diferença runtimes (relevante pro UI educar)

## Referências externas

- [shadcn/ui](https://ui.shadcn.com)
- [TanStack Router](https://tanstack.com/router)
- [TanStack Query](https://tanstack.com/query)
- [Tremor — React components for dashboards](https://tremor.so)
- [Geist UI font](https://vercel.com/font)
- [WCAG 2.2 quick reference](https://www.w3.org/WAI/WCAG22/quickref/)
- [Refactoring UI (Tailwind founders' book)](https://refactoringui.com/book)
- [Vercel commerce as B2B SaaS UI reference](https://demo.vercel.store)
