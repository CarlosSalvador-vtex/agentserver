package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLookupSimCobrancaLeadByCpfLast3(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		lead, ok := lookupSimCobrancaLeadByCpfLast3("111")
		if !ok {
			t.Fatal("expected found for cpf_last_3=111")
		}
		if lead["lead_id"] != "L-001" {
			t.Fatalf("lead_id = %v, want L-001", lead["lead_id"])
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, ok := lookupSimCobrancaLeadByCpfLast3("999")
		if ok {
			t.Fatal("expected not found for cpf_last_3=999")
		}
	})

	t.Run("invalid length", func(t *testing.T) {
		_, ok := lookupSimCobrancaLeadByCpfLast3("11")
		if ok {
			t.Fatal("expected not found for short input")
		}
	})
}

func TestHandleSimCobrancaLookup_HTTP(t *testing.T) {
	s := &Server{}

	t.Run("found via HTTP", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/sim/cobranca/lookup?cpf_last_3=111", nil)
		rec := httptest.NewRecorder()
		s.handleSimCobrancaLookup(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["found"] != true {
			t.Fatalf("found = %v, want true", body["found"])
		}
		lead, ok := body["lead"].(map[string]interface{})
		if !ok {
			t.Fatal("expected lead object")
		}
		if lead["lead_id"] != "L-001" {
			t.Fatalf("lead_id = %v", lead["lead_id"])
		}
	})

	t.Run("not found via HTTP", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/sim/cobranca/lookup?cpf_last_3=999", nil)
		rec := httptest.NewRecorder()
		s.handleSimCobrancaLookup(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["found"] != false {
			t.Fatalf("found = %v, want false", body["found"])
		}
	})
}
