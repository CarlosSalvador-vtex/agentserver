package imbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

const routingModeOpenclaw = "openclaw"

type openclawAgentResult struct {
	Payloads []struct {
		Text string `json:"text"`
	} `json:"payloads"`
}

// openclawSessionID returns a stable session id for OpenClaw memory scoped to
// channel + end user (bare user id, no @im.* suffix).
func openclawSessionID(channelID, fromUserID string) string {
	sum := sha256.Sum256([]byte(channelID + "\x00" + fromUserID))
	return "im-" + hex.EncodeToString(sum[:16])
}

func buildOpenclawAgentCommand(message, sessionID string) []string {
	return []string{
		"node", "openclaw.mjs", "agent",
		"--message", message,
		"--json",
		"--session-id", sessionID,
	}
}

func parseOpenclawAgentStdout(stdout string) (string, error) {
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return "", fmt.Errorf("openclaw agent: empty stdout")
	}
	var result openclawAgentResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return "", fmt.Errorf("openclaw agent: parse json: %w", err)
	}
	var parts []string
	for _, p := range result.Payloads {
		if t := strings.TrimSpace(p.Text); t != "" {
			parts = append(parts, t)
		}
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("openclaw agent: no payloads text")
	}
	return strings.Join(parts, "\n\n"), nil
}

func (b *Bridge) forwardToOpenclaw(ctx context.Context, binding BridgeBinding, msg InboundMessage) (bool, error) {
	sandboxID, _, _, _, err := b.db.GetSandboxForChannel(binding.ChannelID)
	if err != nil {
		log.Printf("imbridge: openclaw channel %s no running sandbox: %v", binding.ChannelID, err)
		return false, nil
	}

	sessionID := openclawSessionID(binding.ChannelID, msg.FromUserID)
	cmd := buildOpenclawAgentCommand(msg.Text, sessionID)

	execCtx, cancel := context.WithTimeout(ctx, forwardTimeout)
	defer cancel()

	stdout, err := b.exec.ExecSimple(execCtx, sandboxID, cmd)
	if err != nil {
		return false, err
	}

	reply, err := parseOpenclawAgentStdout(stdout)
	if err != nil {
		return false, err
	}

	if err := binding.Provider.Send(ctx, &binding.Credentials, msg.FromUserID, reply, msg.Metadata); err != nil {
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
