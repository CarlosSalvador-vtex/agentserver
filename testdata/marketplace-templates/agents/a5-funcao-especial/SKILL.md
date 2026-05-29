---
name: Função Especial
description: Executa uma funcao de calculo especial (mock) sobre um numero. Use quando o usuario digitar /especial <numero>.
tags: [acento, edge-case, single-tool, ptbr]
command: /especial
platforms: [whatsapp, telegram]
---

# Skill: Função Especial

Quando `/especial <numero>` for invocado, chame `calcular` e devolva o resultado.
Mock — apenas para testar nome com acento na composicao (#126).

## Tools (mock)

### `calcular(numero)`

```bash
N="${NUMERO:-0}"
echo "{\"input\":$N,\"resultado\":$(( N * N + 1 ))}"
```
