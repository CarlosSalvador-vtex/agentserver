package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/db"
)

func openAutomationTestDB(t *testing.T) *db.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d, err := db.Open(url)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	return d
}

func automationConfigJSON(t *testing.T, chID, wsID, userID, prompt string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{
		"channel_id": chID, "workspace_id": wsID, "wechat_user_id": userID, "prompt": prompt,
	})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return b
}

func insertAutomationFixtures(t *testing.T, d *db.DB, ctx context.Context) (wsID, chID, userID string) {
	t.Helper()
	wsID = "ws-auto-" + t.Name()
	if err := d.CreateWorkspace(wsID, "auto test ws"); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteWorkspace(wsID) })

	// The channel's bound user_id IS the wechat_user_id the create handler
	// derives for the automation config, so set it to the returned userID to
	// keep fixtures self-consistent (handler reads ch.UserID, tests assert userID).
	userID = "wxid_auto_" + t.Name()
	chID = "ch-auto-" + t.Name()
	_, err := d.ExecContext(ctx,
		`INSERT INTO workspace_im_channels (id, workspace_id, provider, bot_id, user_id)
		 VALUES ($1, $2, 'weixin', 'bot', $3)`,
		chID, wsID, userID,
	)
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	t.Cleanup(func() { _, _ = d.ExecContext(ctx, `DELETE FROM workspace_im_channels WHERE id = $1`, chID) })

	return wsID, chID, userID
}

func createTestAutomation(t *testing.T, d *db.DB, ctx context.Context, wsID, chID string, cfg json.RawMessage, cron string) string {
	t.Helper()
	id := "auto_" + t.Name()
	past := time.Now().UTC().Add(-time.Minute)
	a := &db.Automation{
		ID:          id,
		WorkspaceID: wsID,
		Name:        "test automation",
		SkillRef:    "",
		Cron:        cron,
		ChannelID:   chID,
		Config:      cfg,
		Enabled:     true,
		NextRunAt:   &past,
	}
	if err := d.CreateAutomation(ctx, a); err != nil {
		t.Fatalf("CreateAutomation: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteAutomation(ctx, id) })
	return id
}

func TestFireAutomationCodexHandlerNil(t *testing.T) {
	d := openAutomationTestDB(t)
	ctx := context.Background()
	wsID, chID, userID := insertAutomationFixtures(t, d, ctx)
	cfg := automationConfigJSON(t, chID, wsID, userID, "hi")
	autoID := createTestAutomation(t, d, ctx, wsID, chID, cfg, "@every 1h")

	srv := &Server{DB: d, codexHandler: nil}
	srv.fireAutomation(ctx, db.Automation{
		ID: autoID, WorkspaceID: wsID, Cron: "@every 1h", Config: cfg,
	})

	got, err := d.GetAutomation(ctx, autoID)
	if err != nil {
		t.Fatalf("GetAutomation: %v", err)
	}
	if got.LastError == nil || *got.LastError != "codex handler not configured" {
		t.Fatalf("last_error = %v, want codex handler not configured", got.LastError)
	}
	if got.LastRunAt == nil {
		t.Fatal("expected last_run_at set")
	}
}

func TestFireAutomationDeliverSuccess(t *testing.T) {
	d := openAutomationTestDB(t)
	ctx := context.Background()
	wsID, chID, userID := insertAutomationFixtures(t, d, ctx)
	cfg := automationConfigJSON(t, chID, wsID, userID, "auto prompt")
	autoID := createTestAutomation(t, d, ctx, wsID, chID, cfg, "@every 1h")

	codex := &fakeCodexClient{
		turnFn: func(req CodexTurnRequest) (*CodexTurnResponse, error) {
			if !strings.Contains(string(req.Params), "auto prompt") {
				t.Errorf("params missing prompt, got %s", string(req.Params))
			}
			turn, _ := json.Marshal(map[string]any{
				"status": "completed",
				"items": []map[string]string{
					{"type": "agentMessage", "text": "automation reply"},
				},
			})
			return &CodexTurnResponse{ThreadID: "thr-auto", Turn: turn}, nil
		},
	}
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()
	h := newCodexInboundHandler(codex, &fakeSessionStore{}, sendURL, os.Getenv("INTERNAL_API_SECRET"))
	srv := &Server{DB: d, codexHandler: h}

	srv.fireAutomation(ctx, db.Automation{
		ID: autoID, WorkspaceID: wsID, Cron: "@every 1h", Config: cfg,
	})

	list := sends.Load().([]*capturedSend)
	if len(list) != 1 {
		t.Fatalf("sends = %d, want 1", len(list))
	}
	if list[0].text != "automation reply" {
		t.Fatalf("text = %v", list[0].text)
	}

	got, err := d.GetAutomation(ctx, autoID)
	if err != nil {
		t.Fatalf("GetAutomation: %v", err)
	}
	if got.LastError != nil {
		t.Fatalf("last_error = %v, want nil", got.LastError)
	}
	if got.NextRunAt == nil {
		t.Fatal("expected next_run_at")
	}
}

func TestFireAutomationDeliverFailure(t *testing.T) {
	d := openAutomationTestDB(t)
	ctx := context.Background()
	wsID, chID, userID := insertAutomationFixtures(t, d, ctx)
	cfg := automationConfigJSON(t, chID, wsID, userID, "x")
	autoID := createTestAutomation(t, d, ctx, wsID, chID, cfg, "@every 1h")

	codex := &fakeCodexClient{
		turnFn: func(req CodexTurnRequest) (*CodexTurnResponse, error) {
			return nil, errors.New("cxg down")
		},
	}
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()
	h := newCodexInboundHandler(codex, &fakeSessionStore{}, sendURL, os.Getenv("INTERNAL_API_SECRET"))
	srv := &Server{DB: d, codexHandler: h}

	srv.fireAutomation(ctx, db.Automation{
		ID: autoID, WorkspaceID: wsID, Cron: "@every 1h", Config: cfg,
	})

	got, err := d.GetAutomation(ctx, autoID)
	if err != nil {
		t.Fatalf("GetAutomation: %v", err)
	}
	if got.LastError == nil || *got.LastError != "cxg down" {
		t.Fatalf("last_error = %v", got.LastError)
	}
	if len(sends.Load().([]*capturedSend)) == 0 {
		t.Fatal("expected error delivered to channel")
	}
}

func TestFireAutomationMalformedCron(t *testing.T) {
	d := openAutomationTestDB(t)
	ctx := context.Background()
	wsID, chID, userID := insertAutomationFixtures(t, d, ctx)
	cfg := automationConfigJSON(t, chID, wsID, userID, "x")
	autoID := createTestAutomation(t, d, ctx, wsID, chID, cfg, "not a cron")

	sendURL, _, stop := newCapturingImbridge(t)
	defer stop()
	h := newCodexInboundHandler(&fakeCodexClient{}, &fakeSessionStore{}, sendURL, "")
	srv := &Server{DB: d, codexHandler: h}

	srv.fireAutomation(ctx, db.Automation{
		ID: autoID, WorkspaceID: wsID, Cron: "not a cron", Config: cfg,
	})

	got, err := d.GetAutomation(ctx, autoID)
	if err != nil {
		t.Fatalf("GetAutomation: %v", err)
	}
	if got.LastError == nil {
		t.Fatal("expected last_error for malformed cron")
	}
	if got.NextRunAt != nil {
		t.Fatalf("next_run_at should be cleared, got %v", got.NextRunAt)
	}
}

func TestRunDueAutomations(t *testing.T) {
	d := openAutomationTestDB(t)
	ctx := context.Background()
	wsID, chID, userID := insertAutomationFixtures(t, d, ctx)
	cfg := automationConfigJSON(t, chID, wsID, userID, "batch")
	createTestAutomation(t, d, ctx, wsID, chID, cfg, "@every 1h")

	codex := &fakeCodexClient{
		turnFn: func(req CodexTurnRequest) (*CodexTurnResponse, error) {
			turn, _ := json.Marshal(map[string]any{
				"status": "completed",
				"items":  []map[string]string{{"type": "agentMessage", "text": "ok"}},
			})
			return &CodexTurnResponse{Turn: turn}, nil
		},
	}
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()
	h := newCodexInboundHandler(codex, &fakeSessionStore{}, sendURL, os.Getenv("INTERNAL_API_SECRET"))
	srv := &Server{DB: d, codexHandler: h}

	srv.runDueAutomations(ctx)

	if len(sends.Load().([]*capturedSend)) != 1 {
		t.Fatalf("sends = %d", len(sends.Load().([]*capturedSend)))
	}
}
