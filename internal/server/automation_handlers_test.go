package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newAutomationHandlerTestServer(t *testing.T) (*Server, string, string, string) {
	t.Helper()
	d := newCodexTestDBForServer(t)
	ctx := t.Context()
	wsID, chID, userID := insertAutomationFixtures(t, d, ctx)
	seedWorkspaceMember(t, d, wsID, "owner-user", "owner")
	seedWorkspaceMember(t, d, wsID, "viewer-user", "viewer")
	return &Server{DB: d}, wsID, chID, userID
}

func TestHandleListAutomationsEmpty(t *testing.T) {
	srv, wsID, _, _ := newAutomationHandlerTestServer(t)
	req := reqWithUser(http.MethodGet, "/api/workspaces/"+wsID+"/automations", "owner-user", nil, map[string]string{"id": wsID})
	rr := httptest.NewRecorder()
	srv.handleListAutomations(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d %s", rr.Code, rr.Body.String())
	}
	var resp AutomationListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Automations) != 0 {
		t.Fatalf("expected empty list, got %d", len(resp.Automations))
	}
}

func TestHandleCreateGetPatchDeleteAutomation(t *testing.T) {
	srv, wsID, chID, userID := newAutomationHandlerTestServer(t)
	ctx := t.Context()

	createBody, _ := json.Marshal(AutomationCreateRequest{
		Name:      "daily digest",
		SkillRef:  "playground",
		Cron:      "@daily",
		ChannelID: chID,
		Prompt:    "Summarize inbox",
	})
	req := reqWithUser(http.MethodPost, "/api/workspaces/"+wsID+"/automations", "owner-user", createBody, map[string]string{"id": wsID})
	rr := httptest.NewRecorder()
	srv.handleCreateAutomation(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d %s", rr.Code, rr.Body.String())
	}
	var created AutomationResponse
	if err := json.NewDecoder(rr.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" || created.Name != "daily digest" || !created.Enabled {
		t.Fatalf("bad create response: %+v", created)
	}
	if created.NextRunAt == nil || *created.NextRunAt == "" {
		t.Fatal("expected next_run_at on create")
	}

	reqGet := reqWithUser(http.MethodGet, "/api/workspaces/"+wsID+"/automations/"+created.ID, "owner-user", nil,
		map[string]string{"id": wsID, "automationId": created.ID})
	rrGet := httptest.NewRecorder()
	srv.handleGetAutomation(rrGet, reqGet)
	if rrGet.Code != http.StatusOK {
		t.Fatalf("get: want 200, got %d", rrGet.Code)
	}

	patchBody, _ := json.Marshal(AutomationPatchRequest{
		Enabled: boolPtr(false),
		Prompt:  strPtr("updated prompt"),
	})
	reqPatch := reqWithUser(http.MethodPatch, "/api/workspaces/"+wsID+"/automations/"+created.ID, "owner-user", patchBody,
		map[string]string{"id": wsID, "automationId": created.ID})
	rrPatch := httptest.NewRecorder()
	srv.handlePatchAutomation(rrPatch, reqPatch)
	if rrPatch.Code != http.StatusOK {
		t.Fatalf("patch: want 200, got %d %s", rrPatch.Code, rrPatch.Body.String())
	}
	var patched AutomationResponse
	if err := json.NewDecoder(rrPatch.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Enabled {
		t.Fatal("expected enabled false after patch")
	}

	got, err := srv.DB.GetAutomation(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetAutomation: %v", err)
	}
	cfg := map[string]string{}
	_ = json.Unmarshal(got.Config, &cfg)
	if cfg["prompt"] != "updated prompt" {
		t.Fatalf("config prompt = %q", cfg["prompt"])
	}
	if cfg["wechat_user_id"] != userID {
		t.Fatalf("wechat_user_id = %q want %q", cfg["wechat_user_id"], userID)
	}

	reqDel := reqWithUser(http.MethodDelete, "/api/workspaces/"+wsID+"/automations/"+created.ID, "owner-user", nil,
		map[string]string{"id": wsID, "automationId": created.ID})
	rrDel := httptest.NewRecorder()
	srv.handleDeleteAutomation(rrDel, reqDel)
	if rrDel.Code != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", rrDel.Code)
	}
	if _, err := srv.DB.GetAutomation(ctx, created.ID); err == nil {
		t.Fatal("automation should be gone")
	}
}

func TestHandleCreateAutomationViewerForbidden(t *testing.T) {
	srv, wsID, chID, _ := newAutomationHandlerTestServer(t)
	body, _ := json.Marshal(AutomationCreateRequest{
		Name: "x", Cron: "@hourly", ChannelID: chID, Prompt: "hi",
	})
	req := reqWithUser(http.MethodPost, "/api/workspaces/"+wsID+"/automations", "viewer-user", body, map[string]string{"id": wsID})
	rr := httptest.NewRecorder()
	srv.handleCreateAutomation(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("viewer create: want 403, got %d", rr.Code)
	}
}

func TestHandleCreateAutomationBadCron(t *testing.T) {
	srv, wsID, chID, _ := newAutomationHandlerTestServer(t)
	body, _ := json.Marshal(AutomationCreateRequest{
		Name: "x", Cron: "not-a-cron", ChannelID: chID, Prompt: "hi",
	})
	req := reqWithUser(http.MethodPost, "/api/workspaces/"+wsID+"/automations", "owner-user", body, map[string]string{"id": wsID})
	rr := httptest.NewRecorder()
	srv.handleCreateAutomation(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad cron: want 400, got %d", rr.Code)
	}
}

func TestHandleCreateAutomationForeignChannel(t *testing.T) {
	srv, wsID, _, _ := newAutomationHandlerTestServer(t)
	ctx := t.Context()
	otherWS := "ws-other-" + t.Name()
	if err := srv.DB.CreateWorkspace(otherWS, "other"); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	t.Cleanup(func() { _ = srv.DB.DeleteWorkspace(otherWS) })
	otherCh := "ch-other-" + t.Name()
	_, err := srv.DB.ExecContext(ctx,
		`INSERT INTO workspace_im_channels (id, workspace_id, provider, bot_id, user_id) VALUES ($1, $2, 'weixin', 'b', 'u')`,
		otherCh, otherWS,
	)
	if err != nil {
		t.Fatalf("channel: %v", err)
	}
	t.Cleanup(func() { _, _ = srv.DB.ExecContext(ctx, `DELETE FROM workspace_im_channels WHERE id = $1`, otherCh) })

	body, _ := json.Marshal(AutomationCreateRequest{
		Name: "x", Cron: "@hourly", ChannelID: otherCh, Prompt: "hi",
	})
	req := reqWithUser(http.MethodPost, "/api/workspaces/"+wsID+"/automations", "owner-user", body, map[string]string{"id": wsID})
	rr := httptest.NewRecorder()
	srv.handleCreateAutomation(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("foreign channel: want 400, got %d %s", rr.Code, rr.Body.String())
	}
}

func boolPtr(b bool) *bool { return &b }
