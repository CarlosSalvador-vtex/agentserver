package codexauth

import "net/http"

// handleJWKS serves GET /agent-identities/jwks — the JSON Web Key Set
// codex fetches to verify Agent Identity JWTs (codex-rs/agent-identity/
// src/lib.rs:147-171 at rust-v0.132.0).
//
// Returns every key in codex_jwks_keys (active and inactive). Inactive
// keys are retained during a rotation grace window so JWTs issued by
// the previous active key still validate.
func (s *Server) handleJWKS(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		http.Error(w, "store not configured", http.StatusInternalServerError)
		return
	}
	keys, err := s.Store.ListAllJwksKeys(r.Context())
	if err != nil {
		http.Error(w, "store: "+err.Error(), http.StatusInternalServerError)
		return
	}
	jwks := make([]map[string]string, 0, len(keys))
	for _, k := range keys {
		jwks = append(jwks, map[string]string{
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"kid": k.Kid,
			"n":   k.PublicN,
			"e":   k.PublicE,
		})
	}
	// 5min cache — codex has no JWKS cache of its own (lib.rs:147-171
	// fetches fresh every JWT load), so this only helps if a CDN is in
	// front. Harmless either way.
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{"keys": jwks})
}
