# Multi-Channel Routing — N:M sandbox ↔ channel bindings

> **Status:** PR #3 (`feat/multi-channel-routing`) merged into `main`.
> Foundational layer only. Auto-provisioner, UI, and WhatsApp provider
> ship in follow-up PRs.

## Por que existe

Antes deste PR, cada `workspace_im_channels` apontava para no máximo 1 sandbox via FK `sandboxes.im_channel_id`. Isso forçava **1 pod por canal** — empresa com 3 números de WhatsApp = 3 pods rodando + 3 imagens de agente em memória, mesmo quando a persona/skill é a mesma.

Para B2B multi-tenant isso é caro e contra-intuitivo:

| Cenário | Hoje (antes) | Agora |
|---|---|---|
| 1 empresa, 3 canais WA, mesma persona | 3 pods | 1 pod (modo `shared`) |
| 1 empresa, 3 canais WA, personas diferentes | 3 pods | 3 pods (modo `per_agent`) |
| Híbrido — 2 canais compartilham, 1 isolado | impossível | 2 pods (modo `hybrid`) |

## Modelo de dados

### Tabela junction (nova)

```sql
CREATE TABLE sandbox_channel_bindings (
    sandbox_id TEXT NOT NULL REFERENCES sandboxes(id)             ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES workspace_im_channels(id) ON DELETE CASCADE,
    bound_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (sandbox_id, channel_id)
);
```

Uma única tabela cobre os 3 cenários — basta variar quantas linhas têm o mesmo `sandbox_id`:

```
sandbox_channel_bindings
┌──────────────┬──────────────┐
│ sandbox_id   │ channel_id   │
├──────────────┼──────────────┤
│ sandbox-A    │ wa-vendas    │  ← shared: 1 sandbox, 3 canais
│ sandbox-A    │ wa-suporte   │
│ sandbox-A    │ telegram-bot │
├──────────────┼──────────────┤
│ sandbox-B    │ wa-vendas    │  ← per_agent: 3 sandboxes, 3 canais
│ sandbox-C    │ wa-suporte   │
│ sandbox-D    │ telegram-bot │
├──────────────┼──────────────┤
│ sandbox-E    │ wa-vendas    │  ← hybrid: 1 sandbox compartilha 2 canais,
│ sandbox-E    │ wa-suporte   │     outro canal tem seu próprio
│ sandbox-F    │ wa-cobranca  │
└──────────────┴──────────────┘
```

### Coluna de estratégia

```sql
ALTER TABLE workspaces
    ADD COLUMN channel_routing_strategy TEXT NOT NULL DEFAULT 'shared';
-- Valores: 'shared' | 'per_agent' | 'hybrid'
```

Validação é app-layer (`internal/db/workspaces.go::ValidRoutingStrategies`). O default `'shared'` é a alavanca de custo mais agressiva para SMB; enterprise muda para `per_agent` ou `hybrid` na criação do workspace.

### FK legado (mantido)

```sql
sandboxes.im_channel_id  -- ainda existe, ainda é dual-written
```

A FK 1:1 antiga **não foi removida**. Reads preferem a junction; quando junction está vazia (dados pré-migration que ainda não passaram por Bind/Unbind), fallback é o FK. Rollback da imagem volta a usar só FK sem perda de dados.

## Camadas

### 1. Migration

`internal/db/migrations/031_multi_channel_routing.sql`:

1. Adiciona `channel_routing_strategy` em `workspaces`.
2. Cria `sandbox_channel_bindings` com índices em ambas FKs.
3. **Backfill**: copia `(sandboxes.id, sandboxes.im_channel_id)` para junction onde `im_channel_id IS NOT NULL`. Idempotente via `ON CONFLICT DO NOTHING`.

### 2. DB layer (`internal/db/`)

**Novo arquivo:** `sandbox_channel_bindings.go`

| Helper | Contrato |
|---|---|
| `BindSandboxChannels(sandboxID, channelIDs)` | N:1 — não desloca outros sandboxes |
| `UnbindSandboxChannel(sandboxID, channelID)` | Remove um par específico |
| `UnbindAllSandboxChannels(sandboxID)` | Limpa junction do sandbox (defensivo; CASCADE já cobre) |
| `GetSandboxForChannelViaBinding(channelID)` | Lookup `running` + `pod_ip != ''`. Tiebreak: `bound_at DESC` |
| `GetChannelsForSandbox(sandboxID)` | Lista canais bound, ordem `bound_at` |
| `GetSharedSandbox(workspaceID)` | Para o auto-provisioner (PR 2): sandbox running com mais bindings |

**Mudanças em `im_channels.go`:**

- `BindSandboxToChannel(sandboxID, channelID)` — mantém semântica 1:1 mas dual-writes:
  ```go
  // dentro de uma transação:
  UPDATE sandboxes SET im_channel_id = NULL WHERE im_channel_id = $channelID
  UPDATE sandboxes SET im_channel_id = $channelID WHERE id = $sandboxID
  DELETE FROM sandbox_channel_bindings WHERE channel_id = $channelID
  INSERT INTO sandbox_channel_bindings (sandbox_id, channel_id) VALUES (...)
  ```
- `UnbindSandboxFromChannel(sandboxID)` — limpa FK + junction em transação.
- `GetSandboxForChannel(channelID)` — junction primeiro via `GetSandboxForChannelViaBinding`, fallback FK.
- `GetIMChannelForSandbox(sandboxID)` — JOIN junction primeiro (`ORDER BY bound_at DESC LIMIT 1`), fallback JOIN via FK.

**Mudanças em `workspaces.go`:**

- `Workspace` struct ganha `ChannelRoutingStrategy string`.
- Todos os SELECTs (`GetWorkspace`, `ListWorkspacesByUser`, `ListAllWorkspaces`, `ListWorkspacesWithoutNamespace`, `ListAllWorkspacesAdmin`) incluem `COALESCE(channel_routing_strategy, 'shared')`.
- Novo `UpdateWorkspaceRoutingStrategy(id, strategy)` — valida contra `ValidRoutingStrategies` map.

### 3. HTTP

**Em `internal/imbridgesvc/handlers.go`:**

| Rota | Handler | Body | Resposta |
|---|---|---|---|
| `GET /api/workspaces/{id}/routing-strategy` | `handleGetWorkspaceRoutingStrategy` | — | `{"strategy":"shared"}` |
| `PUT /api/workspaces/{id}/routing-strategy` | `handleUpdateWorkspaceRoutingStrategy` | `{"strategy":"per_agent"}` | `{"strategy":"per_agent"}` |
| `POST /api/sandboxes/{id}/im/bind-multi` | `handleBindSandboxChannelsMulti` | `{"channel_ids":["ch1","ch2"]}` | `{"status":"bound","channel_ids":[...]}` |

`bind-multi` valida que todos os channels pertencem ao mesmo workspace do sandbox antes de inserir; mistura de workspaces retorna `400`.

**Em `internal/imbridgesvc/server.go`:** 3 rotas registradas no grupo autenticado (cookie auth via `s.auth.Middleware`).

**Em `internal/server/server.go`:** 3 rotas proxiadas via `s.imBridgeProxy` quando `IMBridgeURL != ""` (deploy com imbridge standalone).

## Fluxos

### Modo `shared` (default SMB)

```
PUT /api/workspaces/W1/routing-strategy {"strategy":"shared"}
POST /api/workspaces/W1/im/weixin/qr-start → channel ch1
POST /api/workspaces/W1/im/telegram/configure → channel ch2
# (workspace ainda não tem sandbox; criar via UI ou API)
POST /api/workspaces/W1/sandboxes → sbx-A (running)
POST /api/sandboxes/sbx-A/im/bind-multi {"channel_ids":["ch1","ch2"]}

# Resultado:
junction = { (sbx-A, ch1), (sbx-A, ch2) }
sandboxes.im_channel_id = NULL para sbx-A  ← bind-multi NÃO toca FK

# Mensagem entra em ch1:
imbridge.bridge.go::forwardMessage(ch1)
  → db.GetSandboxForChannel("ch1")
  → GetSandboxForChannelViaBinding("ch1") → sbx-A (running)
  → POST http://<sbx-A.pod_ip>/api/im/inbound
```

### Modo `per_agent` (enterprise)

```
PUT /api/workspaces/W2/routing-strategy {"strategy":"per_agent"}

# Cliente provisiona 1 sandbox por canal manualmente (auto-provisioner
# é PR 2). Bind 1:1 usa o endpoint legado:
POST /api/sandboxes/sbx-B/im/bind {"channel_id":"ch3"}
POST /api/sandboxes/sbx-C/im/bind {"channel_id":"ch4"}

# Como BindSandboxToChannel é 1:1 (desloca qualquer outro sandbox do channel),
# qualquer rebind futuro é seguro.
```

### Modo `hybrid`

```
PUT /api/workspaces/W3/routing-strategy {"strategy":"hybrid"}

# Operador escolhe na UI (PR 3) ou via API qual sandbox recebe quais canais:
POST /api/sandboxes/sbx-D/im/bind-multi {"channel_ids":["ch5","ch6"]}  # vendas + suporte
POST /api/sandboxes/sbx-E/im/bind        {"channel_id":"ch7"}           # cobrança isolada
```

## Decisões de design

| # | Decisão | Razão |
|---|---|---|
| 1 | Junction `(sandbox_id, channel_id)` em vez de array em `sandboxes` | Postgres-native cascade, índices, sem parsing JSONB |
| 2 | Strategy em `workspaces`, não em `sandboxes` | Decisão por tenant; um workspace não vai ter 2 estratégias |
| 3 | Default `shared` | Maior alavanca de custo no MVP B2B; clientes enterprise mudam explicitamente |
| 4 | FK legado preservado | Rollback safety, dual-write não tem custo perceptível |
| 5 | Junction-first read com FK fallback | Migra clientes sem janela de downtime |
| 6 | `BindSandboxToChannel` mantém semântica 1:1 | Não quebra os 6 call-sites existentes (handlers de qr-start, telegram-configure, etc.) |
| 7 | Novo `BindSandboxChannels` (plural) para N:1 | API clara: 1:1 = `BindSandboxToChannel`, N:1 = `BindSandboxChannels` |
| 8 | Tie-break por `bound_at DESC` em `GetSandboxForChannelViaBinding` | Em rollouts mid-flight onde 2 sandboxes apareçam bound ao mesmo channel, o mais recente vence |

## Compat / rollback

- **Backward**: clientes pre-PR continuam usando `BindSandboxToChannel` 1:1; dual-write garante junction populada sem que eles saibam.
- **Forward**: cliente pode ignorar `routing-strategy` e tudo segue 1:1. Strategy só vira efetiva quando o auto-provisioner do PR 2 entra.
- **Rollback de imagem**: revert da binary volta a ler só FK; junction torna-se órfã mas íntegra. Próximo deploy lê de novo.
- **Rollback de migration**: drop da junction + drop da coluna é seguro porque toda lógica nova faz fallback ao FK.

## Smoke / verificação

### SQL

```sql
-- 1. Schema aplicado
SELECT column_name FROM information_schema.columns
 WHERE table_name='workspaces' AND column_name='channel_routing_strategy';
-- → channel_routing_strategy

SELECT count(*) FROM pg_tables WHERE tablename='sandbox_channel_bindings';
-- → 1

-- 2. Backfill consistente
SELECT
  (SELECT count(*) FROM sandboxes WHERE im_channel_id IS NOT NULL) AS legacy,
  (SELECT count(*) FROM sandbox_channel_bindings)                   AS junction;
-- legacy = junction logo após migration

-- 3. Dual-write em sync (após exercitar Bind)
SELECT s.id, s.im_channel_id, b.channel_id
FROM sandboxes s
LEFT JOIN sandbox_channel_bindings b ON b.sandbox_id = s.id
WHERE s.im_channel_id IS NOT NULL AND b.channel_id IS NULL;
-- → 0 linhas
```

### HTTP

```bash
TOKEN="<cookie>"
BASE="https://agentserver.analytics.vtex.com"

# Get default strategy
curl -b "$TOKEN" $BASE/api/workspaces/<wid>/routing-strategy
# {"strategy":"shared"}

# Switch
curl -b "$TOKEN" -X PUT -d '{"strategy":"per_agent"}' \
  $BASE/api/workspaces/<wid>/routing-strategy

# Bind 2 channels to 1 sandbox
curl -b "$TOKEN" -X POST -d '{"channel_ids":["ch1","ch2"]}' \
  $BASE/api/sandboxes/<sbxid>/im/bind-multi
```

### Dev EKS

```bash
CTX="arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform"

# Migration aplicada?
kubectl --context "$CTX" exec deploy/agentserver-postgresql -n agentserver -- \
  psql -U agentserver -d agentserver -c "\d sandbox_channel_bindings"

# Logs da migration
kubectl --context "$CTX" logs deploy/agentserver -n agentserver | grep -i "031_multi_channel"
```

## Auto-bind (PR #4)

Endpoint que provisiona/reusa sandbox e o vincula ao canal automaticamente, baseado em `channel_routing_strategy` do workspace.

```
POST /api/workspaces/{id}/im/channels/{channelId}/auto-bind
Body: {"sandbox_type":"openclaw","name":"vendas"}  # ambos opcionais
```

| Strategy | Comportamento |
|---|---|
| `shared` | Reusa via `GetSharedSandbox` se existir running. Senão provisiona novo + bind via `BindSandboxChannels` (N:1, sem deslocar) |
| `per_agent` | Sempre provisiona novo sandbox + `BindSandboxToChannel` (1:1, desloca outros) |
| `hybrid` | Retorna `409` — operador deve usar `/im/bind` ou `/im/bind-multi` manualmente |

Resposta:
```json
{
  "sandbox_id": "uuid",
  "channel_id": "uuid",
  "strategy": "shared",
  "reused": false
}
```

`reused=true` indica que um sandbox existente foi reutilizado; `false` indica provisionamento novo (status inicial `creating`, pod IP populado async).

### Implementação

Refatora `handleCreateSandbox` extraindo o core para `provisionSandbox(ctx, wsID, in provisionInput) (*sbxstore.Sandbox, error)`. Ambos handlers (criação manual + auto-bind) compartilham:

- Validação de quota + budget
- Resolução de defaults do workspace
- Validação de tipo + CPU + memory + idle_timeout
- Geração de tokens por tipo (opencode/openclaw/nanoclaw bridge secret)
- `s.Sandboxes.Create` + retry de short-id
- Goroutine async `StartContainerWithIP`

Erros viram `*provisionError` (struct tipada com `Code`, `Status`, `Message`, `Detail`) que mapeiam direto a JSON HTTP via `writeProvisionError`.

### Fluxo recomendado (frontend)

```
1. Workspace strategy já configurada (shared|per_agent|hybrid)
2. POST /api/workspaces/W/im/telegram/configure → channel ch1
3. POST /api/workspaces/W/im/channels/ch1/auto-bind  → sandbox provisioned/bound
4. Polling GET /api/sandboxes/{sandbox_id} até status="running"
5. Mensagens entrantes no canal já são roteadas pelo imbridge
```

### Race window

Há janela entre criação do canal (passo 2) e auto-bind (passo 3) onde mensagens podem chegar sem sandbox bound. Comportamento: imbridge loga "no sandbox" e re-tenta o próximo poll. Para fluxo zero-race, próximo PR integra auto-bind dentro dos handlers de configure/qr-complete.

---

## Roadmap

| PR | Escopo | Status |
|---|---|---|
| #3 | Schema N:M + dual-write + read fallback + API de strategy | ✅ merged |
| **#4 (este)** | Auto-bind handler + extração de `provisionSandbox` | ✅ this PR |
| #5 | Frontend UI — dropdown de strategy no modal de criar workspace + drag-and-drop hybrid binder | pending |
| #6 | Provider WhatsApp (Z-API / Evolution API) integrado ao imbridge como novo `workspace_im_channels.provider` | pending |
| #7 | Cleanup — drop da FK `sandboxes.im_channel_id` depois de N semanas em produção com junction estável | future |

## Arquivos tocados

```
internal/db/migrations/031_multi_channel_routing.sql  [new]
internal/db/sandbox_channel_bindings.go               [new]
internal/db/im_channels.go                            [modified]
internal/db/workspaces.go                             [modified]
internal/imbridgesvc/handlers.go                      [modified]
internal/imbridgesvc/server.go                        [modified]
internal/server/server.go                             [modified]
```

## Referências

- PR: https://github.com/CarlosSalvador-vtex/agentserver/pull/3
- Plano: `/Users/carlos.neto/.claude/plans/peaceful-hatching-beacon.md`
- Migration anterior (workspace IM channels 1:1): `internal/db/migrations/011_workspace_im_channels.sql`
- Migration de routing mode (`nanoclaw`/`codex`): `internal/db/migrations/018_im_routing_mode.sql`
