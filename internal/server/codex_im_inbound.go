package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// codexInboundHandler routes inbound WeChat messages destined for the
// codex routing path. POST /api/internal/imbridge/codex/turn body is:
//
//	{
//	  "channel_id": "ch-xxx",
//	  "workspace_id": "ws-xxx",
//	  "wechat_user_id": "wxid_xxx",
//	  "text": "..."
//	}
//
// Returns 202 immediately and processes the codex turn in a goroutine.
// Task 14 wraps this with a per-(channel,user) FIFO dispatcher; this
// task ships the bare path so end-to-end works for one in-flight
// request per user.
type codexInboundHandler struct {
	codex           codexCaller
	sessions        sessionStore
	imbridgeSendURL string
	internalSecret  string
}

type codexCaller interface {
	RunTurn(ctx context.Context, req CodexTurnRequest) (*CodexTurnResponse, error)
}

// sessionStore is what the handler needs from the DB. Defined as an
// interface so tests can inject fakes without a real *sql.DB. The
// production adapter (Task 15) wraps *db.DB.
type sessionStore interface {
	GetSessionByExternalID(ctx context.Context, workspaceID, externalID string) (sessionView, error)
	SetSessionCodexThreadID(ctx context.Context, sessionID string, threadID *string) error
}

// sessionView is the subset of agent_sessions fields the codex handler
// needs. Decoupled from db.AgentSession to keep test fakes small.
type sessionView struct {
	ID            string
	CodexThreadID *string
}

type codexInboundRequest struct {
	ChannelID    string `json:"channel_id"`
	WorkspaceID  string `json:"workspace_id"`
	WechatUserID string `json:"wechat_user_id"`
	WechatSender string `json:"wechat_sender_name,omitempty"`
	Text         string `json:"text"`
	QuotedText   string `json:"quoted_text,omitempty"`
	QuotedSender string `json:"quoted_sender,omitempty"`
}

func (h *codexInboundHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req codexInboundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.ChannelID == "" || req.WorkspaceID == "" || req.WechatUserID == "" {
		http.Error(w, "channel_id, workspace_id, wechat_user_id required", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"queued":true}`))
	go h.processTurn(context.Background(), req)
}

func (h *codexInboundHandler) processTurn(ctx context.Context, req codexInboundRequest) {
	externalID := req.WechatUserID + "@im.wechat"
	sess, err := h.sessions.GetSessionByExternalID(ctx, req.WorkspaceID, externalID)
	if err != nil {
		log.Printf("codex_im: resolve session: %v", err)
		h.sendError(ctx, req, "⚠️ 内部错误：找不到会话")
		return
	}

	params := buildCodexInput(req)
	cresp, err := h.codex.RunTurn(ctx, CodexTurnRequest{
		WorkspaceID: req.WorkspaceID,
		ThreadID:    sess.CodexThreadID,
		Params:      params,
	})
	if err != nil {
		log.Printf("codex_im: cxg call: %v", err)
		h.sendError(ctx, req, "⚠️ Codex 处理失败，请稍后重试")
		return
	}

	// Transport-layer failure.
	if cresp.Transport != nil {
		h.sendError(ctx, req, transportToUserMessage(cresp.Transport))
		return
	}

	// Persist thread id if new or changed.
	if cresp.ThreadID != "" && (sess.CodexThreadID == nil || *sess.CodexThreadID != cresp.ThreadID) {
		tid := cresp.ThreadID
		if err := h.sessions.SetSessionCodexThreadID(ctx, sess.ID, &tid); err != nil {
			log.Printf("codex_im: persist thread id: %v", err)
		}
	}

	// Decode turn.status / items / error.
	var turn struct {
		Status string            `json:"status"`
		Items  []json.RawMessage `json:"items"`
		Error  *struct {
			Message        string  `json:"message"`
			CodexErrorInfo *string `json:"codexErrorInfo,omitempty"`
		} `json:"error"`
	}
	if err := json.Unmarshal(cresp.Turn, &turn); err != nil {
		log.Printf("codex_im: decode turn: %v", err)
		h.sendError(ctx, req, "⚠️ Codex 返回格式异常")
		return
	}

	switch turn.Status {
	case "completed":
		text := lastAgentMessageText(turn.Items)
		if text == "" {
			h.sendError(ctx, req, "⚠️ Codex 没有返回文本内容")
			return
		}
		h.sendText(ctx, req, text)
	case "failed":
		if turn.Error != nil && turn.Error.CodexErrorInfo != nil {
			switch *turn.Error.CodexErrorInfo {
			case "contextWindowExceeded":
				_ = h.sessions.SetSessionCodexThreadID(ctx, sess.ID, nil)
				h.sendError(ctx, req, "⚠️ 上下文已满，请新开会话")
				return
			case "usageLimitExceeded":
				h.sendError(ctx, req, "⚠️ Codex 配额已用尽")
				return
			case "serverOverloaded":
				h.sendError(ctx, req, "⚠️ Codex 繁忙，请稍后重试")
				return
			}
		}
		// Heuristic: thread-not-found.
		msg := ""
		if turn.Error != nil {
			msg = turn.Error.Message
		}
		lo := strings.ToLower(msg)
		if strings.Contains(lo, "thread") && (strings.Contains(lo, "not found") || strings.Contains(lo, "unknown") || strings.Contains(lo, "missing")) {
			_ = h.sessions.SetSessionCodexThreadID(ctx, sess.ID, nil)
			h.sendError(ctx, req, "⚠️ 会话已重置，请重发消息")
			return
		}
		log.Printf("codex_im: turn failed: %s", msg)
		h.sendError(ctx, req, "⚠️ Codex 处理失败")
	case "interrupted":
		h.sendError(ctx, req, "⚠️ 处理已取消，请重发")
	default:
		log.Printf("codex_im: unexpected status %q", turn.Status)
		h.sendError(ctx, req, "⚠️ Codex 返回异常状态")
	}
}

// lastAgentMessageText scans the items list in reverse for the last
// {type:"agentMessage"} entry and returns its text. Returns "" if none.
func lastAgentMessageText(items []json.RawMessage) string {
	for i := len(items) - 1; i >= 0; i-- {
		var shell struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(items[i], &shell); err != nil {
			continue
		}
		if shell.Type == "agentMessage" && shell.Text != "" {
			return shell.Text
		}
	}
	return ""
}

func transportToUserMessage(t *CodexTransportError) string {
	switch t.Code {
	case "brokerTimeout":
		return "⚠️ 处理超时，请稍后重试"
	default:
		return "⚠️ Codex 处理失败，请稍后重试"
	}
}

// buildCodexInput constructs the codex turn/start params.input from the
// inbound WeChat message. MVP: text only. Quoted text is concatenated
// into the same text item with a "引用:" prefix; image/media ignored.
func buildCodexInput(req codexInboundRequest) json.RawMessage {
	text := req.Text
	if req.QuotedText != "" {
		quoter := req.QuotedSender
		if quoter == "" {
			quoter = "之前的消息"
		}
		text = fmt.Sprintf("[引用 %s] %s\n%s", quoter, req.QuotedText, req.Text)
	}
	wrapped := map[string]any{
		"input": []map[string]any{
			{"type": "text", "text": text},
		},
	}
	b, _ := json.Marshal(wrapped)
	return b
}

// sendText / sendError both POST /api/internal/imbridge/send. The
// endpoint's StopTyping side-effect kicks in automatically.

func (h *codexInboundHandler) sendText(ctx context.Context, req codexInboundRequest, text string) {
	h.postSend(ctx, map[string]any{
		"channel_id": req.ChannelID,
		"to_user_id": req.WechatUserID,
		"text":       text,
	})
}

func (h *codexInboundHandler) sendError(ctx context.Context, req codexInboundRequest, text string) {
	h.postSend(ctx, map[string]any{
		"channel_id": req.ChannelID,
		"to_user_id": req.WechatUserID,
		"text":       text,
	})
}

func (h *codexInboundHandler) postSend(ctx context.Context, body map[string]any) {
	b, _ := json.Marshal(body)
	r, err := http.NewRequestWithContext(ctx, "POST", h.imbridgeSendURL+"/api/internal/imbridge/send", bytes.NewReader(b))
	if err != nil {
		log.Printf("codex_im: build send req: %v", err)
		return
	}
	r.Header.Set("Content-Type", "application/json")
	if h.internalSecret != "" {
		r.Header.Set("X-Internal-Secret", h.internalSecret)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		log.Printf("codex_im: send POST: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("codex_im: send status=%d body=%s", resp.StatusCode, body)
	}
}
