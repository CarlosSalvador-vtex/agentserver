# B1 — Skills com endpoints reais (dados externos via HTTP)

> Pendência B1 de `docs/pendencias-investigacao.md`. GitHub Issues disabled → handoff doc.

## Contexto

As skills atuais (cobrança) leem dados de um fixture **local** (`leads.json`) embutido
no pod. Objetivo B1: as tools chamarem um **endpoint HTTP** real para buscar os dados,
removendo o hardcoding e abrindo caminho para integração multi-tenant (cada tenant
conecta seu próprio sistema de dados).

## Estado atual (verificado)

- `deploy/helm/agentserver/skills/cobranca/index.mjs`: tool `lookup_debt` chama
  `findLeadByCpfLast3()` que lê `references/leads.json` (array de leads sintéticos).
- Tools `generate_boleto` + `mark_agreement` idem (fixture local + Map em memória).
- Pod do sandbox alcança o agentserver via service DNS `http://agentserver:8080`
  (mesmo cluster). Env vars são injetadas em `internal/sandbox/manager.go` via
  `containerEnv := []corev1.EnvVar{...}`.

## Mudança proposta

Padrão de 3 camadas — endpoint mock no agentserver + env var de base URL + tool com fetch
e fallback:

### 1. Endpoint mock no agentserver

Novo arquivo `internal/server/sim_endpoints.go`:
- `GET /api/sim/cobranca/lookup?cpf_last_3=XXX` → retorna o lead sintético (mesma shape
  de `leads.json`): `{ found: bool, lead?: {...} }`.
- Dados sintéticos LGPD-safe embutidos no Go (mesmos 3 leads do fixture, marcados TEST).
- Sem auth no MVP (endpoint interno de simulação); marcar claramente como mock/sim.
- Registrar em `server.go`: `r.Get("/api/sim/cobranca/lookup", s.handleSimCobrancaLookup)`.

### 2. Env var de base URL no sandbox

Em `internal/sandbox/manager.go`, adicionar ao `containerEnv` (ambos os caminhos de
criação — `StartContainerWithIP` e o equivalente openclaw):
```go
containerEnv = append(containerEnv, corev1.EnvVar{
    Name:  "SIM_API_BASE_URL",
    Value: "http://agentserver:8080",
})
```
Valor default = serviço in-cluster. **Multi-tenant:** futuramente sobrescrever por
workspace (campo `sim_api_base_url` em `workspace_settings`) — fora do escopo deste PR,
mas deixar o ponto de extensão claro num comentário.

### 3. Skill chama endpoint com fallback

Em `index.mjs`, `lookup_debt.execute`:
```js
async execute(_toolCallId, rawParams) {
  const last3 = normalizeCpfDigits(rawParams?.cpf_last_3).slice(-3);
  const base = process.env.SIM_API_BASE_URL;
  if (base) {
    try {
      const res = await fetch(`${base}/api/sim/cobranca/lookup?cpf_last_3=${last3}`, {
        signal: AbortSignal.timeout(5000),
      });
      if (res.ok) return await res.json();
    } catch (e) {
      api.logger?.warn?.(`[cobranca] sim endpoint unreachable, falling back to fixture: ${e}`);
    }
  }
  // Fallback: local fixture (preserves current behavior).
  const lead = findLeadByCpfLast3(last3);
  return lead ? { found: true, lead } : { found: false, cpf_last_3: last3 };
}
```

## Acceptance criteria

1. `GET /api/sim/cobranca/lookup?cpf_last_3=111` → `{ found: true, lead: { lead_id: "L-001", ... } }`.
2. `GET /api/sim/cobranca/lookup?cpf_last_3=999` → `{ found: false }`.
3. Skill com `SIM_API_BASE_URL` setado → `lookup_debt` busca do endpoint (verificável no log).
4. Skill sem `SIM_API_BASE_URL` OU endpoint inalcançável → fallback pro fixture local
   (comportamento atual preservado, fail-open).
5. Dados continuam 100% sintéticos LGPD-safe (sem PII real).
6. `go build -tags goolm ./...` + `go vet` passam; teste unitário do handler
   (`sim_endpoints_test.go`) cobrindo found + not-found.

## Out of scope

- Override de `SIM_API_BASE_URL` por workspace (multi-tenant real) — PR seguinte.
- Migrar `generate_boleto` + `mark_agreement` para endpoints — fazer só `lookup_debt`
  neste PR como prova de conceito; replicar depois.
- Auth no endpoint sim (interno-only por enquanto).
- Endpoints para vendas/SAC — replicar o padrão depois que cobrança provar.

## Arquivos

| Arquivo | Mudança |
|---|---|
| `internal/server/sim_endpoints.go` | NOVO: handler + dados sintéticos embutidos |
| `internal/server/sim_endpoints_test.go` | NOVO: testes found/not-found |
| `internal/server/server.go` | Registrar rota `GET /api/sim/cobranca/lookup` |
| `internal/sandbox/manager.go` | Injetar env `SIM_API_BASE_URL` no containerEnv |
| `deploy/helm/agentserver/skills/cobranca/index.mjs` | `lookup_debt` fetch + fallback |

## Effort

~M. Endpoint + dados: ~45min. Env injection: ~15min. Skill fetch+fallback: ~30min.
Testes: ~30min. Total: ~2h.

## Related

- `docs/pendencias-investigacao.md` (B1).
- `deploy/helm/agentserver/skills/cobranca/index.mjs` (tool pattern).
