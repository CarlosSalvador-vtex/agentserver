---
title: "Voice Agent de Cobrança — Análise e Plano"
date: 2026-05-22
tags: [voice-agent, ai, cobranca, telephony, hermes, claude, vapi, twilio, tts, asr]
author: Carlos + Claude
---


# Voice Agent de Cobrança — Análise e Plano

PoC para construir um voice agent de cobrança BR-PT inspirado em 3 gravações reais ("Clara" da OperadoraSaude-X e "BancoY"). Documento cobre análise das gravações, viabilidade técnica com Claude e Hermes, stack recomendado, compliance e roadmap.

## Resumo

3 gravações analisadas:
1. **OperadoraSaude-X "Clara" → Cliente A** (6min, R$ 1080,30)
2. **OperadoraSaude-X "Clara" → Cliente B** (4min, R$ 2019,28)
3. **BancoY → Cliente C** (1.5min, R$ 5215,46)

Todas: outbound voice bots de cobrança financeira (BR-PT) com fluxo idêntico de 8 etapas. Persona "Clara" consistente entre as duas chamadas OperadoraSaude-X. Stack tecnicamente replicável com Claude API + Vapi/Twilio OU Hermes + skill telephony em ~30min de setup para PoC.

## Análise das Gravações

### Fluxo comum (8 etapas)

1. **Saudação + identificação** — "Olá, aqui é a Clara, assistente virtual do Salvador, parceira da OperadoraSaude-X"
2. **Verificação de identidade** — pede 3 primeiros dígitos do CPF, retenta em caso de erro
3. **Informe da dívida** — valor exato, período de referência (ex: "mensalidade abril a junho 2025")
4. **Negociação rígida** — só "à vista" ou prazo de 5 dias úteis, sem parcelamento/desconto fora do script
5. **Empatia roteirizada** — "Entendo perfeitamente, Cliente A" + reconhece objeções superficialmente sem ceder no script
6. **Confirmação verbal** — repete valor + data antes de fechar
7. **Boleto enviado via WhatsApp** — sender name "Salvador recuperação"
8. **Email com termos** (BancoY) — link com condições completas

### Comparativo das 3 chamadas

| # | Empresa | Persona | Valor | Plano | Resultado |
|---|---------|---------|-------|-------|-----------|
| 1 | OperadoraSaude-X | Clara (assistente virtual) | R$ 1080,30 | Boleto 22/mai (3 dias) após reunião do cliente | Acordo verbal |
| 2 | OperadoraSaude-X | Clara | R$ 2019,28 | Boleto vencimento amanhã, valor à vista | Cliente resistente, acordo parcial |
| 3 | BancoY | "Assistente virtual" | R$ 5215,46 | Opção 1: 10% desc à vista (R$ 4693,98). Opção 2: 10% entrada (R$ 521,55) | Acordo |

### Padrões observados

- **Persona consistente entre chamadas** — OperadoraSaude-X usa mesma voz "Clara" em ambas as gravações. Voz tipo ElevenLabs v3 ou Cartesia Sonic (premium tier).
- **VAD/turn-taking natural** — bot interrompe quando cliente termina, retoma quando cliente fala em cima.
- **Backend integration** — bot consulta CPF, busca valor real, gera boleto via API. NÃO improvisa números.
- **Conformidade parcial** — bot identifica-se como "assistente virtual" no início (compliance LGPD/CDC), mas não menciona "esta chamada está sendo gravada".
- **WhatsApp como canal de entrega** — bot envia boleto pelo WhatsApp Business com nome customizado.
- **Sender name customizado** — "Salvador recuperação" aparece no contato do WhatsApp, gerando senso de autoridade institucional.

## Viabilidade Técnica

### Claude (Anthropic)

| Componente | Status | Notas |
|------------|--------|-------|
| LLM brain | ✓ | Claude Opus 4.7 ou Sonnet 4.6 via API |
| Voice mode CLI | ✓ | `claude voice` |
| Outbound voice | ✓ via Twilio | Twilio ConversationRelay (parceria Anthropic oficial) |
| Inbound voice | ✓ via Twilio | Twilio Programmable Voice + webhook |
| ASR | ✓ | Deepgram/AssemblyAI/Whisper via Twilio Media Streams |
| TTS pt-BR | ✓ | ElevenLabs, Cartesia, OpenAI TTS |
| Tool calling | ✓ | Nativo Claude API |
| Memória entre chamadas | Externa | Implementar via DB próprio |
| Custo | $0.05-0.10/min voz + $0.003/1k tokens Claude | |

### Hermes (Nous Research)

| Componente | Status | Notas |
|------------|--------|-------|
| LLM brain | ✓ | Multi-provider (Bedrock/zai/Anthropic) |
| Voice mode CLI/TUI | ✓ | Nativo |
| Outbound voice | ✓ | Skill `productivity-telephony` (Twilio/Bland.ai/Vapi) |
| Inbound voice | ✗ nativo | Via MCP AgentCall ($0.40/min) |
| ASR/TTS | ✓ | Mesma stack Vapi |
| WhatsApp send (boleto) | ✓ nativo | Skill WhatsApp já existente |
| Email send (termos) | ✓ nativo | Skill email já existente |
| Tool calling | ✓ | Skills + scripts Python |
| Memória entre chamadas | ✓ | SQLite FTS nativo (Hermes Memory) |
| Custo | Igual + custo MCP AgentCall se inbound | |

### Veredito

**Ambos viáveis**. Hermes tem vantagem operacional porque já tem WhatsApp + email integrados como skills nativas — combina exatamente com fluxo das gravações ("envio boleto pelo WhatsApp" + "termos por email"). Claude tem vantagem em maturidade da integração Twilio (ConversationRelay).

## Stack Recomendado

### Para PoC rápido (1-2 horas setup)

```
Hermes Agent (existente)
  + Vapi (ASR + TTS + SIP gerenciados) — $0.05-0.10/min
  + Twilio número outbound
  + Hermes skill productivity-telephony (já existe)
  + Hermes skill WhatsApp (já existe, para envio do boleto)
```

Custo PoC: ~$5 USD para testar 50 chamadas curtas.

#### O que cada componente do PoC faz

**Vapi (~$3-5)** — Orquestrador da chamada. Cola tudo num pipeline em tempo real:
- **ASR** (audio → texto): transcreve o que o cliente fala (~200ms latência)
- **VAD** (voice activity detection): detecta quando cliente para de falar
- **Roteamento ao LLM**: manda transcript pro Claude/Hermes, pega resposta
- **TTS** (texto → audio): converte resposta em voz e toca pro cliente
- **Barge-in**: cliente pode interromper o bot
- **SIP/PSTN bridge**: conecta tudo ao número telefônico real

Sem Vapi seriam semanas de código colando Deepgram + Claude + ElevenLabs + Twilio SDK. Custo: $0.05-0.10/min. 50 chamadas × 2min = 100min × $0.05 = $5.

**Twilio número (~$1/mês)** — Identidade telefônica do bot:
- **Número físico** roteável na rede telefônica mundial (PSTN)
- **Caller ID** que aparece no celular do cliente quando bot liga
- **SIP trunk** que Vapi usa para conectar à rede pública
- **DID brasileiro** se quiser +55 (mais reconhecível, menos chance de cliente recusar)

Sem isso o bot não consegue discar. Vapi orquestra mas precisa do pipe pra PSTN. $1/mês US ou ~$3/mês BR + $0.013/min outbound.

**Alternativa**: usar número Vapi nativo (incluído em alguns planos), menos controle.

**ElevenLabs Starter (~$5/mês, opcional)** — Voz humana premium pt-BR.

A "Clara" das gravações usa TTS state-of-the-art. Comparativo:

| TTS | Qualidade pt-BR | Latência | Custo |
|-----|-----------------|----------|-------|
| OpenAI tts-1 (default Vapi) | OK, sotaque artificial | 300ms | $0.015/1k chars |
| ElevenLabs v3 / Flash | Excelente, indistinguível | 200ms | $5/mês 30k chars |
| Cartesia Sonic | Excelente, mais rápida | 90ms | Free tier 10k chars |
| Google WaveNet | Bom, robótico | 200ms | $0.016/1k chars |

ElevenLabs ganha em emoção e prosódia natural — pausas, ênfase, hesitações tipo "umh". Crítico se objetivo é bot indistinguível de humano (caso "Clara"). **Pular se** só quer validar fluxo técnico — voz default Vapi atende PoC interno.

#### Caminho mínimo absoluto ($0)

Só Vapi com trial $10 grátis + Hermes existente:
1. Criar conta Vapi
2. Usar número Vapi grátis (US, mas funciona)
3. Configurar prompt + voz pt-BR default
4. Discar pra próprio celular

Tempo: 30min. Suficiente pra validar persona + voz + fluxo antes de pagar Twilio/ElevenLabs.

### Para production-grade

```
Claude API (Opus 4.7 para complexidade, Haiku 4.5 para fallback)
  + Twilio ConversationRelay (controle nível protocolo)
  + Deepgram Nova-3 (ASR pt-BR melhor que Whisper)
  + ElevenLabs v3 Flash (TTS latência <300ms)
  + Postgres (memória de leads + estado de conversa)
  + Lambda functions para tool calls (verificar CPF, calcular dívida, gerar boleto)
  + WhatsApp Business API (envio boleto + sender name custom)
  + SendGrid (email com termos)
```

Custo prod estimado: $0.15-0.25/min total all-in.

## Implementação Sugerida (Hermes)

### 1. Habilitar Vapi + telephony skill

```bash
# ~/.hermes/.env
echo "VAPI_API_KEY=<key>" >> ~/.hermes/.env
echo "TWILIO_ACCOUNT_SID=<sid>" >> ~/.hermes/.env
echo "TWILIO_AUTH_TOKEN=<token>" >> ~/.hermes/.env
```

### 2. System prompt persona "Clara"

```
Você é Clara, assistente virtual de cobrança. Persona:
- Tom: amigável mas firme. Nunca desce no script.
- Sempre identifica-se como assistente virtual no início (compliance LGPD/CDC).
- Verifica identidade pedindo 3 primeiros dígitos do CPF antes de qualquer informação.
- Informa valor EXATO via tool call get_pending(lead_id). NUNCA invente número.
- Opções de negociação: à vista (com desconto, se autorizado) OU prazo até 5 dias úteis.
- Sem parcelamento, sem desconto fora do script.
- Empatia: reconhece objeções ("Entendo, [Nome]") mas mantém o objetivo.
- Ao fechar: repete valor + data + envia boleto via WhatsApp tool.
- Sender name do WhatsApp: "Salvador recuperação"
- Fala SEM ler frases longas — pause naturalmente. Permite interrupção.
```

### 3. Tool calls necessárias

```python
@tool
def verify_cpf(first_3_digits: str, lead_id: str) -> bool:
    """Verifica se os 3 primeiros dígitos do CPF batem com o lead."""

@tool
def get_pending(lead_id: str) -> dict:
    """Retorna {'amount': float, 'period': str, 'due_date': str}."""

@tool
def generate_boleto(lead_id: str, due_date: str, discount: float = 0) -> dict:
    """Gera boleto + retorna {'url': str, 'barcode': str, 'pix': str}."""

@tool
def send_whatsapp(phone: str, boleto_url: str, sender_name: str = "Salvador recuperação") -> bool:
    """Envia boleto via WhatsApp Business."""

@tool
def send_email_terms(email: str, terms_pdf_url: str) -> bool:
    """Envia termos do acordo por email."""
```

### 4. Anti-hallucination: NUNCA o LLM gera valor monetário

Critical: todos os valores (dívida, desconto, parcela) DEVEM vir de tool call. System prompt instrui explicitamente "se você não tem o valor de tool call, pergunte pelo CPF e chame get_pending. NUNCA invente."

### 5. VAD + interrupção

Vapi configurar:
- `silence_timeout_ms: 1500` (espera 1.5s antes de assumir que cliente terminou)
- `barge_in_enabled: true` (permite cliente interromper bot)
- `endpointing_ms: 300` (latência do turn-taking)

## Compliance Regulatório (BR)

| Norma | Exigência | Mitigação |
|-------|-----------|-----------|
| LGPD (Lei 13.709/2018) | Consentimento + identificação do controlador | Bot identifica-se como IA + nome da empresa no opening |
| CDC (Lei 8.078/1990) | Cobrança não pode constranger | Tom respeitoso, sem ameaças, oferece soluções |
| Resolução BACEN 4.949/2021 | Se instituição financeira: registro, transparência | Validar com setor jurídico |
| Lei estadual SP 17.846/2023 | Obrigatoriedade de informar que é IA | Cumprido no script atual |
| Lei estadual RS 16.069/2023 | Informar IA no início + opção de falar com humano | Adicionar "Posso transferir para um atendente?" |
| Bloqueio do Procon | Lista de não-perturbe | Integrar com base de bloqueio antes de discar |

## Limitações Práticas

1. **Hallucination em valores monetários** — risco crítico. Sempre tool calls determinísticos.
2. **TTS qualidade pt-BR** — ElevenLabs/Cartesia tier premium é caro. Mas voz das gravações é claramente premium.
3. **Latência turn-taking** — alvo <500ms para conversa natural. Vapi+ElevenLabs Flash atende.
4. **Code-switching e gírias** — bot precisa entender "boa gente, olha 8" (cliente respondendo CPF). Deepgram Nova-3 melhor que Whisper para isso.
5. **Robustez contra ataques** — bots de cobrança são alvo de prompt injection. Sanitizar input do cliente.
6. **Hermes não atende inbound** — outbound only nativo. Inbound via AgentCall MCP.

## Roadmap

### Fase 1: PoC (1 semana)
- [ ] Conta Vapi + número Twilio teste
- [ ] System prompt persona "Clara" calibrada
- [ ] 5 tool calls básicas (mock backend)
- [ ] 10 chamadas-teste com números próprios
- [ ] Métricas: turn latency, hallucination rate, completion rate

### Fase 2: Backend real (2 semanas)
- [ ] Postgres lead DB + estado conversa
- [ ] Integração com API de boleto (BoletoCloud / iugu)
- [ ] WhatsApp Business API
- [ ] SendGrid templates email
- [ ] Métricas: conversão (acordo fechado / chamada)

### Fase 3: Compliance + scale (3 semanas)
- [ ] Adequação LGPD (consent, data retention)
- [ ] Lista bloqueio Procon
- [ ] Audit log de cada chamada
- [ ] Dashboard de métricas
- [ ] A/B test de scripts

### Fase 4: Production (1 mês)
- [ ] Migração Hermes → Claude direto (latência)
- [ ] Multi-tenant (vários clientes)
- [ ] Self-tuning de scripts via GEPA loop (Hermes feature)

## Repos existentes (open source) — alternativas a começar do zero

Pesquisa de repos production-ready que cobrem voice agent + outbound calls. Permite fork ao invés de partir do zero.

### Top candidatos

#### 1. Bolna ⭐ — melhor open-source production-ready
[github.com/bolna-ai/bolna](https://github.com/bolna-ai/bolna)
- ✓ Outbound calls (Twilio/Plivo, Exotel/Vonage planejado)
- ✓ STT: Deepgram, Azure | TTS: Polly, ElevenLabs, Cartesia, Smallest, OpenAI | LLM: via LiteLLM (Claude, GPT, GLM, Llama, etc.)
- ✓ End-to-end, framework Python
- ✗ Sem template cobrança específico, sem doc pt-BR (mas TTS pt-BR via Cartesia/Polly)

#### 2. Dograh ⭐ — alternativa self-host Vapi/Retell
[github.com/dograh-hq/dograh](https://github.com/dograh-hq/dograh)
- ✓ `docker compose up` deploy
- ✓ Drag-and-drop workflow builder (visual)
- ✓ Bring your own LLM/STT/TTS
- ✓ Twilio/Vonage/Telnyx
- ✗ Sem template cobrança

#### 3. LiveKit agent-starter-python — production-grade starter
[github.com/livekit-examples/agent-starter-python](https://github.com/livekit-examples/agent-starter-python)
- ✓ Dockerfile + framework eval/test embutido
- ✓ 50+ providers (OpenAI, Cartesia, Deepgram, Silero VAD)
- ✓ Turn detector multilingual
- ⚠ Twilio integration via doc separada
- ✗ Sem template cobrança

#### 4. Pipecat — framework Python flexível (v1.2.1 mai/2026)
[github.com/pipecat-ai/pipecat](https://github.com/pipecat-ai/pipecat)
- ✓ Twilio serializer nativo
- ✓ Anthropic, Deepgram, ElevenLabs, Cartesia
- ✓ Self-host + cloud
- ✗ Exemplos só "storytelling/simple-chatbot", sem cobrança
- ✗ Sem doc pt-BR

### Comerciais (referência de fluxo + compliance)

- **Vodex** [vodex.ai/debt-collection](https://www.vodex.ai/debt-collection) — FDCPA/TCPA/CFPB compliant, debt collection é use case principal. Closed source, US-centric. **Útil para inspiração de fluxo e compliance.**

### Outros úteis

- **leads-reactivation-with-AI-Voice-Agent** [github.com/kaymen99/leads-reactivation-with-AI-Voice-Agent](https://github.com/kaymen99/leads-reactivation-with-AI-Voice-Agent) — Voice agent reativando cold leads + CRM sync. Fluxo parecido com cobrança (qualificar lead, oferecer ação).
- **BentoVoiceAgent** [github.com/bentoml/BentoVoiceAgent](https://github.com/bentoml/BentoVoiceAgent) — Phone agent com modelos open source.
- **Rapida voice-ai** [github.com/rapidaai/voice-ai](https://github.com/rapidaai/voice-ai) — End-to-end voice AI orchestration.
- **Voice Bot (Agentic-Insights)** [github.com/Agentic-Insights/voice-bot](https://github.com/Agentic-Insights/voice-bot) — Vocode+Twilio+Deepgram+ElevenLabs. **Arquivado desde dez/2025 — usar com cautela.**
- **awesome-voice-ai** [github.com/amitdev01/awesome-voice-ai](https://github.com/amitdev01/awesome-voice-ai) — lista curada.

### Recursos pt-BR (ASR)

- **CORAA dataset** [github.com/nilc-nlp/CORAA](https://github.com/nilc-nlp/CORAA) — 290.77h de áudio pt-BR + transcrições (400k+ segmentos). Útil para treinar/fine-tunar ASR em pt-BR.
- **Wav2vec 2.0 pt-BR** [arxiv.org/abs/2107.11414](https://arxiv.org/pdf/2107.11414) — modelo ASR pt-BR open source.

### Comparativo dos top 4

| Repo | Type | Twilio | Workflow visual | pt-BR ready | Curva |
|------|------|--------|-----------------|-------------|-------|
| Bolna | Python framework | ✓ | ✗ | TTS sim, ASR sim | Média |
| Dograh | Platform self-host | ✓ | ✓ | TTS sim, ASR sim | Baixa |
| LiveKit starter | Python framework | ⚠ doc separada | ✗ | Multilingual VAD | Média |
| Pipecat | Python framework | ✓ | ✗ | TTS sim, ASR sim | Média |

### Recomendação para este projeto

**Fork Bolna ou Dograh** e calibrar para cobrança:
- **Bolna** se quer Python framework, mais controle de código
- **Dograh** se quer drag-and-drop e UI

Para pt-BR: usar **Cartesia Sonic** (free tier, latência 90ms) ou **ElevenLabs v3** como TTS. Ambos têm vozes brasileiras boas. ASR: **Deepgram Nova-3** com `language: pt-BR` OU Whisper large fine-tuned com CORAA.

#### Roadmap atalho (fork Bolna)

1. Fork [github.com/bolna-ai/bolna](https://github.com/bolna-ai/bolna)
2. Adaptar `examples/` para fluxo cobrança (8 etapas do Clara — ver seção "Análise das Gravações")
3. Plug Twilio + Cartesia/ElevenLabs pt-BR
4. Adicionar 5 tool calls (verify_cpf, get_pending, generate_boleto, send_whatsapp, send_email_terms)
5. Adapter compliance LGPD no system prompt
6. **Tempo estimado: 1-2 semanas pra PoC funcional** vs 4+ semanas começando do zero

## Conceitos Relacionados

- **2026-05-22-hermes-vs-openclaw-latency** — análise por que Hermes é lento, importante para latência de voz
- **2026-05-22-openclaw-vs-hermes-skill-patterns** — como estruturar skill telephony
- **agent-governance-toolkit** — guardrails para agentes em produção

## Transcripts (referência)

> Transcripts originais das 3 chamadas que motivaram esta análise contêm dados pessoais (nomes, telefones, CPF parcial, valores de dívida) e por isso **não** estão neste repositório. Estão armazenados localmente fora do controle de versão.
>
> Casos analisados:
> - BancoY — bot debt collection (1.5min)
> - OperadoraSaude-X "Clara" — bot cobrança plano de saúde, 2 chamadas distintas (4min e 6min)
>
> Os padrões observados estão sintetizados na seção [Análise das Gravações](#análise-das-gravações) acima.

## Referências

- [Hermes Telephony — Twilio/Bland.ai/Vapi](https://hermes-agent.nousresearch.com/docs/user-guide/skills/optional/productivity/productivity-telephony)
- [Hermes Voice Mode](https://hermes-agent.nousresearch.com/docs/user-guide/features/voice-mode)
- [AgentCall — inbound via MCP](https://agentcall.co/docs/hermes)
- [Claude ConversationRelay (Twilio Voice)](https://www.twilio.com/en-us/blog/integrate-anthropic-twilio-voice-using-conversationrelay)
- [Anthropic Claude Connector for Twilio](https://www.twilio.com/en-us/blog/partners/introducing-twilio-claude-connector-claude-code-plugin)
- [Vapi MCP + Hermes (Composio)](https://composio.dev/toolkits/vapi/framework/hermes-agent)
- [Twilio vs Vapi vs Bland AI 2026](https://ortemtech.com/blog/twilio-vs-vapi-vs-bland-ai-voice-agent-comparison-2026)
- [Vapi Review 2026 — Softailed](https://softailed.com/blog/vapi-review)
- [Anthropic Twilio Function Calling Tutorial](https://www.twilio.com/en-us/blog/developers/tutorials/product/function-calling-twilio-voice-anthropic-claude-integration)
- [Bolna — Conversational voice AI agents (GitHub)](https://github.com/bolna-ai/bolna)
- [Dograh — Open source Voice Agent Platform (GitHub)](https://github.com/dograh-hq/dograh)
- [LiveKit Agents Framework (GitHub)](https://github.com/livekit/agents)
- [LiveKit agent-starter-python (GitHub)](https://github.com/livekit-examples/agent-starter-python)
- [Pipecat — Open source voice/multimodal AI (GitHub)](https://github.com/pipecat-ai/pipecat)
- [Voice Bot — Vocode+Twilio+Deepgram+ElevenLabs (archived, GitHub)](https://github.com/Agentic-Insights/voice-bot)
- [BentoVoiceAgent (GitHub)](https://github.com/bentoml/BentoVoiceAgent)
- [Rapida voice-ai (GitHub)](https://github.com/rapidaai/voice-ai)
- [leads-reactivation-with-AI-Voice-Agent (GitHub)](https://github.com/kaymen99/leads-reactivation-with-AI-Voice-Agent)
- [awesome-voice-ai (GitHub)](https://github.com/amitdev01/awesome-voice-ai)
- [CORAA pt-BR ASR dataset (GitHub)](https://github.com/nilc-nlp/CORAA)
- [Wav2vec 2.0 pt-BR paper](https://arxiv.org/pdf/2107.11414)
- [Vodex AI Debt Collection (commercial)](https://www.vodex.ai/debt-collection)
