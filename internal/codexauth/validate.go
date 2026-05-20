package codexauth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type validateRequest struct {
	Scheme    string `json:"scheme"`
	Token     string `json:"token,omitempty"`
	Assertion string `json:"assertion,omitempty"`
	AccountID string `json:"account_id,omitempty"`
}

// HandleValidate is the internal endpoint codex-exec-gateway calls
// over X-Internal-Secret to verify a Bearer / AgentAssertion token.
// Returns 200 with {user_id} or 401 with {error}.
func (s *Server) HandleValidate(w http.ResponseWriter, r *http.Request) {
	var req validateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	uid, err := s.validate(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"user_id": uid})
}

func (s *Server) validate(ctx context.Context, req validateRequest) (string, error) {
	switch req.Scheme {
	case "bearer":
		uid, err := s.Store.LookupAccessToken(ctx, req.Token)
		if err != nil {
			return "", err
		}
		if uid == "" {
			return "", fmt.Errorf("bearer token unknown or expired")
		}
		// Defense-in-depth: ChatGPT-mode bearer requests always carry
		// the ChatGPT-Account-ID header (codex's BearerAuthProvider adds it
		// unconditionally — model-provider/src/bearer_auth_provider.rs).
		// We mint id_token with chatgpt_account_id == user_id (see
		// BuildIDToken in claims.go), so codex will echo our user_id back.
		// Reject mismatches.
		if req.AccountID != "" && req.AccountID != uid {
			return "", fmt.Errorf("ChatGPT-Account-ID header does not match token owner")
		}
		return uid, nil
	case "agent_assertion":
		return s.validateAgentAssertion(ctx, req)
	default:
		return "", fmt.Errorf("unknown scheme %q", req.Scheme)
	}
}

func (s *Server) validateAgentAssertion(ctx context.Context, req validateRequest) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(req.Assertion)
	if err != nil {
		return "", fmt.Errorf("base64url decode: %w", err)
	}
	var a struct {
		AgentRuntimeID string `json:"agent_runtime_id"`
		TaskID         string `json:"task_id"`
		Timestamp      string `json:"timestamp"`
		Signature      string `json:"signature"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("assertion json: %w", err)
	}
	if a.AgentRuntimeID == "" || a.TaskID == "" || a.Timestamp == "" || a.Signature == "" {
		return "", fmt.Errorf("assertion missing fields")
	}

	identity, err := s.Store.GetAgentIdentity(ctx, a.AgentRuntimeID)
	if err != nil {
		return "", err
	}
	if identity == nil {
		return "", fmt.Errorf("unknown agent")
	}
	if req.AccountID != "" && req.AccountID != identity.UserID {
		return "", fmt.Errorf("account_id header does not match identity owner")
	}

	task, err := s.Store.GetAgentTask(ctx, a.TaskID)
	if err != nil {
		return "", err
	}
	if task == nil || task.AgentRuntimeID != a.AgentRuntimeID {
		return "", fmt.Errorf("task_id unknown or mismatched")
	}

	ts, err := time.Parse(time.RFC3339, a.Timestamp)
	if err != nil {
		return "", fmt.Errorf("bad timestamp")
	}
	if d := time.Since(ts); d > taskRegisterClockSkew || d < -taskRegisterClockSkew {
		return "", fmt.Errorf("timestamp outside replay window")
	}

	sig, err := base64.StdEncoding.DecodeString(a.Signature)
	if err != nil {
		return "", fmt.Errorf("signature base64: %w", err)
	}
	msg := []byte(a.AgentRuntimeID + ":" + a.TaskID + ":" + a.Timestamp)
	if !ed25519.Verify(ed25519.PublicKey(identity.PublicKey), msg, sig) {
		return "", fmt.Errorf("signature invalid")
	}
	return identity.UserID, nil
}
