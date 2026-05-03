package ccbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// callTurnFinished is the cc-broker → agentserver internal callback to clear
// the active_turn_id row in agentserver's DB after this turn finishes
// (regardless of success / cancel / panic). Best-effort: failures are logged
// but don't propagate to caller — the leak worker will eventually clean up.
func (s *Server) callTurnFinished(sessionID, turnID string) {
	if s.config.AgentserverInternalURL == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{
		"session_id": sessionID,
		"turn_id":    turnID,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST",
		s.config.AgentserverInternalURL+"/internal/sessions/"+sessionID+"/turn-finished",
		bytes.NewReader(body))
	if err != nil {
		s.logger.Warn("turn-finished: build request", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.config.InternalAPISecret != "" {
		req.Header.Set("X-Internal-Secret", s.config.InternalAPISecret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Warn("turn-finished: callback failed", "session_id", sessionID, "err", err)
		return
	}
	resp.Body.Close()
}
