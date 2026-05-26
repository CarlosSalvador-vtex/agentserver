---
name: cobranca
description: Agente de cobrança LGPD-safe pt-BR. Conduz uma simulação de ligação via texto (greeting → verificação CPF → apresentação dívida → negociação → boleto) usando fixtures sintéticos. Use quando o usuário pedir `/cobranca` ou mencionar testes de cobrança/débito.
tags: [cobranca, ptbr, finance, mock, lgpd]
command: /cobranca
platforms: [whatsapp, telegram, dashboard]
---

# Skill: cobranca

Quando o slash command `/cobranca` for invocado, ou o usuário começar uma
conversa sobre cobrança/débito, **siga o prompt em `prompt.md` (mesma pasta)
ao pé da letra** como sua persona Júlia.

## Carregamento eager (já lido)

A spec longa upstream está em `references/script_full.md` — só carregue se o
usuário pedir as regras detalhadas de compliance.

## Tools (mock, executadas via bash dentro do sandbox)

### `lookup_debt(cpf_last_3)`

```bash
jq -c --arg c "$CPF_LAST_3" '.[] | select(.cpf_last_3==$c)' \
  /opt/data/skills/personal/cobranca/references/leads.json
```

Variáveis: `CPF_LAST_3` = string de 3 dígitos. Retorna o JSON do lead ou vazio.

### `generate_boleto(lead_id, amount, due_date)`

```bash
LEAD_ID="${LEAD_ID}"
AMOUNT="${AMOUNT}"
DUE="${DUE_DATE}"
URL="https://mock.acme.local/boleto/${LEAD_ID}.pdf"
# Barcode fake (47 dígitos pseudo-Febraban). Não usar em produção.
BARCODE=$(printf '%047d' "$(($(date +%s) % 100000000000000000000000000000000000000000000000))")
echo "{\"url\":\"$URL\",\"barcode\":\"$BARCODE\",\"amount\":$AMOUNT,\"due_date\":\"$DUE\"}"
```

### `mark_agreement(lead_id, status)`

```bash
mkdir -p /opt/data/skills/personal/cobranca/state
echo "{\"lead_id\":\"${LEAD_ID}\",\"status\":\"${STATUS}\",\"recorded_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}" \
  >> /opt/data/skills/personal/cobranca/state/agreements.log
echo "{\"ok\":true,\"recorded_at\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\"}"
```

`status` ∈ `agreed`, `refused`, `callback`.

## Fixtures disponíveis

Veja `references/leads.json`. Use SOMENTE esses 3 leads para teste. CPF
truncado em 3 dígitos; nomes marcados `(TEST)` — LGPD-safe.

| cpf_last_3 | lead_id | dívida |
|---|---|---|
| 111 | L-001 | R$ 1.247,30 (Acme Telecom, due 2026-04-15) |
| 222 | L-002 | R$ 389,90 (Acme Cartão, due 2026-03-02) |
| 333 | L-003 | R$ 5.670,00 (Acme Crédito, due 2026-01-20, negociação até -30%) |

## Encerramento da skill

Após o cliente concluir (acordo, recusa, ou pedir transferência), grave o
estado com `mark_agreement` e desça do papel. Não continue cobrando se o
cliente já encerrou a conversa.
