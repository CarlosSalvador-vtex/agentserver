package server

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/agentserver/agentserver/internal/db"
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

	if s.codexHandler == nil {
		msg := "codex handler not configured"
		next, nerr := db.ComputeNextRun(a.Cron, runAt)
		if nerr != nil {
			errMsg := nerr.Error()
			_ = s.DB.MarkAutomationRun(ctx, a.ID, runAt, &errMsg, time.Time{})
			return
		}
		_ = s.DB.MarkAutomationRun(ctx, a.ID, runAt, &msg, next)
		return
	}

	var cfg struct {
		ChannelID    string `json:"channel_id"`
		WorkspaceID  string `json:"workspace_id"`
		WechatUserID string `json:"wechat_user_id"`
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

	req := codexInboundRequest{
		ChannelID:    cfg.ChannelID,
		WorkspaceID:  cfg.WorkspaceID,
		WechatUserID: cfg.WechatUserID,
		Text:         cfg.Prompt,
	}

	runErr := s.codexHandler.runTurnSync(ctx, req)

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
