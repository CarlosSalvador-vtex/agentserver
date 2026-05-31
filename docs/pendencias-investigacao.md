# Pendências — Investigação e Features

> Capturado em sessão 2026-05-30. Reagrupado 2026-05-30.
> A1+A2 investigados e corrigidos em 2026-05-30 (PR #163).
> Contexto técnico incluído em cada item para facilitar retomada.

---

## Grupo A — Comportamento do Sandbox (ciclo de vida, memória, capacidade)

Todos os itens abaixo são interdependentes: entender pause vs stop (A1) desbloqueia
A2 (auto-resume) e A3 (persistência). Capacidade paralela (A4) é independente mas usa
o mesmo contexto de como o pod funciona.

### A1 — Idle timeout: o sandbox para ou é pausado? ✅ RESOLVIDO

**Resposta (investigado 2026-05-30, PR #163):** **PAUSADO**, não deletado.

`Pause()` em `internal/sandbox/manager.go` faz `kubectl patch sandbox spec.replicas=0`.
O pod é removido pelo controller, mas **o PVC `session-data` sobrevive** (comentário no
código confirma: "Pod goes away, PVC stays"). Status no DB: `running` → `pausing` → `paused`,
`pod_ip` limpo. `ResumeContainerWithIP()` faz o inverso: `replicas=1`, aguarda pod ready,
retorna novo IP.

---

### A2 — Auto-resume: sandbox pausado é reativado ao receber nova mensagem? ✅ RESOLVIDO

**Bug confirmado e corrigido (PR #163).**

`GetSandboxForChannelViaBinding` filtrava `status='running' AND pod_ip!=''` → sandbox
`paused` retornava `ErrNoRows` → `handleOpenclawTurn` retornava 404 → imbridge logava
erro → **bot não respondia ao usuário (bug silencioso confirmado)**.

**Fix aplicado:**
- Novo `db.GetPausedSandboxForChannel(channelID)` — busca sandbox `paused` vinculado ao canal.
- `handleOpenclawTurn`: se running não encontrado → checa paused → chama
  `SandboxExecerIface.ResumeContainerWithIP()` (timeout 90s) → atualiza `pod_ip` + status
  `running` → prossegue com `ExecSimple`. Bot responde normalmente; usuário vê apenas
  uma pequena latência extra (~15-30s na primeira msg após longa inatividade).

---

### A3 — Persistência de sessão/memória após pause ou kill

**Pergunta:** após o sandbox ser pausado ou morto, a memória de conversas anteriores
é preservada para quando o usuário voltar?

**Contexto técnico:**
- OpenClaw salva estado em `/home/agent/.openclaw/workspace/` **no PVC** (`session-data`).
  Arquivos: `AGENTS.md`, `USER.md`, `HEARTBEAT.md`, histórico da sessão.
- O PVC é `ReadWriteOnce` e existe independente do pod (K8s PersistentVolume).
- **Se o pod é pausado/deletado mas o PVC sobrevive**: na próxima vez que o pod sobe
  com o mesmo PVC, o OpenClaw relê os arquivos → memória preservada.
- **Se o PVC é deletado** (delete do CRD + claim): memória perdida.
- Sessão IM (`--session-id` derivado de `channelID + userID`): estado em memória do
  pod durante o turn. Se pod reinicia, a próxima call `openclaw agent --session-id X`
  lê do PVC — memória persiste se PVC ok.

**Resposta (derivada dos achados de A1, 2026-05-30):** ✅ CONFIRMADO SEM INVESTIGAÇÃO ADICIONAL.

Pause = `replicas:0` → pod vai, PVC fica. Resume = `replicas:1` → mesmo PVC montado no novo
pod. OpenClaw relê `AGENTS.md`, `USER.md`, histórico de sessão do PVC automaticamente.
Memória da conversa é preservada entre pause/resume.

**Caveat a monitorar:** se o sandbox for **deletado** (não pausado) — ex.: reaper do
playground, admin delete, TTL longo — o CRD `AgentSandbox` pode ser removido junto com
o PVC claim. Verificar se o StorageClass tem `reclaimPolicy: Retain` ou `Delete`.
Se `Delete` (padrão em muitos clusters), memória perdida ao deletar CRD.

---

### A4 — Capacidade paralela: quantas conversas simultâneas por sandbox? ✅ RESOLVIDO

**Resposta (investigado 2026-05-30):**

**Poll loop do imbridge:** sequencial por canal (`bridge.go/pollLoop` processa msgs em fila
dentro do `for _, msg := range result.Messages` — `forwardMessage` é síncrono). Msgs do
mesmo canal chegam 1 por vez ao agentserver.

**handleOpenclawTurn:** sem mutex — 2 canais diferentes bound ao mesmo sandbox → 2 calls
concorrentes → 2 `ExecSimple` no mesmo pod simultâneos são possíveis.

**OpenClaw isolamento por session-id:** `--session-id` derivado de `(channelID, fromUserID)`
isola a memória por conversa. Calls concorrentes com sessions diferentes rodam sem conflito
(cada session tem seu próprio path de estado no PVC).

**Modelo de escala:**
- 1 canal WhatsApp = 1 fila de msgs = 1 turn por vez para aquele número.
- N usuários no mesmo número: fila de N turns (~2-5s cada). Para até ~10 usuários simultâneos
  o throughput é aceitável (50s de espera no pior caso, raramente atingido).
- Para alta concorrência: 1 sandbox por usuário ativo (ou 1 sandbox por tenant com múltiplos
  canais) — arquitetura de escala horizontal, não investigada ainda.
- WhatsApp Business API (tier padrão): 250 conversas/24h; tier alto: 1000+. O gargalo é
  o LLM (~2-5s/turn), não o sandbox.

---

## Grupo B — Skills e Integração com Dados Externos

### B1 — Skills com endpoints reais (dados fictícios → dados reais)

**Ideia:** as skills atuais (cobrança, vendas, SAC) usam dados hardcoded no prompt.
Melhorar integrando com endpoints que retornam dados reais (ou mock estruturado).

**Exemplos concretos:**
- Cobrança: `GET /api/mock/dividas?cpf=321` → `{ valor: 1500.00, vencimento: "2026-02-10" }`
- Vendas: `GET /api/mock/produtos?categoria=notebook` → catálogo com preços
- SAC: `GET /api/mock/pedidos/{numero}` → status do pedido

**Como skills chamam endpoints hoje:**
- Via `index.mjs` (plugin/agentic tier): `registerTool` registra uma função que faz
  HTTP call. O agente chama a tool e usa o retorno.
- Via tool nativa `web_fetch` do OpenClaw (se habilitada no `openclaw.json`).
- Via prompt que instrui o agente a usar tools já registradas.

**O que implementar:**
1. Criar mock endpoints em agentserver: `GET /api/sim/cobranca/{cpf}`, `/api/sim/produtos`,
   `/api/sim/pedidos/{id}` — retornam dados fictícios estruturados (remove hardcoding do prompt).
2. Documentar padrão de `registerTool` no `index.mjs` para chamar esses endpoints com auth.
3. **Padrão multi-tenant de integração:** tenant registra URL da sua API no agentserver
   (novo campo em `workspace_settings`). Skill faz call para essa URL com token do tenant.
   Cada tenant conecta seus próprios sistemas (CRM, ERP, etc.) sem fork da skill.

---

## Grupo C — Dados e Conformidade

### C1 — Viabilidade de salvar conversas no banco de dados

**Ideia:** persistir turns IM (inbound + outbound) no DB para auditoria, analytics e
conformidade.

**Contexto atual:**
- Turns processados em `runTurnSync` → reply enviado → **nada persiste no DB**.
- `agent_sessions` guarda só `session_id` + `codex_thread_id`, não o conteúdo.
- OpenClaw guarda histórico localmente no PVC, não em DB relacional.

**O que avaliar:**
1. Criar tabela `im_messages` (channelID, fromUserID, direction, text, timestamp).
2. Gravar em `runTurnSync` antes/depois de entregar (inbound + reply).
3. **LGPD gate:** conversas podem conter CPF, nome, valores de dívida → política de
   retenção obrigatória (TTL, purge por workspace, anonimização automática).
4. Estimativa de volume antes de implementar (N bots × M usuários × K turns/dia).
5. Alternativa leve: logar só metadados (timestamp, channelID, sessionID, token_count)
   sem conteúdo — compliance sem armazenar PII.

---

## Grupo D — Bot Proativo (bot inicia contato com usuário)

### D1 — Como configurar um bot para entrar em contato ativamente ✅ INVESTIGADO

**Investigado 2026-05-31 via subagent.**

**Resposta:** o sistema já tem o mecanismo pronto — **Automations** (PR1+PR2 deployed em dev).
Bot proativo = Automation com cron schedule que dispara um turn no sandbox e entrega via imbridge.

---

#### Como fazer (passo a passo)

**Pré-requisitos:**
- Canal IM configurado no workspace (Telegram, WhatsApp, WeChat, Matrix)
- Para Telegram: o usuário DEVE ter mandado ao menos uma msg ao bot primeiro (limitação da API do Telegram — veja abaixo)
- Para WhatsApp/WeChat: sem restrição de iniciativa

**Passos:**
1. Workspace → aba **Automations**
2. Clicar "New Automation" (ou usar template do catálogo)
3. Preencher:
   - **Name:** ex. "Cobrança diária"
   - **Skill:** skill de cobrança/vendas (draft ID)
   - **Cron:** ex. `0 9 * * 1-5` (9h manhã dias úteis) ou `@daily` ou `@every 1h`
   - **Channel:** canal IM vinculado ao bot
   - **Prompt:** instrução que o agente recebe (ex. "Contacte o cliente sobre a dívida em aberto")
4. Salvar → `next_run_at` é calculado automaticamente
5. Na hora configurada: scheduler dispara → skill roda no sandbox → reply enviado via imbridge ao usuário

**Verificação:** Automations tab mostra `last_run_at`, `last_error`, `next_run_at` por automation.

---

#### Limitação crítica — Telegram

**Telegram NÃO permite que bots iniciem conversa com usuários que nunca interagiram antes.**
Erro retornado pela API: `403 Forbidden: bot was blocked by the user`.

Workarounds:
- **Grupos:** bot pode mandar msg em grupos onde é membro (automation funciona direto)
- **Indivíduos:** usuário precisa enviar `/start` ao bot primeiro — depois automation funciona
- **Alternativa:** usar WhatsApp ou WeChat (sem restrição de iniciativa)

---

#### Endpoint de envio manual (interno)

```bash
POST /api/internal/imbridge/send
X-Internal-Secret: <INTERNAL_API_SECRET>
Content-Type: application/json

{
  "channel_id": "<uuid-do-canal>",
  "to_user_id": "<telegram-user-id>",
  "text": "Olá! Lembrete sobre sua dívida..."
}
```

Resposta: `{"status": "sent"}` ou `{"status": "blocked", "message": "..."}` (guardrail).

---

#### Arquivos relevantes

| Arquivo | Descrição |
|---|---|
| `internal/server/automation_scheduler.go` | Ticker 1min + fireAutomation() |
| `internal/server/automation_handlers.go` | CRUD HTTP handlers |
| `internal/db/automations.go` | DB ops + ComputeNextRun() |
| `internal/db/migrations/046_automations.sql` | Schema da tabela |
| `web/src/components/WorkspaceAutomationsTab.tsx` | UI React |
| `internal/imbridgesvc/handlers.go` | handleImbridgeDirectSend() |
| `docs/productized-automations-spec.md` | Spec completo |

---

#### O que ainda não existe

- Trigger por evento (só cron time-based por enquanto)
- Runs manuais via UI (só agendados)
- Multi-réplica segura (PR3 pendente) → usar `replicaCount: 1` no Helm

---

## Tabela Resumo

| ID | Título | Grupo | Prioridade |
|----|--------|-------|------------|
| A1 | Idle timeout: para ou pausa? | Sandbox | ✅ Resolvido (PR #163) |
| A2 | Auto-resume após pause (bug silencioso) | Sandbox | ✅ Resolvido (PR #163) |
| A3 | Persistência de memória após pause/kill | Sandbox | ✅ Confirmado (PVC sobrevive, ver A1) |
| A4 | Capacidade paralela por sandbox | Sandbox | ✅ Resolvido (fila por canal, session-id isola) |
| B1 | Skills com endpoints reais | Integração | Pendente |
| C1 | Salvar conversas no DB (+ LGPD TTL) | Dados | Parcial (C1 impl PR #170; TTL pendente) |
| D1 | Bot proativo — como configurar | Automations | ✅ Investigado (usar Automations tab) |

**Ordem recomendada de investigação:**
A1 → A2 (depende de A1) → A3 (depende de A1) → A4 (independente) → B1 → C1
