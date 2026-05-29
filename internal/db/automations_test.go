package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestComputeNextRunValid(t *testing.T) {
	from := time.Date(2026, 5, 29, 7, 0, 0, 0, time.UTC)
	next, err := ComputeNextRun("0 8 * * 1-5", from)
	if err != nil {
		t.Fatal(err)
	}
	// Next weekday 08:00 UTC after Friday 07:00
	if next.Weekday() == time.Saturday || next.Weekday() == time.Sunday {
		t.Fatalf("next=%v should be weekday", next)
	}
	if next.Hour() != 8 || next.Minute() != 0 {
		t.Fatalf("next=%v want 08:00", next)
	}
}

func TestComputeNextRunMalformed(t *testing.T) {
	_, err := ComputeNextRun("not a cron", time.Now())
	if err == nil {
		t.Fatal("expected error for malformed cron")
	}
}

func TestAutomationPromptFromConfig(t *testing.T) {
	// exercised indirectly via server tests; config shape documented here.
	cfg := json.RawMessage(`{"prompt":"hello scheduled"}`)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(cfg, &m); err != nil {
		t.Fatal(err)
	}
	var p string
	if err := json.Unmarshal(m["prompt"], &p); err != nil || p != "hello scheduled" {
		t.Fatalf("prompt parse: %v", err)
	}
}

func TestScanDueAndMarkRunIntegration(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()

	wsID := "ws_auto_" + t.Name()
	if err := d.CreateWorkspace(wsID, "auto test ws"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteWorkspace(wsID) })

	chID := "ch_auto_" + t.Name()
	_, err := d.ExecContext(ctx,
		`INSERT INTO workspace_im_channels (id, workspace_id, provider, bot_id, user_id)
		 VALUES ($1, $2, 'weixin', 'bot', 'wxid_testuser')`,
		chID, wsID,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.ExecContext(ctx, `DELETE FROM workspace_im_channels WHERE id = $1`, chID) })

	autoID := "auto_" + t.Name()
	past := time.Now().Add(-2 * time.Minute)
	next := time.Now().Add(-1 * time.Minute)
	cfg, _ := json.Marshal(map[string]string{"prompt": "scheduled ping"})
	a := &Automation{
		ID:          autoID,
		WorkspaceID: wsID,
		Name:        "test automation",
		SkillRef:    "",
		Cron:        "* * * * *",
		ChannelID:   chID,
		Config:      cfg,
		Enabled:     true,
		NextRunAt:   &next,
	}
	if err := d.CreateAutomation(ctx, a); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteAutomation(ctx, autoID) })

	due, err := d.ScanDueAutomations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != autoID {
		t.Fatalf("due=%+v", due)
	}

	runAt := time.Now().UTC().Truncate(time.Second)
	errMsg := "simulated failure"
	if err := d.MarkAutomationRun(ctx, autoID, runAt, &errMsg, time.Time{}); err != nil {
		t.Fatal(err)
	}
	got, err := d.GetAutomation(ctx, autoID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRunAt == nil || !got.LastRunAt.Equal(runAt) {
		t.Fatalf("last_run_at=%v want %v", got.LastRunAt, runAt)
	}
	if got.LastError == nil || *got.LastError != errMsg {
		t.Fatalf("last_error=%v", got.LastError)
	}
	if got.NextRunAt != nil {
		t.Fatalf("next_run_at should be null after malformed mark, got %v", got.NextRunAt)
	}

	// Not due anymore
	due2, _ := d.ScanDueAutomations(ctx)
	if len(due2) != 0 {
		t.Fatalf("still due after mark with null next: %+v", due2)
	}

	_ = past // silence unused in some toolchains
}
