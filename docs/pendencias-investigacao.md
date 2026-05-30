# Pendências — Investigação e Features

> Capturado em sessão 2026-05-30. Itens para investigar e implementar em momento oportuno.
> Cada item tem contexto técnico inicial para facilitar retomada.

---

## P1 — Idle timeout: o sandbox está parando ou sendo pausado?

**Pergunta:** após o tempo de inatividade configurado, o sandbox é **parado/deletado** (pod removido) ou **pausado** (pod suspenso, retomável)?

**Contexto técnico levantado:**
- `internal/sbxstore/idlewatcher.go`: goroutine roda a cada 1 minuto, chama `ListIdleSandboxes` + `procMgr.Pause(sbx.ID)`.
- Fluxo: status `running` → `pausing` → (chama `Pause`) → `paused` + limpa `pod_ip`.
- O que `procMgr.Pause()` faz concretamente (stop do pod K8s? freeze? delete CRD?) **ainda não investigado**.
- `cmd/serve.go:327`: idle watcher inicializado com timeout dinâmico via `srv.GetEffectiveIdleTimeout()`.
- Configurável por workspace (campo `max_idle_timeout` em `workspace_defaults`).
- Dev: UI mostra "Max Idle: 30 min" no overview do workspace.

**O que investigar:**
1. `internal/sandbox/manager.go` → implementação de `Pause()` — stop/delete pod ou freeze?
2. Se `paused`: como reativar? Há um `Resume()` e quando é chamado automaticamente (próxima mensagem do usuário)?
3. Estado persiste no DB (`sandboxes.status`)? Sessão/memória do OpenClaw sobrevive?

---

## P2 — Skills com endpoints reais: integração com dados externos

**Ideia:** as skills atuais (cobrança, vendas) usam dados fictícios/hardcoded no prompt. Melhorar integrando com endpoints externos reais.

**Exemplos concretos:**
- Skill de cobrança: endpoint que busca valor da dívida pelo CPF (ex.: `GET /api/mock/dividas?cpf=XXX` → `{ valor: 1500.00, vencimento: "2026-02-10", credor: "Acme" }`).
- Skill de vendas: endpoint de catálogo de produtos com preços reais.
- Skill de SAC: endpoint de status de pedido por número.

**Como skills podem chamar endpoints hoje:**
- Via `index.mjs` (plugin/agentic tier): `registerTool` expõe uma função Go ou HTTP call.
- Via tools nativas do OpenClaw (`web_fetch`, `web_search`) se habilitadas.
- Via skill prompt que instrui o agente a usar tools já registradas.

**O que investigar/implementar:**
1. Criar mock endpoints em agentserver (ex.: `GET /api/mock/cobranca/{cpf}`) que retornam dados fictícios mas estruturados — remove hardcoding do prompt.
2. Documentar padrão de `registerTool` no `index.mjs` para chamar esses endpoints com auth.
3. Avaliar se a tool `web_fetch` do OpenClaw é suficiente ou se precisa de SDK customizado.
4. Sugestão de integração real: webhook/API do tenant registrado no agentserver que a skill chama — multi-tenant, cada tenant configura sua URL de dados.

---

## P3 — Persistência de sessão/memória quando sandbox é pausado/morto

**Pergunta:** quando um sandbox é pausado por inatividade (ou morto por qualquer razão), o estado da conversa (memória, histórico de sessão) é salvo no banco de dados para ser reutilizado na próxima vez?

**Contexto técnico:**
- OpenClaw salva estado em `/home/agent/.openclaw/workspace/` (PVC — `session-data` PVC).
- O PVC é `ReadWriteOnce` e **persiste** entre reinícios de pod (não é efêmero).
- `AGENTS.md`, `USER.md`, `HEARTBEAT.md` etc. são lidos a cada turn pelo OpenClaw.
- Sessão codex: armazenada em `agent_sessions.codex_thread_id` no DB.
- Sessão OpenClaw (turn via `openclaw agent --session-id`): estado local no PVC.

**O que investigar:**
1. O PVC sobrevive a um `Pause` + eventual `Resume`? (Provavelmente sim — é um PVC K8s separado do pod.)
2. O PVC sobrevive se o CRD for deletado (reaper, timeout longo, admin delete)?
3. Quando sandbox é recriado (após delete), o mesmo PVC é reutilizado (mesmo `session-data` PV)?
4. Para sessões IM (canal bound): `--session-id` derivado de `(channelID, fromUserID)` → persiste entre runs do `openclaw agent` dentro do mesmo pod. Mas se pod é deletado, memória é perdida (está no PVC mas o index do OpenClaw pode não reindexar automaticamente).

---

## P4 — Viabilidade de salvar conversas no banco de dados

**Ideia:** persistir as conversas (turns de cada usuário com cada bot) no DB para auditoria, analytics, replay e conformidade.

**Contexto atual:**
- `draft_audit_events` existe para ações do playground (não para conversas IM).
- Turns IM: processados em `processTurn` / `runTurnSync` → reply enviado → **nada persiste no DB**.
- OpenClaw guarda histórico localmente no PVC (arquivos `workspace/`), mas não em DB relacional.
- `agent_sessions` no DB guarda só `session_id` + `codex_thread_id` — não o conteúdo das mensagens.

**O que avaliar:**
1. Criar tabela `im_messages` (inbound + outbound, por canal, por usuário, com timestamp).
2. Gravar em `runTurnSync` (inbound text + agent reply) antes de entregar.
3. LGPD: conversas podem conter dados pessoais → política de retenção necessária (TTL, purge por workspace, anonimização).
4. Volume: cada turn = 2 rows. Com N bots × M usuários ativos → estimar volume antes de implementar.
5. Alternativa mais leve: logar só metadados (timestamp, channelID, sessionID, token_count) sem o conteúdo — compliance sem armazenar PII.

---

## P5 — Sandbox: paused vs stopped — como reativar?

**Pergunta complementar ao P1:** se o sandbox é **pausado** (não deletado), como ele é reativado quando o usuário manda uma nova mensagem? É automático?

**Contexto técnico:**
- `idlewatcher.go` → pausa via `procMgr.Pause()` → status = `paused`.
- `internal/sandbox/manager.go` tem `Resume(id, sandboxName, command, args)` (via `process.Manager` interface).
- A lógica de "reativar ao receber mensagem" deveria estar em `forwardToOpenclaw` / `handleOpenclawTurn`: se sandbox está `paused`, chama `Resume` antes de `ExecSimple`.
- **Não confirmado** se isso existe hoje — pode ser que mensagens cheguem com sandbox `paused` e falhem silenciosamente.

**O que investigar:**
1. `handleOpenclawTurn` (`internal/server/openclaw_turn_handler.go`): verifica status do sandbox antes de ExecSimple? Se `paused`, chama resume?
2. `GetSandboxForChannel` retorna sandbox paused ou só running?
3. Se não há auto-resume: é um bug — mensagem chega, sandbox paused, turn falha, usuário não recebe resposta.

---

## P6 — Capacidade: quantas conversas paralelas um único sandbox WhatsApp pode servir?

**Pergunta:** um sandbox OpenClaw consegue atender múltiplos usuários do WhatsApp em paralelo, ou é 1 usuário por vez?

**Contexto técnico:**
- `openclaw agent --session-id <id>` roda um turn síncrono (bloqueia até LLM responder, ~2-5s).
- `ExecSimple` é chamado por `handleOpenclawTurn` que é chamado pelo poller do imbridge.
- O imbridge poll loop é sequencial por canal (1 msg de cada vez por canal).
- Se 2 usuários mandam msg ao mesmo tempo para o mesmo bot WhatsApp → 2 calls a `handleOpenclawTurn` sequencialmente? Ou concorrentemente?
- `agent_sessions` garante isolamento de memória por `(channelID, userID)`, mas o pod processa um turn por vez.

**O que investigar:**
1. O poll loop do imbridge é sequencial ou paralelo por mensagem?
2. `handleOpenclawTurn` tem concorrência limitada (mutex? semáforo)?
3. Se simultâneo: `openclaw agent` permite 2 instâncias concorrentes no mesmo pod (locking de arquivo? conflito de sessão)?
4. Para N usuários simultâneos: precisa de N sandboxes (1 por usuário) ou N pods de OpenClaw?
5. WhatsApp Business API: rate limits por número (250 conv/24h tier padrão; 1000 com qualidade alta). Quantos usuários simultâneos o pod suporta antes de a latência degradar?

---

## Resumo / Priorização sugerida

| # | Título | Tipo | Prioridade sugerida |
|---|--------|------|---------------------|
| P1 | Idle timeout: stop ou pause? | Investigação | Alta (afeta custos e UX) |
| P2 | Skills com endpoints reais | Feature | Alta (MVP sim multi-bot) |
| P3 | Persistência sessão após pause/kill | Investigação | Média |
| P4 | Salvar conversas no DB | Feature/decisão | Média (LGPD gates) |
| P5 | Auto-resume de sandbox pausado | Bug/investigação | Alta (potencial bug silencioso) |
| P6 | Capacidade paralela por sandbox | Investigação | Média (escala do sim) |
