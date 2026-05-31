package imbridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
)

const routingModeOpenclaw = "openclaw"

// openclawSessionID returns a stable session id for OpenClaw memory scoped to
// channel + end user (bare user id, no @im.* suffix).
func openclawSessionID(channelID, fromUserID string) string {
	sum := sha256.Sum256([]byte(channelID + "\x00" + fromUserID))
	return "im-" + hex.EncodeToString(sum[:16])
}

// buildOpenclawAgentCommand returns the command vector for `openclaw agent`.
// Used by tests and by the agentserver-side turn handler (the imbridge no longer
// calls ExecSimple directly — it POSTs to agentserver's /api/internal/openclaw/turn).
func buildOpenclawAgentCommand(message, sessionID string) []string {
	return []string{
		"node", "openclaw.mjs", "agent",
		"--message", message,
		"--json",
		"--session-id", sessionID,
	}
}

// parseOpenclawAgentStdout extracts the agent reply from openclaw agent --json stdout.
// Config warnings are on stderr (captured separately by ExecSimple); this only parses stdout.
// parseOpenclawAgentStdout parses `openclaw agent --json` stdout.
// Actual shape: {"status":"ok","result":{"payloads":[{"text":"..."}],...}}
func parseOpenclawAgentStdout(stdout string) (string, error) {
	out := strings.TrimSpace(stdout)
	if out == "" {
		return "", fmt.Errorf("openclaw agent: empty stdout")
	}
	var result struct {
		Status string `json:"status"`
		Result struct {
			Payloads []struct {
				Text string `json:"text"`
			} `json:"payloads"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return "", fmt.Errorf("openclaw agent: parse json: %w", err)
	}
	var parts []string
	for _, p := range result.Result.Payloads {
		if t := strings.TrimSpace(p.Text); t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("openclaw agent: no payloads text (status=%s)", result.Status)
	}
	return strings.Join(parts, "\n\n"), nil
}

// openclawTurnRequest is the payload for POST /api/internal/openclaw/turn
// handled by agentserver, which owns the sandbox manager + can ExecSimple.
type openclawTurnRequest struct {
	ChannelID  string `json:"channel_id"`
	FromUserID string `json:"from_user_id"`
	Text       string `json:"text"`
	SessionID  string `json:"session_id"`
}

type openclawTurnResponse struct {
	Reply string `json:"reply"`
}

func (b *Bridge) forwardToOpenclaw(ctx context.Context, binding BridgeBinding, msg InboundMessage) (bool, error) {
	sessionID := openclawSessionID(binding.ChannelID, msg.FromUserID)

	payload, _ := json.Marshal(openclawTurnRequest{
		ChannelID:  binding.ChannelID,
		FromUserID: msg.FromUserID,
		Text:       msg.Text,
		SessionID:  sessionID,
	})

	url := b.agentserverURL + "/api/internal/openclaw/turn"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return false, fmt.Errorf("openclaw turn: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if secret := os.Getenv("INTERNAL_API_SECRET"); secret != "" {
		req.Header.Set("X-Internal-Secret", secret)
	}

	hctx, cancel := context.WithTimeout(ctx, openclawForwardTimeout)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(hctx))
	if err != nil {
		return false, fmt.Errorf("openclaw turn: POST: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("openclaw turn: status %d body=%s", resp.StatusCode, truncateForLog(string(body), 200))
	}

	var result openclawTurnResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return false, fmt.Errorf("openclaw turn: decode response: %w", err)
	}
	if result.Reply == "" {
		return false, fmt.Errorf("openclaw turn: empty reply")
	}

	if err := binding.Provider.Send(ctx, &binding.Credentials, msg.FromUserID, result.Reply, msg.Metadata); err != nil {
		return false, err
	}
	log.Printf("imbridge: openclaw reply sent channel=%s user=%s", binding.ChannelID, msg.FromUserID)
	return true, nil
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
