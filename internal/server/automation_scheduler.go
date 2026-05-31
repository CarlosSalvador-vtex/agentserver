package server

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
	"time"

	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/sbxstore"
)

const (
	automationSchedulerInterval = time.Minute
	automationClaimLease        = 5 * time.Minute
	automationClaimBatch        = 50
)

// StartAutomationScheduler runs an in-process ticker; claims due rows with SKIP LOCKED.
func (s *Server) StartAutomationScheduler(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(automationSchedulerInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runDueAutomations(ctx)
			}
		}
	}()
}

func (s *Server) runDueAutomations(ctx context.Context) {
	if s.DB == nil {
		return
	}
	due, err := s.DB.ClaimDueAutomations(ctx, automationClaimLease, automationClaimBatch)
	if err != nil {
		log.Printf("automation scheduler: scan due: %v", err)
		return
	}
	for _, a := range due {
		s.fireAutomation(ctx, a)
	}
}

func (s *Server) fireAutomation(ctx context.Context, a db.Automation) {
	runAt := time.Now().UTC()

	var cfg struct {
		ChannelID    string `json:"channel_id"`
		WorkspaceID  string `json:"workspace_id"`
		WechatUserID string `json:"wechat_user_id"` // used as to_user_id for all providers
		Prompt       string `json:"prompt"`
	}
	if err := json.Unmarshal(a.Config, &cfg); err != nil {
		msg := "invalid automation config: " + err.Error()
		_ = s.DB.MarkAutomationRun(ctx, a.ID, runAt, &msg, time.Time{})
		return
	}
	if cfg.ChannelID == "" || cfg.WorkspaceID == "" || cfg.WechatUserID == "" || cfg.Prompt == "" {
		msg := "automation config missing channel_id, workspace_id, wechat_user_id, or prompt"
		_ = s.DB.MarkAutomationRun(ctx, a.ID, runAt, &msg, time.Time{})
		return
	}

	// Try OpenClaw turn first (works when channel routing_mode=openclaw and
	// SandboxExecer is configured). Falls back to codexHandler if not available.
	var runErr error
	if s.SandboxExecer != nil {
		runErr = s.fireAutomationOpenClaw(ctx, cfg.ChannelID, cfg.WechatUserID, cfg.Prompt)
		if runErr != nil {
			log.Printf("automation %s: openclaw path failed: %v; falling back to codex", a.ID, runErr)
		}
	}

	if runErr != nil || s.SandboxExecer == nil {
		if s.codexHandler == nil {
			msg := "no turn backend configured (set CODEX_APP_GATEWAY_REST_URL or ensure openclaw sandbox is running)"
			next, nerr := db.ComputeNextRun(a.Cron, runAt)
			if nerr != nil {
				errMsg := nerr.Error()
				_ = s.DB.MarkAutomationRun(ctx, a.ID, runAt, &errMsg, time.Time{})
				return
			}
			_ = s.DB.MarkAutomationRun(ctx, a.ID, runAt, &msg, next)
			return
		}
		req := codexInboundRequest{
			ChannelID:    cfg.ChannelID,
			WorkspaceID:  cfg.WorkspaceID,
			WechatUserID: cfg.WechatUserID,
			Text:         cfg.Prompt,
		}
		runErr = s.codexHandler.runTurnSync(ctx, req)
	}

	var lastErr *string
	if runErr != nil {
		msg := runErr.Error()
		lastErr = &msg
	}
	next, nerr := db.ComputeNextRun(a.Cron, runAt)
	if nerr != nil {
		errMsg := nerr.Error()
		_ = s.DB.MarkAutomationRun(ctx, a.ID, runAt, &errMsg, time.Time{})
		return
	}
	_ = s.DB.MarkAutomationRun(ctx, a.ID, runAt, lastErr, next)
}

// fireAutomationOpenClaw runs an automation turn via the OpenClaw path:
// POST /api/internal/openclaw/turn (agentserver-internal) → agent replies →
// POST /api/internal/imbridge/send delivers to the user.
func (s *Server) fireAutomationOpenClaw(ctx context.Context, channelID, toUserID, prompt string) error {
	sum := sha256.Sum256([]byte(channelID + "\x00" + toUserID))
	sessionID := "im-" + hex.EncodeToString(sum[:16])

	// Get running sandbox for channel (auto-resume handled by handleOpenclawTurn
	// on the HTTP path; here we call ExecSimple directly so we must resolve
	// sandbox ourselves and trigger resume if needed).
	sandboxID, _, _, _, err := s.DB.GetSandboxForChannel(channelID)
	if err != nil {
		pausedID, pauseErr := s.DB.GetPausedSandboxForChannel(channelID)
		if pauseErr != nil {
			return fmt.Errorf("no running sandbox for channel %s: %v", channelID, err)
		}
		resumeCtx, resumeCancel := context.WithTimeout(ctx, 90*time.Second)
		defer resumeCancel()
		podIP, resumeErr := s.SandboxExecer.ResumeContainerWithIP(pausedID)
		if resumeErr != nil {
			return fmt.Errorf("auto-resume sandbox %s: %v", pausedID, resumeErr)
		}
		if podIP != "" {
			_ = s.DB.UpdateSandboxPodIP(pausedID, podIP)
		}
		_ = s.Sandboxes.UpdateStatus(pausedID, sbxstore.StatusRunning)
		_ = resumeCtx
		sandboxID = pausedID
	}

	cmd := []string{"node", "openclaw.mjs", "agent", "--message", prompt, "--json", "--session-id", sessionID}
	execCtx, execCancel := context.WithTimeout(ctx, openclawTurnTimeout)
	defer execCancel()
	stdout, err := s.SandboxExecer.ExecSimple(execCtx, sandboxID, cmd)
	if err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	reply, err := parseOpenclawStdout(stdout)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	// Save to conversation history.
	go func() {
		_ = s.DB.SaveIMMessage(channelID, toUserID, "outbound", reply, sessionID)
	}()

	// Deliver via imbridge direct-send endpoint.
	imbridgeURL := s.IMBridgeURL
	if imbridgeURL == "" {
		imbridgeURL = "http://127.0.0.1:8080"
	}
	payload, _ := json.Marshal(map[string]string{
		"channel_id":  channelID,
		"to_user_id":  toUserID,
		"text":        reply,
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", imbridgeURL+"/api/internal/imbridge/send", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if secret := os.Getenv("INTERNAL_API_SECRET"); secret != "" {
		req.Header.Set("X-Internal-Secret", secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("imbridge send: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("imbridge send: status %d body=%s", resp.StatusCode, body)
	}
	log.Printf("automation: openclaw reply delivered channel=%s user=%s", channelID, toUserID)
	return nil
}
