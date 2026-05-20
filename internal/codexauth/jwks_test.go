package codexauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestJWKS_ReturnsActiveKeys(t *testing.T) {
	srv := newAuthTestServer(t, "")
	r := chi.NewRouter()
	srv.Mount(r)

	// Add one more inactive key to cover the rotation case.
	kid2, kp2, _ := GenerateRSAKey()
	srv.Store.InsertJwksKey(context.Background(), kid2, kp2, false)

	req := httptest.NewRequest(http.MethodGet, "/agent-identities/jwks", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var doc struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(doc.Keys) != 2 {
		t.Errorf("got %d keys, want 2 (one active + one inactive)", len(doc.Keys))
	}
	for _, k := range doc.Keys {
		if k["kty"] != "RSA" || k["alg"] != "RS256" || k["use"] != "sig" {
			t.Errorf("bad key shape: %+v", k)
		}
		if k["kid"] == "" || k["n"] == "" || k["e"] == "" {
			t.Errorf("missing required field: %+v", k)
		}
	}
}
