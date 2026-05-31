package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// simCobrancaLeads mirrors deploy/helm/agentserver/skills/cobranca/references/leads.json.
// LGPD-safe synthetic records only — never use real PII.
var simCobrancaLeads = []map[string]interface{}{
	{
		"lead_id":     "L-001",
		"name_masked": "Maria Aparecida (TEST)",
		"cpf_last_3": "111",
		"amount":      1247.30,
		"due_date":    "2026-04-15",
		"creditor":    "Acme Telecom",
		"status":      "open",
	},
	{
		"lead_id":     "L-002",
		"name_masked": "Joao da Silva (TEST)",
		"cpf_last_3": "222",
		"amount":      389.90,
		"due_date":    "2026-03-02",
		"creditor":    "Acme Cartao",
		"status":      "open",
	},
	{
		"lead_id":     "L-003",
		"name_masked": "Ana Souza (TEST)",
		"cpf_last_3": "333",
		"amount":      5670.00,
		"due_date":    "2026-01-20",
		"creditor":    "Acme Credito",
		"status":      "overdue",
		"note":        "negotiation_authorized_up_to_30pct",
	},
}

func normalizeCpfLast3(input string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, input)
	if len(digits) < 3 {
		return digits
	}
	return digits[len(digits)-3:]
}

func lookupSimCobrancaLeadByCpfLast3(cpfLast3 string) (map[string]interface{}, bool) {
	last3 := normalizeCpfLast3(cpfLast3)
	if len(last3) != 3 {
		return nil, false
	}
	for _, lead := range simCobrancaLeads {
		if lead["cpf_last_3"] == last3 {
			return lead, true
		}
	}
	return nil, false
}

// handleSimCobrancaLookup serves GET /api/sim/cobranca/lookup?cpf_last_3=XXX.
// No auth — internal sim endpoint for sandbox skills (same-cluster agentserver).
func (s *Server) handleSimCobrancaLookup(w http.ResponseWriter, r *http.Request) {
	cpfLast3 := r.URL.Query().Get("cpf_last_3")
	lead, found := lookupSimCobrancaLeadByCpfLast3(cpfLast3)

	w.Header().Set("Content-Type", "application/json")
	if !found {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"found":      false,
			"cpf_last_3": cpfLast3,
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"found": true,
		"lead":  lead,
	})
}
