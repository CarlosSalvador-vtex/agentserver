# Prior Art — Skill Creation Playgrounds & Autopilot Systems

Pesquisa realizada em 2026-05-27 para embasar o design do fluxo **Publish** do OpenClaw playground (substituição do "Promote → PR" por um botão acessível a não-técnicos).

---

## 1. Agenta — LLMOps Prompt Playground

**Repo:** https://github.com/Agenta-AI/agenta  
**Stack:** Python + React · Self-hostable · Apache 2.0  
**Stars:** ~8k

### O que faz
Plataforma LLMOps open source com playground interativo para prompt engineering. Foco em comparação side-by-side de prompts, gerenciamento de versões, eval sistemática e observabilidade em produção.

### Relevância para o OpenClaw
- UI de edição/teste de prompts é bastante similar ao nosso playground de skills
- Suporta 50+ modelos via configuração; configSchema por variante de prompt
- Tem conceito de "environments" (staging → production) que mapeia para nosso fluxo draft → published
- **Diferença-chave:** centrado em prompts/variáveis, não em arquivos de skill estruturados

### Referência direta
O fluxo de "publicar para environment" do Agenta é um bom modelo para o Publish button: marcar uma versão como `live` sem precisar de Git.

---

## 2. Mastra Studio — TypeScript Agent Playground

**Repo:** https://github.com/mastra-ai/mastra  
**Site:** https://mastra.ai  
**Stack:** TypeScript/Node · YC S25 · Apache 2.0 · $13M seed  
**Stars:** ~18k

### O que faz
Framework TypeScript para agentes com um **Mastra Studio** local (`localhost:4111`): UI visual para criar, testar e depurar agents e workflows. Não-técnicos podem editar prompts e criar datasets diretamente na UI sem código.

### Relevância para o OpenClaw
- Modelo de UX mais próximo do nosso playground: editor visual + test runner na mesma tela
- Studio separa "build" (dev) de "refine" (PM/designer) — exatamente o que queremos com Publish
- Tem conceito de "trace" por execução, análogo ao nosso dry-run
- **Diferença-chave:** skills são objetos TypeScript compilados, não arquivos SKILL.md/index.mjs

### Referência direta
Estrutura do Studio (sidebar com lista de agents, editor central, painel de trace) é referência de layout para evoluir o nosso playground.

---

## 3. AutoAgent — Zero-Code Agent Creation via Linguagem Natural

**Repo:** https://github.com/HKUDS/AutoAgent  
**Paper:** https://arxiv.org/abs/2502.05957  
**Stack:** Python · MIT

### O que faz
Framework totalmente automatizado: cria agents, ferramentas e workflows a partir de linguagem natural pura, sem código. 4 componentes: engine de ações LLM, filesystem auto-gerenciado, módulo self-play para customização.

### Relevância para o OpenClaw
- Autopilot mais próximo do que foi pesquisado: o usuário descreve o comportamento, o sistema gera o skill
- Módulo self-play faz o agent refinar sua própria definição iterativamente
- **Diferença-chave:** sem UI; resultado não é um arquivo versionado/distribuível; focado em autonomia total, não em edição colaborativa

### Referência direta
O loop "descrever → gerar → testar → refinar" do AutoAgent é inspiração para um futuro "Create from description" no playground.

---

## 4. GenericAgent — Skill Tree Auto-Evolutivo

**Repo:** https://github.com/lsdefine/GenericAgent  
**Paper:** https://huggingface.co/papers/2604.17091  
**Stack:** Python · minimal (~3K linhas core)

### O que faz
Agent minimalista que **cresce uma skill tree automaticamente**: cada tarefa resolvida cristaliza o caminho de execução como skill reutilizável para uso futuro. Opera com < 30K tokens de contexto via memória em camadas.

### Relevância para o OpenClaw
- Modelo de skill como "trajetória cristalizada" é alternativo ao nosso modelo de arquivo estático
- Skill library cresce organicamente com o uso — sem curadoria manual
- Suporta Claude/Gemini/Kimi/MiniMax
- **Diferença-chave:** skills são internas ao agent, não portáteis/editáveis pelo usuário; sem UI; sem marketplace

### Referência direta
Conceito de "cristalização de trajetória" é referência de longo prazo para auto-geração de skills a partir de sessões de uso no OpenClaw.

---

## 5. agent-skill-creator — CLI de Conversão para SKILL.md

**Repo:** https://github.com/FrancyJGLisboa/agent-skill-creator

### O que faz
CLI que converte qualquer input (docs, PDFs, links, código, descrição em texto) em `SKILL.md` validado, compatível com 14+ ferramentas: Claude Code, Cursor, Copilot, Windsurf, Codex, Gemini, Kiro.

### Relevância para o OpenClaw
- Proof-of-concept de que "qualquer coisa vira skill" é viável via LLM
- Abordagem multi-plataforma via transpilação do SKILL.md base
- **Diferença-chave:** CLI puro, sem UI; resultado é para ferramentas de coding, não para agents em produção

### Referência direta
Inspiração para um "Publish from description" no playground: usuário cola uma descrição, LLM gera o `index.mjs` inicial como ponto de partida.

---

## 6. VoltAgent / awesome-agent-skills — Curadoria de Skills

**Repo:** https://github.com/VoltAgent/awesome-agent-skills  
**Outros:** https://github.com/skillmatic-ai/awesome-agent-skills

### O que faz
Coleções de 1000+ skills prontas compatíveis com Claude Code, Codex, Gemini CLI, Cursor. Indexadas por categoria e compatibilidade de plataforma.

### Relevância para o OpenClaw
- Referência de taxonomia e metadados de skills para o marketplace
- Mostra quais categorias têm maior demanda na comunidade
- **Diferença-chave:** repositório estático (README/JSON), não uma plataforma dinâmica

---

## Comparativo rápido

| Projeto | UI | Skill como arquivo | Publish sem Git | Auto-geração | Open Source |
|---------|----|--------------------|-----------------|--------------|-------------|
| **Agenta** | ✅ Web | ❌ (prompts/config) | ✅ (environments) | ❌ | ✅ |
| **Mastra Studio** | ✅ Local | ❌ (TypeScript) | ✅ | ❌ | ✅ |
| **AutoAgent** | ❌ | ❌ | N/A | ✅ NL→agent | ✅ |
| **GenericAgent** | ❌ | ❌ (interno) | N/A | ✅ por uso | ✅ |
| **agent-skill-creator** | ❌ CLI | ✅ SKILL.md | N/A | ✅ NL→skill | ✅ |
| **OpenClaw playground** | ✅ Web | ✅ index.mjs | 🚧 (Publish pendente) | ❌ | — |

---

## Conclusão

Nenhum projeto cobre exatamente o nosso caso: **edição visual de skills como arquivos + publish sem Git + marketplace multi-tenant**. O mais próximo em UX é o **Mastra Studio**; o mais próximo em conceito de "publish sem PR" é o **Agenta** (environments). O OpenClaw playground, quando o Publish button for implementado, terá uma proposta única na combinação desses três eixos.

**Próximo passo:** implementar o Publish button (ver `feedback_promote_pr_ux.md` em memory) e o fix de routing do `App.tsx` (ver `project_routing_fix_deferred.md`).
