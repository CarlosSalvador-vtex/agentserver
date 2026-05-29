package imbridgesvc

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/imbridge"
	"github.com/agentserver/agentserver/internal/weixin"
)

type imBindingResponse struct {
	Provider string `json:"provider"`
	BotID    string `json:"bot_id"`
	UserID   string `json:"user_id,omitempty"`
	BoundAt  string `json:"bound_at"`
}

// ---------------------------------------------------------------------------
// Stateless CC outbound messages (POST /api/internal/imbridge/send)
// ---------------------------------------------------------------------------

// handleImbridgeDirectSend sends a text message to an IM user without a
// sandbox binding. Used by agentserver's stateless CC flow to route CC
// responses back to the originating IM user. Authenticated via the
// INTERNAL_API_SECRET shared secret.
func (s *Server) handleImbridgeDirectSend(w http.ResponseWriter, r *http.Request) {
	if secret := os.Getenv("INTERNAL_API_SECRET"); secret != "" {
		if r.Header.Get("X-Internal-Secret") != secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	var req struct {
		ChannelID string `json:"channel_id"`
		ToUserID  string `json:"to_user_id"`
		Text      string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.ChannelID == "" || req.ToUserID == "" || req.Text == "" {
		http.Error(w, "channel_id, to_user_id, and text are required", http.StatusBadRequest)
		return
	}

	channel, err := s.db.GetIMChannel(req.ChannelID)
	if err != nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}

	provider := s.bridge.GetProvider(channel.Provider)
	if provider == nil {
		http.Error(w, "unknown IM provider: "+channel.Provider, http.StatusBadRequest)
		return
	}

	meta, _ := s.db.GetAllChannelMeta(channel.ID, req.ToUserID)
	s.bridge.StopTyping(channel.ID, req.ToUserID)

	creds := &imbridge.Credentials{
		ChannelID: channel.ID,
		BotID:     channel.BotID,
		BotToken:  channel.BotToken,
		BaseURL:   channel.BaseURL,
	}

	if err := provider.Send(r.Context(), creds, req.ToUserID, req.Text, meta); err != nil {
		log.Printf("imbridge direct send: failed channel=%s provider=%s to=%s: %v",
			channel.ID, provider.Name(), req.ToUserID, err)
		http.Error(w, "failed to send message", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
}

// maxDirectSendImageBytes caps the decoded image payload size for the
// direct send-image endpoint. 20 MiB covers any reasonable screenshot or
// AI-generated image while bounding memory use per request.
const maxDirectSendImageBytes = 20 << 20

// maxDirectSendImageRequestBytes bounds the raw request body before JSON
// decode. A 20 MiB image base64-encodes to ~26.67 MiB; the extra headroom
// covers JSON overhead and the other fields.
const maxDirectSendImageRequestBytes = 32 << 20

// handleImbridgeDirectSendImage sends an image to an IM user without a
// sandbox binding. Parallel to handleImbridgeDirectSend but carries
// base64-encoded image bytes. Auth via INTERNAL_API_SECRET.
func (s *Server) handleImbridgeDirectSendImage(w http.ResponseWriter, r *http.Request) {
	if secret := os.Getenv("INTERNAL_API_SECRET"); secret != "" {
		if r.Header.Get("X-Internal-Secret") != secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Cap the raw body before JSON decode so we never buffer an unbounded
	// request. MaxBytesReader returns an error from subsequent Reads once
	// the limit is exceeded, which Decode will surface.
	r.Body = http.MaxBytesReader(w, r.Body, maxDirectSendImageRequestBytes)

	var req struct {
		ChannelID   string `json:"channel_id"`
		ToUserID    string `json:"to_user_id"`
		ImageBase64 string `json:"image_base64"`
		Format      string `json:"format,omitempty"`
		Caption     string `json:"caption,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid or oversized request body", http.StatusBadRequest)
		return
	}
	if req.ChannelID == "" || req.ToUserID == "" || req.ImageBase64 == "" {
		http.Error(w, "channel_id, to_user_id, and image_base64 are required", http.StatusBadRequest)
		return
	}

	data, err := base64.StdEncoding.DecodeString(req.ImageBase64)
	if err != nil {
		http.Error(w, "invalid image_base64: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(data) > maxDirectSendImageBytes {
		http.Error(w, "image exceeds 20 MiB limit", http.StatusRequestEntityTooLarge)
		return
	}

	channel, err := s.db.GetIMChannel(req.ChannelID)
	if err != nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}

	provider := s.bridge.GetProvider(channel.Provider)
	if provider == nil {
		http.Error(w, "unknown IM provider: "+channel.Provider, http.StatusBadRequest)
		return
	}

	isp, ok := provider.(imbridge.ImageSendProvider)
	if !ok {
		http.Error(w, "image sending not supported for provider: "+provider.Name(),
			http.StatusNotImplemented)
		return
	}

	s.bridge.StopTyping(channel.ID, req.ToUserID)

	creds := &imbridge.Credentials{
		ChannelID: channel.ID,
		BotID:     channel.BotID,
		BotToken:  channel.BotToken,
		BaseURL:   channel.BaseURL,
	}

	meta, _ := s.db.GetAllChannelMeta(channel.ID, req.ToUserID)
	if err := isp.SendImage(r.Context(), creds, req.ToUserID, data, req.Caption, meta); err != nil {
		log.Printf("imbridge direct send-image: failed channel=%s provider=%s to=%s: %v",
			channel.ID, provider.Name(), req.ToUserID, err)
		http.Error(w, "failed to send image", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "sent"})
}

// ---------------------------------------------------------------------------
// Legacy sandbox-level WeChat QR login
// ---------------------------------------------------------------------------

func (s *Server) handleIMWeixinQRStart(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sbx, ok := s.sandboxes.Get(id)
	if !ok {
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	}
	if _, ok := s.requireWorkspaceMember(w, r, sbx.WorkspaceID); !ok {
		return
	}
	if sbx.Type != "openclaw" {
		http.Error(w, "weixin login is only available for openclaw sandboxes", http.StatusBadRequest)
		return
	}
	if sbx.Status != "running" {
		http.Error(w, "sandbox is not running", http.StatusConflict)
		return
	}

	wp := s.bridge.GetProvider("weixin").(*imbridge.WeixinProvider)
	session, err := wp.StartQRLogin(r.Context())
	if err != nil {
		log.Printf("weixin qr-start: %v", err)
		http.Error(w, "failed to start weixin login", http.StatusBadGateway)
		return
	}
	wp.SetSession(id, session)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"qrcode_url": session.QRCodeURL,
		"message":    "Scan the QR code with WeChat",
	})
}

func (s *Server) handleIMWeixinQRWait(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sbx, ok := s.sandboxes.Get(id)
	if !ok {
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	}
	if _, ok := s.requireWorkspaceMember(w, r, sbx.WorkspaceID); !ok {
		return
	}
	if sbx.Type != "openclaw" {
		http.Error(w, "weixin login is only available for openclaw sandboxes", http.StatusBadRequest)
		return
	}
	if sbx.Status != "running" {
		http.Error(w, "sandbox is not running", http.StatusConflict)
		return
	}

	wp := s.bridge.GetProvider("weixin").(*imbridge.WeixinProvider)
	session := wp.GetSession(id)
	if session == nil {
		http.Error(w, "no active weixin login session", http.StatusBadRequest)
		return
	}

	result, err := wp.PollQRLogin(r.Context(), session)
	if err != nil {
		log.Printf("weixin qr-wait: poll error: %v", err)
		http.Error(w, "poll failed", http.StatusBadGateway)
		return
	}

	switch result.Status {
	case "confirmed":
		if wp.TakeSession(id) == nil {
			http.Error(w, "login already processed", http.StatusConflict)
			return
		}
		if err := s.saveWeixinCredentials(r.Context(), id, result, wp); err != nil {
			log.Printf("weixin qr-wait: save credentials: %v", err)
			http.Error(w, "login succeeded but failed to save credentials", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": true,
			"status":    "confirmed",
			"message":   "WeChat connected successfully",
			"bot_id":    normalizeAccountID(result.BotID),
			"user_id":   result.UserID,
		})

	case "expired":
		newSession, err := wp.StartQRLogin(r.Context())
		if err != nil {
			wp.ClearSession(id)
			http.Error(w, "QR code expired and refresh failed", http.StatusBadGateway)
			return
		}
		wp.SetSession(id, newSession)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected":  false,
			"status":     "expired",
			"message":    "QR code expired, new code generated",
			"qrcode_url": newSession.QRCodeURL,
		})

	case "scaned_but_redirect":
		// IDC migration: subsequent polls must target the new host.
		if result.RedirectHost != "" {
			session.CurrentAPIBaseURL = "https://" + result.RedirectHost
			wp.SetSession(id, session)
			log.Printf("weixin qr-wait: IDC redirect, switching polling host to %s", result.RedirectHost)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": false,
			"status":    "scaned",
			"message":   statusMessage("scaned"),
		})

	case "binded_redirect":
		wp.TakeSession(id)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected":        false,
			"status":           "binded_redirect",
			"already_connected": true,
			"message":          "已连接过此实例，无需重复连接",
		})

	case "verify_code_blocked":
		wp.ClearSession(id)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": false,
			"status":    "verify_code_blocked",
			"message":   "配对码错误次数过多，请稍后再试",
		})

	default:
		// Includes "wait", "scaned", "need_verifycode" (logged-and-waited),
		// and any unknown future status.
		if result.Status == "need_verifycode" {
			log.Printf("weixin qr-wait: need_verifycode received (interactive pair-code not supported yet, sandbox=%s)", id)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": false,
			"status":    result.Status,
			"message":   statusMessage(result.Status),
		})
	}
}

func statusMessage(status string) string {
	switch status {
	case "scaned":
		return "QR code scanned, confirm on WeChat"
	default:
		return "Waiting for QR code scan"
	}
}

func (s *Server) saveWeixinCredentials(ctx context.Context, sandboxID string, result *weixin.StatusResult, wp *imbridge.WeixinProvider) error {
	accountID := normalizeAccountID(result.BotID)
	if accountID == "" {
		return fmt.Errorf("empty bot ID from ilink response")
	}

	if _, ok := s.sandboxes.Get(sandboxID); !ok {
		return fmt.Errorf("sandbox %s not found", sandboxID)
	}

	// Openclaw: the standalone imbridge service does not have K8s exec access,
	// so openclaw credential injection is not supported. Store the binding
	// record and credentials in DB for the agentserver to pick up.
	baseURL := result.BaseURL
	if baseURL == "" {
		baseURL = wp.DefaultBaseURL()
	}
	if dbErr := s.db.CreateIMBinding(sandboxID, "weixin", accountID, result.UserID); dbErr != nil {
		log.Printf("weixin: failed to save binding record: %v", dbErr)
	}
	if dbErr := s.db.SaveIMCredentials(sandboxID, "weixin", accountID, result.Token, baseURL); dbErr != nil {
		log.Printf("weixin: failed to save bot credentials for openclaw: %v", dbErr)
	}
	return nil
}

func normalizeAccountID(raw string) string {
	var out []byte
	for _, c := range []byte(raw) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return string(out)
}

// ---------------------------------------------------------------------------
// Legacy sandbox-level Telegram
// ---------------------------------------------------------------------------

func (s *Server) handleIMTelegramConfigure(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sbx, ok := s.sandboxes.Get(id)
	if !ok {
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	}
	if _, ok := s.requireWorkspaceMember(w, r, sbx.WorkspaceID); !ok {
		return
	}
	if !telegramBindAllowedType(sbx.Type) {
		http.Error(w, telegramBindingSandboxTypeMsg, http.StatusBadRequest)
		return
	}
	if sbx.Status != "running" {
		http.Error(w, "sandbox is not running", http.StatusConflict)
		return
	}

	var req struct {
		BotToken string `json:"bot_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.BotToken == "" {
		http.Error(w, "bot_token is required", http.StatusBadRequest)
		return
	}

	provider := s.bridge.GetProvider("telegram")
	cp, ok := provider.(imbridge.ConfigurableProvider)
	if !ok {
		http.Error(w, "telegram provider does not support configuration", http.StatusInternalServerError)
		return
	}
	botID, err := cp.ValidateCredentials(r.Context(), "", req.BotToken)
	if err != nil {
		log.Printf("telegram configure: validate failed: %v", err)
		http.Error(w, "invalid bot token: "+err.Error(), http.StatusBadRequest)
		return
	}

	type defaulter interface{ DefaultBaseURL() string }
	tgBaseURL := ""
	if d, ok := provider.(defaulter); ok {
		tgBaseURL = d.DefaultBaseURL()
	}

	channelID, err := s.db.CreateIMChannel(sbx.WorkspaceID, "telegram", botID, "")
	if err != nil {
		log.Printf("telegram configure: create channel: %v", err)
		http.Error(w, "failed to save channel", http.StatusInternalServerError)
		return
	}
	if err := s.db.SaveIMChannelCredentials(channelID, req.BotToken, tgBaseURL); err != nil {
		log.Printf("telegram configure: save credentials: %v", err)
		http.Error(w, "failed to save credentials", http.StatusInternalServerError)
		return
	}
	if err := s.db.BindSandboxToChannel(id, channelID); err != nil {
		log.Printf("telegram configure: bind sandbox: %v", err)
		http.Error(w, "failed to bind sandbox", http.StatusInternalServerError)
		return
	}

	s.bridge.StartPoller(imbridge.BridgeBinding{
		Provider: provider,
		Credentials: imbridge.Credentials{
			ChannelID: channelID,
			BotID:     botID,
			BotToken:  req.BotToken,
			BaseURL:   tgBaseURL,
		},
		ChannelID:   channelID,
		Cursor:      "",
		WorkspaceID: sbx.WorkspaceID,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"connected": true,
		"bot_id":    botID,
	})
}

func (s *Server) handleIMTelegramDisconnect(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sbx, ok := s.sandboxes.Get(id)
	if !ok {
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	}
	if _, ok := s.requireWorkspaceMember(w, r, sbx.WorkspaceID); !ok {
		return
	}

	ch, err := s.db.GetIMChannelForSandbox(id)
	if err != nil || ch.Provider != "telegram" {
		http.Error(w, "no telegram binding found for this sandbox", http.StatusNotFound)
		return
	}
	s.bridge.StopPoller(ch.ID)
	_ = s.db.UnbindSandboxFromChannel(id)
	if err := s.db.DeleteIMChannel(ch.ID); err != nil {
		log.Printf("telegram disconnect: delete channel %s: %v", ch.ID, err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "disconnected"})
}

// ---------------------------------------------------------------------------
// Legacy sandbox-level Matrix
// ---------------------------------------------------------------------------

func (s *Server) handleIMMatrixConfigure(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sbx, ok := s.sandboxes.Get(id)
	if !ok {
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	}
	if _, ok := s.requireWorkspaceMember(w, r, sbx.WorkspaceID); !ok {
		return
	}
	if sbx.Type != "nanoclaw" {
		http.Error(w, "matrix binding is only available for nanoclaw sandboxes", http.StatusBadRequest)
		return
	}
	if sbx.Status != "running" {
		http.Error(w, "sandbox is not running", http.StatusConflict)
		return
	}

	var req struct {
		HomeserverURL string `json:"homeserver_url"`
		AccessToken   string `json:"access_token"`
		RecoveryKey   string `json:"recovery_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.HomeserverURL == "" {
		http.Error(w, "homeserver_url is required", http.StatusBadRequest)
		return
	}
	if req.AccessToken == "" {
		http.Error(w, "access_token is required", http.StatusBadRequest)
		return
	}

	provider := s.bridge.GetProvider("matrix")
	cp, ok := provider.(imbridge.ConfigurableProvider)
	if !ok {
		http.Error(w, "matrix provider does not support configuration", http.StatusInternalServerError)
		return
	}
	botID, err := cp.ValidateCredentials(r.Context(), req.HomeserverURL, req.AccessToken)
	if err != nil {
		log.Printf("matrix configure: validate failed: %v", err)
		http.Error(w, "invalid credentials: "+err.Error(), http.StatusBadRequest)
		return
	}

	channelID, err := s.db.CreateIMChannel(sbx.WorkspaceID, "matrix", botID, "")
	if err != nil {
		log.Printf("matrix configure: create channel: %v", err)
		http.Error(w, "failed to save channel", http.StatusInternalServerError)
		return
	}
	if err := s.db.SaveIMChannelCredentials(channelID, req.AccessToken, req.HomeserverURL); err != nil {
		log.Printf("matrix configure: save credentials: %v", err)
		http.Error(w, "failed to save credentials", http.StatusInternalServerError)
		return
	}
	if err := s.db.BindSandboxToChannel(id, channelID); err != nil {
		log.Printf("matrix configure: bind sandbox: %v", err)
		http.Error(w, "failed to bind sandbox", http.StatusInternalServerError)
		return
	}

	type e2eeConfigurer interface {
		ConfigureE2EE(ctx context.Context, creds *imbridge.Credentials, recoveryKey string) error
	}
	if ec, ok := provider.(e2eeConfigurer); ok && req.RecoveryKey != "" {
		creds := imbridge.Credentials{ChannelID: channelID, BotID: botID, BotToken: req.AccessToken, BaseURL: req.HomeserverURL}
		if err := ec.ConfigureE2EE(r.Context(), &creds, req.RecoveryKey); err != nil {
			log.Printf("matrix configure: E2EE init failed: %v", err)
		}
	}

	s.bridge.StartPoller(imbridge.BridgeBinding{
		Provider: provider,
		Credentials: imbridge.Credentials{
			ChannelID: channelID,
			BotID:     botID,
			BotToken:  req.AccessToken,
			BaseURL:   req.HomeserverURL,
		},
		ChannelID:   channelID,
		Cursor:      "",
		WorkspaceID: sbx.WorkspaceID,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"connected": true,
		"bot_id":    botID,
	})
}

func (s *Server) handleIMMatrixDisconnect(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sbx, ok := s.sandboxes.Get(id)
	if !ok {
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	}
	if _, ok := s.requireWorkspaceMember(w, r, sbx.WorkspaceID); !ok {
		return
	}

	ch, err := s.db.GetIMChannelForSandbox(id)
	if err != nil || ch.Provider != "matrix" {
		http.Error(w, "no matrix binding found for this sandbox", http.StatusNotFound)
		return
	}
	s.bridge.StopPoller(ch.ID)
	provider := s.bridge.GetProvider("matrix")
	if dp, ok := provider.(imbridge.DisconnectProvider); ok {
		dp.Disconnect(id, ch.BotID)
	}
	_ = s.db.UnbindSandboxFromChannel(id)
	if err := s.db.DeleteIMChannel(ch.ID); err != nil {
		log.Printf("matrix disconnect: delete channel %s: %v", ch.ID, err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "disconnected"})
}

// ---------------------------------------------------------------------------
// Sandbox IM bindings
// ---------------------------------------------------------------------------

func (s *Server) handleListIMBindings(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sbx, ok := s.sandboxes.Get(id)
	if !ok {
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	}
	if _, ok := s.requireWorkspaceMember(w, r, sbx.WorkspaceID); !ok {
		return
	}

	var resp []imBindingResponse
	ch, err := s.db.GetIMChannelForSandbox(id)
	if err == nil {
		resp = append(resp, imBindingResponse{
			Provider: ch.Provider,
			BotID:    ch.BotID,
			UserID:   ch.UserID,
			BoundAt:  ch.BoundAt.Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"bindings": resp})
}

func (s *Server) handleBindSandboxToChannel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sbx, ok := s.sandboxes.Get(id)
	if !ok {
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	}
	if _, ok := s.requireWorkspaceMember(w, r, sbx.WorkspaceID); !ok {
		return
	}

	var req struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}

	ch, err := s.db.GetIMChannel(req.ChannelID)
	if err != nil || ch.WorkspaceID != sbx.WorkspaceID {
		http.Error(w, "channel not found in this workspace", http.StatusNotFound)
		return
	}

	if err := s.db.BindSandboxToChannel(id, req.ChannelID); err != nil {
		http.Error(w, "failed to bind channel", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "bound"})
}

func (s *Server) handleUnbindSandboxFromChannel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sbx, ok := s.sandboxes.Get(id)
	if !ok {
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	}
	if _, ok := s.requireWorkspaceMember(w, r, sbx.WorkspaceID); !ok {
		return
	}

	if err := s.db.UnbindSandboxFromChannel(id); err != nil {
		http.Error(w, "failed to unbind channel", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "unbound"})
}

// ---------------------------------------------------------------------------
// Workspace-level IM channel management
// ---------------------------------------------------------------------------

func (s *Server) handleListWorkspaceIMChannels(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}

	channels, err := s.db.ListIMChannels(wsID)
	if err != nil {
		http.Error(w, "failed to list channels", http.StatusInternalServerError)
		return
	}

	type channelResp struct {
		ID             string `json:"id"`
		Provider       string `json:"provider"`
		BotID          string `json:"bot_id"`
		UserID         string `json:"user_id,omitempty"`
		RequireMention bool   `json:"require_mention"`
		RoutingMode    string `json:"routing_mode"`
		BoundAt        string `json:"bound_at"`
	}
	resp := make([]channelResp, 0, len(channels))
	for _, ch := range channels {
		resp = append(resp, channelResp{
			ID:             ch.ID,
			Provider:       ch.Provider,
			BotID:          ch.BotID,
			UserID:         ch.UserID,
			RequireMention: ch.RequireMention,
			RoutingMode:    ch.RoutingMode,
			BoundAt:        ch.BoundAt.Format(time.RFC3339),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"channels": resp})
}

func (s *Server) handleDeleteWorkspaceIMChannel(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	channelID := chi.URLParam(r, "channelId")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}

	ch, err := s.db.GetIMChannel(channelID)
	if err != nil || ch.WorkspaceID != wsID {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}

	s.bridge.StopPoller(channelID)
	provider := s.bridge.GetProvider(ch.Provider)
	if dp, ok := provider.(imbridge.DisconnectProvider); ok {
		dp.Disconnect("", ch.BotID)
	}
	if err := s.db.DeleteIMChannel(channelID); err != nil {
		log.Printf("delete im channel: %v", err)
		http.Error(w, "failed to delete channel", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUpdateWorkspaceIMChannel(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	channelID := chi.URLParam(r, "channelId")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}

	ch, err := s.db.GetIMChannel(channelID)
	if err != nil || ch.WorkspaceID != wsID {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}

	var req struct {
		RequireMention *bool   `json:"require_mention"`
		RoutingMode    *string `json:"routing_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.RequireMention != nil {
		if err := s.db.UpdateIMChannelSettings(channelID, *req.RequireMention); err != nil {
			http.Error(w, "failed to update channel", http.StatusInternalServerError)
			return
		}
		s.bridge.SetChannelRequireMention(channelID, *req.RequireMention)
	}

	if req.RoutingMode != nil {
		mode := *req.RoutingMode
		// stateless_cc is no longer accepted — the agentserver endpoint
		// it pointed to (POST /api/workspaces/{id}/im/inbound) was
		// removed in the #135 purge.
		if mode != "codex" && mode != "openclaw" {
			http.Error(w, "invalid routing_mode: must be codex or openclaw", http.StatusBadRequest)
			return
		}
		if err := s.db.UpdateIMChannelRoutingMode(channelID, mode); err != nil {
			http.Error(w, "failed to update channel", http.StatusInternalServerError)
			return
		}
		s.bridge.SetChannelRoutingMode(channelID, mode)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "updated"})
}

// ---------------------------------------------------------------------------
// Workspace-level WeChat
// ---------------------------------------------------------------------------

func (s *Server) handleWorkspaceWeixinQRStart(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}

	wp := s.bridge.GetProvider("weixin").(*imbridge.WeixinProvider)
	session, err := wp.StartQRLogin(r.Context())
	if err != nil {
		log.Printf("weixin qr-start: %v", err)
		http.Error(w, "failed to start weixin login", http.StatusBadGateway)
		return
	}
	wp.SetSession(wsID, session)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"qrcode_url": session.QRCodeURL,
		"message":    "Scan the QR code with WeChat",
	})
}

func (s *Server) handleWorkspaceWeixinQRWait(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}

	wp := s.bridge.GetProvider("weixin").(*imbridge.WeixinProvider)
	session := wp.GetSession(wsID)
	if session == nil {
		http.Error(w, "no active weixin login session", http.StatusBadRequest)
		return
	}

	result, err := wp.PollQRLogin(r.Context(), session)
	if err != nil {
		log.Printf("weixin qr-wait: poll error: %v", err)
		http.Error(w, "poll failed", http.StatusBadGateway)
		return
	}

	switch result.Status {
	case "confirmed":
		if wp.TakeSession(wsID) == nil {
			http.Error(w, "login already processed", http.StatusConflict)
			return
		}

		accountID := normalizeAccountID(result.BotID)
		if accountID == "" {
			http.Error(w, "empty bot ID", http.StatusInternalServerError)
			return
		}
		baseURL := result.BaseURL
		if baseURL == "" {
			baseURL = wp.DefaultBaseURL()
		}

		channelID, err := s.db.CreateIMChannel(wsID, "weixin", accountID, result.UserID)
		if err != nil {
			http.Error(w, "failed to save channel", http.StatusInternalServerError)
			return
		}
		if err := s.db.SaveIMChannelCredentials(channelID, result.Token, baseURL); err != nil {
			http.Error(w, "failed to save credentials", http.StatusInternalServerError)
			return
		}

		provider := s.bridge.GetProvider("weixin")
		s.bridge.StartPoller(imbridge.BridgeBinding{
			Provider:    provider,
			Credentials: imbridge.Credentials{ChannelID: channelID, BotID: accountID, BotToken: result.Token, BaseURL: baseURL},
			ChannelID:   channelID,
			WorkspaceID: wsID,
		})

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": true,
			"status":    "confirmed",
			"bot_id":    accountID,
		})

	case "expired":
		newSession, err := wp.StartQRLogin(r.Context())
		if err != nil {
			wp.ClearSession(wsID)
			http.Error(w, "QR code expired and refresh failed", http.StatusBadGateway)
			return
		}
		wp.SetSession(wsID, newSession)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected":  false,
			"status":     "expired",
			"qrcode_url": newSession.QRCodeURL,
		})

	case "scaned_but_redirect":
		if result.RedirectHost != "" {
			session.CurrentAPIBaseURL = "https://" + result.RedirectHost
			wp.SetSession(wsID, session)
			log.Printf("weixin qr-wait: IDC redirect, switching polling host to %s", result.RedirectHost)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": false,
			"status":    "scaned",
		})

	case "binded_redirect":
		wp.TakeSession(wsID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected":         false,
			"status":            "binded_redirect",
			"already_connected": true,
			"message":           "已连接过此实例，无需重复连接",
		})

	case "verify_code_blocked":
		wp.ClearSession(wsID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": false,
			"status":    "verify_code_blocked",
			"message":   "配对码错误次数过多，请稍后再试",
		})

	default:
		if result.Status == "need_verifycode" {
			log.Printf("weixin qr-wait: need_verifycode received (interactive pair-code not supported yet, workspace=%s)", wsID)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"connected": false,
			"status":    result.Status,
		})
	}
}

// ---------------------------------------------------------------------------
// Workspace-level Telegram
// ---------------------------------------------------------------------------

func (s *Server) handleWorkspaceTelegramConfigure(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}

	var req struct {
		BotToken string `json:"bot_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.BotToken == "" {
		http.Error(w, "bot_token is required", http.StatusBadRequest)
		return
	}

	provider := s.bridge.GetProvider("telegram")
	cp, ok := provider.(imbridge.ConfigurableProvider)
	if !ok {
		http.Error(w, "telegram provider does not support configuration", http.StatusInternalServerError)
		return
	}
	botID, err := cp.ValidateCredentials(r.Context(), "", req.BotToken)
	if err != nil {
		http.Error(w, "invalid bot token: "+err.Error(), http.StatusBadRequest)
		return
	}

	type defaulter interface{ DefaultBaseURL() string }
	baseURL := ""
	if d, ok := provider.(defaulter); ok {
		baseURL = d.DefaultBaseURL()
	}

	channelID, err := s.db.CreateIMChannel(wsID, "telegram", botID, "")
	if err != nil {
		http.Error(w, "failed to save channel", http.StatusInternalServerError)
		return
	}
	if err := s.db.SaveIMChannelCredentials(channelID, req.BotToken, baseURL); err != nil {
		http.Error(w, "failed to save credentials", http.StatusInternalServerError)
		return
	}

	s.bridge.StartPoller(imbridge.BridgeBinding{
		Provider:    provider,
		Credentials: imbridge.Credentials{ChannelID: channelID, BotID: botID, BotToken: req.BotToken, BaseURL: baseURL},
		ChannelID:   channelID,
		WorkspaceID: wsID,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"connected": true, "bot_id": botID})
}

// ---------------------------------------------------------------------------
// Workspace-level Matrix
// ---------------------------------------------------------------------------

func (s *Server) handleWorkspaceMatrixConfigure(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}

	var req struct {
		HomeserverURL string `json:"homeserver_url"`
		AccessToken   string `json:"access_token"`
		RecoveryKey   string `json:"recovery_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.HomeserverURL == "" || req.AccessToken == "" {
		http.Error(w, "homeserver_url and access_token are required", http.StatusBadRequest)
		return
	}

	provider := s.bridge.GetProvider("matrix")
	cp, ok := provider.(imbridge.ConfigurableProvider)
	if !ok {
		http.Error(w, "matrix provider does not support configuration", http.StatusInternalServerError)
		return
	}
	botID, err := cp.ValidateCredentials(r.Context(), req.HomeserverURL, req.AccessToken)
	if err != nil {
		http.Error(w, "invalid credentials: "+err.Error(), http.StatusBadRequest)
		return
	}

	channelID, err := s.db.CreateIMChannel(wsID, "matrix", botID, "")
	if err != nil {
		http.Error(w, "failed to save channel", http.StatusInternalServerError)
		return
	}
	if err := s.db.SaveIMChannelCredentials(channelID, req.AccessToken, req.HomeserverURL); err != nil {
		http.Error(w, "failed to save credentials", http.StatusInternalServerError)
		return
	}

	type e2eeConfigurer interface {
		ConfigureE2EE(ctx context.Context, creds *imbridge.Credentials, recoveryKey string) error
	}
	if ec, ok := provider.(e2eeConfigurer); ok && req.RecoveryKey != "" {
		creds := imbridge.Credentials{ChannelID: channelID, BotID: botID, BotToken: req.AccessToken, BaseURL: req.HomeserverURL}
		if err := ec.ConfigureE2EE(r.Context(), &creds, req.RecoveryKey); err != nil {
			log.Printf("matrix configure: E2EE init failed: %v", err)
		}
	}

	s.bridge.StartPoller(imbridge.BridgeBinding{
		Provider:    provider,
		Credentials: imbridge.Credentials{ChannelID: channelID, BotID: botID, BotToken: req.AccessToken, BaseURL: req.HomeserverURL},
		ChannelID:   channelID,
		WorkspaceID: wsID,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"connected": true, "bot_id": botID})
}

// ---------------------------------------------------------------------------
// Multi-channel routing (N:M) — strategy + bind-multi
// ---------------------------------------------------------------------------

// handleGetWorkspaceRoutingStrategy returns the channel_routing_strategy
// stored on the workspace. Defaults to "shared" for legacy rows.
func (s *Server) handleGetWorkspaceRoutingStrategy(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}

	ws, err := s.db.GetWorkspace(wsID)
	if err != nil || ws == nil {
		http.Error(w, "workspace not found", http.StatusNotFound)
		return
	}

	strategy := ws.ChannelRoutingStrategy
	if strategy == "" {
		strategy = "shared"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"strategy": strategy})
}

// handleUpdateWorkspaceRoutingStrategy sets the channel_routing_strategy
// to one of shared/per_agent/hybrid. Caller must be a workspace member.
func (s *Server) handleUpdateWorkspaceRoutingStrategy(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}

	var req struct {
		Strategy string `json:"strategy"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := s.db.UpdateWorkspaceRoutingStrategy(wsID, req.Strategy); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"strategy": req.Strategy})
}

// handleBindSandboxChannelsMulti binds N channels to a single sandbox
// without displacing other sandboxes already holding those channels.
// Body: {"channel_ids": ["ch1","ch2", ...]}.
//
// Every channel must belong to the same workspace as the sandbox; mixed
// requests are rejected with 400.
func (s *Server) handleBindSandboxChannelsMulti(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sbx, ok := s.sandboxes.Get(id)
	if !ok {
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	}
	if _, ok := s.requireWorkspaceMember(w, r, sbx.WorkspaceID); !ok {
		return
	}

	var req struct {
		ChannelIDs []string `json:"channel_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.ChannelIDs) == 0 {
		http.Error(w, "channel_ids is required", http.StatusBadRequest)
		return
	}

	for _, channelID := range req.ChannelIDs {
		ch, err := s.db.GetIMChannel(channelID)
		if err != nil || ch.WorkspaceID != sbx.WorkspaceID {
			http.Error(w, fmt.Sprintf("channel %s not found in this workspace", channelID), http.StatusBadRequest)
			return
		}
	}

	if err := s.db.BindSandboxChannels(id, req.ChannelIDs); err != nil {
		http.Error(w, "failed to bind channels", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":      "bound",
		"channel_ids": req.ChannelIDs,
	})
}

// ---------------------------------------------------------------------------
// WhatsApp Cloud (Meta) — configure + webhook (push-based, not poll-based)
// ---------------------------------------------------------------------------

// whatsappWebhookVerifyToken returns the shared secret expected from
// Meta in the initial webhook subscription handshake (hub.verify_token).
// Configured via WHATSAPP_WEBHOOK_VERIFY_TOKEN; falls back to "" which
// disables verification — useful only for local dev.
func whatsappWebhookVerifyToken() string {
	return os.Getenv("WHATSAPP_WEBHOOK_VERIFY_TOKEN")
}

// generateVerifyToken creates a 32-byte random hex string suitable as
// a per-workspace Meta webhook verify_token.
func generateVerifyToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// whatsappAppSecret returns the Meta App Secret used to sign every
// webhook delivery via X-Hub-Signature-256. Configured via
// WHATSAPP_APP_SECRET; empty value disables signature verification
// (dev only — production deploys MUST set this).
func whatsappAppSecret() string {
	return os.Getenv("WHATSAPP_APP_SECRET")
}

// whatsappHMACRequired reports whether the deployment refuses to
// process any webhook delivery while WHATSAPP_APP_SECRET is empty.
// Set via WHATSAPP_HMAC_REQUIRED=true in prod values; default false
// preserves dev-mode opt-in HMAC behavior. improvements.md #13.
func whatsappHMACRequired() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("WHATSAPP_HMAC_REQUIRED")))
	return v == "1" || v == "true" || v == "yes"
}

// LogWhatsAppHMACMode emits a single startup line describing the
// current verification posture. Called from cmd/imbridge/main.go so
// operators can grep boot logs for misconfig.
func LogWhatsAppHMACMode() {
	required := whatsappHMACRequired()
	hasSecret := whatsappAppSecret() != ""
	switch {
	case required && hasSecret:
		log.Printf("WhatsApp HMAC verification: REQUIRED (X-Hub-Signature-256 enforced; rejecting unsigned deliveries)")
	case required && !hasSecret:
		log.Printf("WhatsApp HMAC verification: REQUIRED but WHATSAPP_APP_SECRET is empty — webhook handler will return 503 until the secret is provided")
	case !required && hasSecret:
		log.Printf("WhatsApp HMAC verification: OPTIONAL with secret set (deliveries verified when X-Hub-Signature-256 header present)")
	default:
		log.Printf("WhatsApp HMAC verification: OPTIONAL (dev mode) — no signature verification")
	}
}

// verifyWhatsAppSignature returns true when the X-Hub-Signature-256
// header matches an HMAC-SHA256 of the raw body computed with the
// configured app secret. Constant-time comparison; rejects malformed
// or missing headers.
//
// When the app secret is empty, returns true unconditionally — caller
// is expected to log a startup warning so prod misconfig is visible.
func verifyWhatsAppSignature(header string, body []byte, appSecret string) bool {
	if appSecret == "" {
		return true
	}
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	sigHex := header[len(prefix):]
	wantBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), wantBytes)
}

// handleWorkspaceWhatsAppConfigure binds a WhatsApp Business phone
// number to a workspace. Unlike Telegram/WeChat/Matrix this does not
// start a poller — WhatsApp Cloud is push-based, see the webhook
// handlers below.
//
// Body: {"phone_number_id": "...", "access_token": "...", "base_url": "..." (optional)}
func (s *Server) handleWorkspaceWhatsAppConfigure(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	if _, ok := s.requireWorkspaceMember(w, r, wsID); !ok {
		return
	}

	var req struct {
		PhoneNumberID string `json:"phone_number_id"`
		AccessToken   string `json:"access_token"`
		BaseURL       string `json:"base_url,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PhoneNumberID == "" || req.AccessToken == "" {
		http.Error(w, "phone_number_id and access_token are required", http.StatusBadRequest)
		return
	}
	baseURL := strings.TrimRight(req.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://graph.facebook.com/v18.0"
	}

	channelID, err := s.db.CreateIMChannel(wsID, "whatsapp", req.PhoneNumberID, "")
	if err != nil {
		log.Printf("create whatsapp channel: %v", err)
		http.Error(w, "failed to create channel", http.StatusInternalServerError)
		return
	}
	if err := s.db.SaveIMChannelCredentials(channelID, req.AccessToken, baseURL); err != nil {
		log.Printf("save whatsapp credentials: %v", err)
		http.Error(w, "failed to save credentials", http.StatusInternalServerError)
		return
	}

	// Generate a per-workspace verify_token so each tenant's Meta App
	// webhook handshake is isolated — no shared global env-var token.
	verifyToken, err := generateVerifyToken()
	if err != nil {
		log.Printf("generate verify_token: %v", err)
		http.Error(w, "failed to generate verify token", http.StatusInternalServerError)
		return
	}
	if err := s.db.SetIMChannelVerifyToken(channelID, verifyToken); err != nil {
		log.Printf("save verify_token: %v", err)
		http.Error(w, "failed to save verify token", http.StatusInternalServerError)
		return
	}

	host := r.Host
	if host == "" {
		host = os.Getenv("PLATFORM_DOMAIN")
	}
	webhookURL := fmt.Sprintf("https://%s/webhook/whatsapp/%s", host, wsID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"connected":    true,
		"channel_id":   channelID,
		"bot_id":       req.PhoneNumberID,
		"webhook_url":  webhookURL,
		"verify_token": verifyToken,
	})
}

// handleWhatsAppWebhookVerify implements Meta's webhook handshake.
// Meta sends GET with hub.mode=subscribe, hub.verify_token=<your token>,
// hub.challenge=<random string>. We must return the challenge as plain
// text iff the verify_token matches our configured secret.
func (s *Server) handleWhatsAppWebhookVerify(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")
	expected := whatsappWebhookVerifyToken()

	if mode != "subscribe" || expected == "" || token != expected {
		http.Error(w, "verification failed", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(challenge))
}

// handleWhatsAppWebhookVerifyPerWorkspace handles Meta's webhook handshake
// for a specific workspace. The verify_token is looked up from DB by
// (workspace_id, token) instead of a shared env-var, so each tenant's
// Meta App is independently registered.
//
// Route: GET /webhook/whatsapp/{workspace_id}
func (s *Server) handleWhatsAppWebhookVerifyPerWorkspace(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "workspace_id")
	mode := r.URL.Query().Get("hub.mode")
	token := r.URL.Query().Get("hub.verify_token")
	challenge := r.URL.Query().Get("hub.challenge")

	if mode != "subscribe" || token == "" {
		http.Error(w, "verification failed", http.StatusForbidden)
		return
	}
	_, err := s.db.GetIMChannelByWorkspaceAndToken(wsID, token)
	if err != nil {
		log.Printf("whatsapp webhook verify: no channel for workspace=%s token=<redacted>: %v", wsID, err)
		http.Error(w, "verification failed", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(challenge))
}

// handleWhatsAppWebhookInboundPerWorkspace processes Meta webhook deliveries
// for a specific workspace. workspace_id in the URL path scopes the
// channel lookup — messages from workspace A never reach workspace B.
//
// Route: POST /webhook/whatsapp/{workspace_id}
func (s *Server) handleWhatsAppWebhookInboundPerWorkspace(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "workspace_id")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	appSecret := whatsappAppSecret()
	if whatsappHMACRequired() && appSecret == "" {
		log.Printf("whatsapp webhook [%s]: HMAC required but secret empty — rejecting", wsID)
		http.Error(w, "WhatsApp HMAC required but server secret not configured", http.StatusServiceUnavailable)
		return
	}
	if appSecret != "" {
		if !verifyWhatsAppSignature(r.Header.Get("X-Hub-Signature-256"), body, appSecret) {
			log.Printf("whatsapp webhook [%s]: invalid X-Hub-Signature-256", wsID)
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var payload whatsappWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("whatsapp webhook [%s]: decode: %v", wsID, err)
		w.WriteHeader(http.StatusOK)
		return
	}

	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if change.Field != "messages" {
				continue
			}
			phoneNumberID := change.Value.Metadata.PhoneNumberID
			if phoneNumberID == "" {
				continue
			}
			// Scope lookup to this workspace — prevents cross-tenant routing.
			channel, err := s.db.FindIMChannelByWorkspaceAndBot(wsID, "whatsapp", phoneNumberID)
			if err != nil {
				log.Printf("whatsapp webhook [%s]: no channel for phone_number_id=%s: %v", wsID, phoneNumberID, err)
				continue
			}

			senderName := ""
			if len(change.Value.Contacts) > 0 {
				senderName = change.Value.Contacts[0].Profile.Name
			}

			for _, msg := range change.Value.Messages {
				if msg.Type != "text" || msg.Text.Body == "" {
					continue
				}
				inbound := imbridge.InboundMessage{
					FromUserID: msg.From + "@wa",
					SenderName: senderName,
					Text:       msg.Text.Body,
				}
				if _, err := s.bridge.DispatchInbound(r.Context(), channel.ID, inbound); err != nil {
					log.Printf("whatsapp webhook [%s]: dispatch channel=%s msg=%s: %v", wsID, channel.ID, msg.ID, err)
				}
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}

// whatsappWebhookPayload mirrors the subset of Meta's webhook payload
// we consume. The full schema is much larger (status updates, errors,
// reactions, etc.) but for MVP we only handle inbound text messages.
type whatsappWebhookPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		Changes []struct {
			Value struct {
				MessagingProduct string `json:"messaging_product"`
				Metadata         struct {
					PhoneNumberID      string `json:"phone_number_id"`
					DisplayPhoneNumber string `json:"display_phone_number"`
				} `json:"metadata"`
				Contacts []struct {
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
					WaID string `json:"wa_id"`
				} `json:"contacts"`
				Messages []struct {
					From      string `json:"from"`
					ID        string `json:"id"`
					Timestamp string `json:"timestamp"`
					Type      string `json:"type"`
					Text      struct {
						Body string `json:"body"`
					} `json:"text"`
				} `json:"messages"`
			} `json:"value"`
			Field string `json:"field"`
		} `json:"changes"`
	} `json:"entry"`
}

// handleWhatsAppWebhookInbound parses a Meta webhook delivery, resolves
// the workspace_im_channels row from each message's phone_number_id,
// and dispatches inbound messages into the same forward pipeline used
// by polling providers (Bridge.DispatchInbound).
//
// We always return 200 even on partial failures — Meta retries on any
// non-2xx for up to 24h, which is the wrong behaviour for one-off bugs.
func (s *Server) handleWhatsAppWebhookInbound(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// HMAC verification:
	// - When WHATSAPP_HMAC_REQUIRED=true and WHATSAPP_APP_SECRET is empty,
	//   we refuse the delivery with 503. This prevents prod from silently
	//   accepting unsigned webhooks if the secret env was forgotten.
	// - When the app secret is set, every delivery must carry a matching
	//   X-Hub-Signature-256 (mismatches return 401). improvements.md #13.
	appSecret := whatsappAppSecret()
	if whatsappHMACRequired() && appSecret == "" {
		log.Printf("whatsapp webhook: WHATSAPP_HMAC_REQUIRED=true but WHATSAPP_APP_SECRET empty — rejecting delivery")
		http.Error(w, "WhatsApp HMAC required but server secret not configured", http.StatusServiceUnavailable)
		return
	}
	if appSecret != "" {
		if !verifyWhatsAppSignature(r.Header.Get("X-Hub-Signature-256"), body, appSecret) {
			log.Printf("whatsapp webhook: invalid X-Hub-Signature-256 — possible spoofing")
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	var payload whatsappWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf("whatsapp webhook: decode: %v", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			if change.Field != "messages" {
				continue
			}
			phoneNumberID := change.Value.Metadata.PhoneNumberID
			if phoneNumberID == "" {
				continue
			}
			channel, err := s.db.FindIMChannelByProviderBot("whatsapp", phoneNumberID)
			if err != nil {
				log.Printf("whatsapp webhook: no channel for phone_number_id=%s: %v", phoneNumberID, err)
				continue
			}

			senderName := ""
			if len(change.Value.Contacts) > 0 {
				senderName = change.Value.Contacts[0].Profile.Name
			}

			for _, msg := range change.Value.Messages {
				if msg.Type != "text" || msg.Text.Body == "" {
					// MVP: text only. TODO: handle media, audio, location, reactions.
					continue
				}
				inbound := imbridge.InboundMessage{
					FromUserID: msg.From + "@wa",
					SenderName: senderName,
					Text:       msg.Text.Body,
				}
				if _, err := s.bridge.DispatchInbound(r.Context(), channel.ID, inbound); err != nil {
					log.Printf("whatsapp webhook: dispatch channel=%s msg=%s: %v", channel.ID, msg.ID, err)
				}
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}
