package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/db"
)

// SandboxExecerIface is the minimal interface the OpenClaw-direct turn handler
// needs from the sandbox manager. *sandbox.Manager satisfies this.
type SandboxExecerIface interface {
	ExecSimple(ctx context.Context, sandboxID string, command []string) (string, error)
}

const openclawTurnTimeout = 120 * time.Second

// handleOpenclawTurn is the server-side handler for
// POST /api/internal/openclaw/turn — called by the imbridge poller when
// routing_mode="openclaw". It runs one OpenClaw agent turn inside the
// sandbox pod (via ExecSimple, which uses the agentserver SA that has the
// correct kubelet TLS trust) and returns the reply text.
//
// This endpoint intentionally does NOT require user auth — it is guarded by
// INTERNAL_API_SECRET and is not reachable from the public internet.
func (s *Server) handleOpenclawTurn(w http.ResponseWriter, r *http.Request) {
	// Internal-secret guard (same pattern as /api/internal/imbridge/codex/turn).
	if secret := os.Getenv("INTERNAL_API_SECRET"); secret != "" {
		if r.Header.Get("X-Internal-Secret") != secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	if s.SandboxExecer == nil {
		http.Error(w, "openclaw exec not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		ChannelID  string `json:"channel_id"`
		FromUserID string `json:"from_user_id"`
		Text       string `json:"text"`
		SessionID  string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChannelID == "" || req.Text == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	sandboxID, _, _, _, err := s.DB.GetSandboxForChannel(req.ChannelID)
	if err != nil {
		log.Printf("openclaw turn: GetSandboxForChannel channel=%s: %v", req.ChannelID, err)
		http.Error(w, "no running sandbox for channel", http.StatusNotFound)
		return
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = "im-default"
	}

	cmd := []string{
		"node", "openclaw.mjs", "agent",
		"--message", req.Text,
		"--json",
		"--session-id", sessionID,
	}

	ctx, cancel := context.WithTimeout(r.Context(), openclawTurnTimeout)
	defer cancel()

	stdout, err := s.SandboxExecer.ExecSimple(ctx, sandboxID, cmd)
	if err != nil {
		log.Printf("openclaw turn: ExecSimple sandbox=%s channel=%s: %v", sandboxID, req.ChannelID, err)
		http.Error(w, "exec failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	reply, err := parseOpenclawStdout(stdout)
	if err != nil {
		log.Printf("openclaw turn: parse stdout sandbox=%s: %v", sandboxID, err)
		http.Error(w, "parse failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"reply": reply})
}

// parseOpenclawStdout extracts the agent reply text from the JSON output of
// `openclaw agent --json`. The CLI prints config warnings to stderr (captured
// separately by ExecSimple) and the JSON result to stdout.
// Actual shape: {"status":"ok","result":{"payloads":[{"text":"..."}],...}}
func parseOpenclawStdout(stdout string) (string, error) {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return "", &openclawParseError{"empty stdout"}
	}
	var result struct {
		Status string `json:"status"`
		Result struct {
			Payloads []struct {
				Text string `json:"text"`
			} `json:"payloads"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return "", &openclawParseError{"json: " + err.Error()}
	}
	var parts []string
	for _, p := range result.Result.Payloads {
		if t := strings.TrimSpace(p.Text); t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return "", &openclawParseError{"no payload text (status=" + result.Status + ")"}
	}
	return strings.Join(parts, "\n\n"), nil
}

type openclawParseError struct{ msg string }

func (e *openclawParseError) Error() string { return "openclaw agent: " + e.msg }

// Compile-time assertion that db.DB satisfies the channel lookup needed.
var _ interface {
	GetSandboxForChannel(channelID string) (sandboxID, podIP, bridgeSecret, assistantName string, err error)
} = (*db.DB)(nil)
