package imbridgesvc

import (
	"context"
	"log"

	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/imbridge"
)

func (s *Server) checkerForChannel(ctx context.Context, ch *db.IMChannel) imbridge.GuardrailsChecker {
	if ch == nil {
		return imbridge.NoopGuardrails{}
	}
	return imbridge.GuardrailsForChannel(&imbridge.ChannelScopeInfo{
		WorkspaceID:      ch.WorkspaceID,
		ScopeDescription: ch.ScopeDescription,
	}, s.llmProxyURL, s.guardrailsTokenProvider())
}

func (s *Server) guardrailsTokenProvider() imbridge.WorkspaceTokenProvider {
	return imbridge.DBWorkspaceTokenProvider{
		GetToken: func(ctx context.Context, workspaceID string) (string, error) {
			return s.db.GetOrCreateWorkspaceToken(workspaceID)
		},
	}
}

func (s *Server) dispatchWhatsAppInbound(ctx context.Context, channel *db.IMChannel, senderName, fromUserID, text string) {
	if channel == nil || text == "" {
		return
	}
	checker := s.checkerForChannel(ctx, channel)
	if dec := checker.CheckInbound(ctx, text); !dec.Allowed {
		log.Printf("whatsapp guardrails: inbound blocked channel=%s user=%s reason=%s", channel.ID, fromUserID, dec.Reason)
		s.sendWhatsAppDirectText(ctx, channel, fromUserID, imbridge.DefaultInboundRedirectMessage)
		return
	}
	inbound := imbridge.InboundMessage{
		FromUserID: fromUserID,
		SenderName: senderName,
		Text:       text,
	}
	if _, err := s.bridge.DispatchInbound(ctx, channel.ID, inbound); err != nil {
		log.Printf("whatsapp: dispatch channel=%s user=%s: %v", channel.ID, fromUserID, err)
	}
}

func (s *Server) sendWhatsAppDirectText(ctx context.Context, channel *db.IMChannel, toUserID, text string) {
	if channel == nil || text == "" || toUserID == "" {
		return
	}
	provider := s.bridge.GetProvider("whatsapp")
	if provider == nil {
		return
	}
	creds := &imbridge.Credentials{
		ChannelID:        channel.ID,
		WorkspaceID:      channel.WorkspaceID,
		BotID:            channel.BotID,
		BotToken:         channel.BotToken,
		BaseURL:          channel.BaseURL,
		ScopeDescription: channel.ScopeDescription,
	}
	sendCtx := imbridge.ContextWithGuardrailsChecker(ctx, imbridge.NoopGuardrails{})
	if err := provider.Send(sendCtx, creds, toUserID, text, nil); err != nil {
		log.Printf("whatsapp: send direct text to %s: %v", toUserID, err)
	}
}
