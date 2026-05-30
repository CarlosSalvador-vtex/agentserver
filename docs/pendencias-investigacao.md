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

**O que investigar:**
1. `Pause()` → deleta o pod mas preserva o PVC? Ou deleta ambos?
2. `Resume()` → monta o mesmo PVC no novo pod?
3. Há algum caso onde o PVC é deletado (reaper, admin, TTL)?
4. Testar: pausar sandbox manualmente → re-resume → checar se `AGENTS.md` ainda está.

---

### A4 — Capacidade paralela: quantas conversas simultâneas por sandbox?

**Pergunta:** um único sandbox OpenClaw consegue atender múltiplos usuários em paralelo
(ex.: WhatsApp com vários clientes ao mesmo tempo)?

**Contexto técnico:**
- `openclaw agent --session-id X --message Y` é síncrono (~2-5s por turn).
- `handleOpenclawTurn` no agentserver é chamado pelo imbridge para cada msg inbound.
- O poll loop do imbridge (`bridge.go/pollLoop`) é **sequencial por canal** — mas se
  múltiplos usuários mandam msg ao mesmo tempo, o agentserver pode receber requests
  concorrentes (HTTP server é concorrente).
- `ExecSimple` abre um `kubectl exec` no pod. 2 calls concorrentes ao mesmo pod são
  tecnicamente possíveis, mas OpenClaw pode ter locking de arquivo de sessão.
- WhatsApp Business API (tier padrão): 250 conversas/24h; tier alto: 1000+.

**O que investigar:**
1. `ExecSimple` concorrente no mesmo pod — OpenClaw suporta?
2. Há mutex ou semáforo em `handleOpenclawTurn` por sandbox?
3. Se 1 turn por vez: N usuários simultâneos = N * latência média de espera. Aceitável?
4. Escala horizontal: precisa de 1 sandbox por usuário ativo ou por canal?

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

## Tabela Resumo

| ID | Título | Grupo | Prioridade |
|----|--------|-------|------------|
| A1 | Idle timeout: para ou pausa? | Sandbox | ✅ Resolvido (PR #163) |
| A2 | Auto-resume após pause (bug silencioso) | Sandbox | ✅ Resolvido (PR #163) |
| A3 | Persistência de memória após pause/kill | Sandbox | Pendente |
| A4 | Capacidade paralela por sandbox | Sandbox | Pendente |
| B1 | Skills com endpoints reais | Integração | Pendente |
| C1 | Salvar conversas no DB (+ LGPD) | Dados | Pendente |

**Ordem recomendada de investigação:**
A1 → A2 (depende de A1) → A3 (depende de A1) → A4 (independente) → B1 → C1
