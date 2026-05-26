package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/auth"
)

// Quotas + TTL for ephemeral test sandboxes (see playground-design.md §10).
const (
	playgroundTestSandboxMaxConcurrent = 3
	playgroundTestSandboxTTL           = 10 * time.Minute
	playgroundReaperInterval           = 30 * time.Second
)

// playgroundTestSandboxRequest is the body of POST /test-sandbox.
type playgroundTestSandboxRequest struct {
	WorkspaceID string `json:"workspace_id"`
	SandboxType string `json:"sandbox_type"`        // openclaw | hermes
	SoulRef     string `json:"soul_ref,omitempty"`  // optional
	Name        string `json:"name,omitempty"`      // optional
}

type playgroundTestSandboxResponse struct {
	SandboxID string `json:"sandbox_id"`
	ExpiresAt string `json:"expires_at"`
	Strategy  string `json:"strategy"` // always "test" — distinguishes from prod compositions
}

// handleSkillDraftTestSandbox spawns a short-lived sandbox bound to a
// draft skill + optional draft soul. Enforces a per-user concurrency
// cap; the reaper goroutine deletes the pod when the TTL elapses.
func (s *Server) handleSkillDraftTestSandbox(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	draftID := chi.URLParam(r, "id")

	skill, err := s.DB.GetSkillDraft(draftID)
	if err != nil || skill == nil {
		http.Error(w, "draft not found", http.StatusNotFound)
		return
	}
	if !skill.AuthorUserID.Valid || skill.AuthorUserID.String != userID {
		http.Error(w, "not your draft", http.StatusForbidden)
		return
	}

	var req playgroundTestSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if req.WorkspaceID == "" {
		http.Error(w, "workspace_id is required", http.StatusBadRequest)
		return
	}
	if req.SandboxType == "" {
		req.SandboxType = "openclaw"
	}
	if req.SandboxType != "openclaw" && req.SandboxType != "hermes" {
		http.Error(w, "sandbox_type must be openclaw or hermes", http.StatusBadRequest)
		return
	}
	if !s.requireWorkspaceRole(w, r, req.WorkspaceID, "owner", "maintainer", "developer") {
		return
	}

	// Quota enforcement — non-archived test sandboxes count, regardless of
	// whether the underlying sandbox is still actually running. The reaper
	// drains the count by deleting both rows + pods.
	count, err := s.DB.CountActivePlaygroundTestSandboxes(userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if count >= playgroundTestSandboxMaxConcurrent {
		http.Error(w,
			fmt.Sprintf("quota exceeded: %d test sandboxes already running", count),
			http.StatusForbidden)
		return
	}

	name := req.Name
	if name == "" {
		name = "Test — " + skill.Name
	}

	sbx, err := s.provisionSandbox(r.Context(), req.WorkspaceID, provisionInput{
		Name: name,
		Type: req.SandboxType,
	})
	var pe *provisionError
	if errors.As(err, &pe) {
		writeProvisionError(w, pe)
		return
	}
	if err != nil {
		log.Printf("playground test sandbox: provision: %v", err)
		http.Error(w, "failed to provision test sandbox", http.StatusInternalServerError)
		return
	}

	// Composition row carrying the draft ref (and optional soul ref).
	skillRefs := []string{"draft:" + skill.ID}
	if compErr := s.DB.CreateSandboxComposition(
		sbx.ID, req.SoulRef, skillRefs, nil, false,
	); compErr != nil {
		log.Printf("playground test sandbox: composition row %s: %v", sbx.ID, compErr)
	}

	if err := s.DB.CreatePlaygroundTestSandbox(sbx.ID, userID, playgroundTestSandboxTTL); err != nil {
		// The pod is already up — log + carry on. Operator can clean up
		// manually if the reaper insert failed (rare).
		log.Printf("playground test sandbox: register %s: %v", sbx.ID, err)
	}

	expiresAt := time.Now().Add(playgroundTestSandboxTTL).UTC().Format(time.RFC3339)
	writeJSON(w, http.StatusOK, playgroundTestSandboxResponse{
		SandboxID: sbx.ID,
		ExpiresAt: expiresAt,
		Strategy:  "test",
	})
}

// StartPlaygroundReaper runs in the background, polling
// playground_test_sandboxes for entries past their TTL and deleting
// both the row and the underlying sandbox pod. Safe to call once at
// server startup; cancel via ctx to stop.
func (s *Server) StartPlaygroundReaper(ctx context.Context) {
	go func() {
		t := time.NewTicker(playgroundReaperInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.reapExpiredTestSandboxes(ctx)
			}
		}
	}()
	log.Printf("Playground test sandbox reaper started (interval: %s, ttl: %s)",
		playgroundReaperInterval, playgroundTestSandboxTTL)
}

func (s *Server) reapExpiredTestSandboxes(ctx context.Context) {
	ids, err := s.DB.ListExpiredPlaygroundTestSandboxes()
	if err != nil {
		log.Printf("playground reaper: list expired: %v", err)
		return
	}
	for _, sandboxID := range ids {
		// Delete pod via process manager — same path as normal delete.
		// The sandboxes table cascade-deletes
		// playground_test_sandboxes via ON DELETE CASCADE on sandbox_id.
		if err := s.Sandboxes.Delete(sandboxID); err != nil {
			log.Printf("playground reaper: delete %s: %v", sandboxID, err)
			continue
		}
		if s.ProcessManager != nil {
			_ = s.ProcessManager.Stop(sandboxID)
		}
		log.Printf("playground reaper: deleted expired test sandbox %s", sandboxID)
	}
}
