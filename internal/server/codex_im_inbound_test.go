package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// captureSender records what handler POSTed to /api/internal/imbridge/send.
type capturedSend struct {
	channelID string
	toUser    string
	text      string
}

func newCapturingImbridge(t *testing.T) (url string, sends *atomic.Value /* []*capturedSend */, stop func()) {
	t.Helper()
	var stored atomic.Value
	stored.Store([]*capturedSend{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ChannelID string `json:"channel_id"`
			ToUserID  string `json:"to_user_id"`
			Text      string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		cur := stored.Load().([]*capturedSend)
		stored.Store(append(cur, &capturedSend{channelID: body.ChannelID, toUser: body.ToUserID, text: body.Text}))
		w.WriteHeader(200)
	}))
	return srv.URL, &stored, srv.Close
}

// fakeSessionStore implements the sessionStore interface (defined in
// codex_im_inbound.go) by routing through caller-supplied closures.
type fakeSessionStore struct {
	get    func(ctx context.Context, workspaceID, externalID string) (sessionView, error)
	set    func(ctx context.Context, sessionID string, threadID *string) error
	create func(ctx context.Context, workspaceID, externalID, title, imChannelID string) (sessionView, error)
}

func (f *fakeSessionStore) GetSessionByExternalID(ctx context.Context, workspaceID, externalID string) (sessionView, error) {
	return f.get(ctx, workspaceID, externalID)
}

func (f *fakeSessionStore) SetSessionCodexThreadID(ctx context.Context, sessionID string, threadID *string) error {
	return f.set(ctx, sessionID, threadID)
}

func (f *fakeSessionStore) CreateSession(ctx context.Context, workspaceID, externalID, title, imChannelID string) (sessionView, error) {
	if f.create != nil {
		return f.create(ctx, workspaceID, externalID, title, imChannelID)
	}
	// Default: return a synthetic session so tests that don't care about
	// session creation don't need to wire up a create closure.
	return sessionView{ID: "sess-created", CodexThreadID: nil}, nil
}

// fakeCodexClient lets us inject CXG responses. If turnFn is set it
// takes precedence (per-call dynamic behavior, e.g. for retry tests);
// otherwise the static resp/err pair is returned.
type fakeCodexClient struct {
	resp   *CodexTurnResponse
	err    error
	turnFn func(req CodexTurnRequest) (*CodexTurnResponse, error)
}

func (f *fakeCodexClient) RunTurn(_ context.Context, req CodexTurnRequest) (*CodexTurnResponse, error) {
	if f.turnFn != nil {
		return f.turnFn(req)
	}
	return f.resp, f.err
}

func TestCodexInboundHappyPath(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	h := newCodexInboundHandler(
		&fakeCodexClient{
			resp: &CodexTurnResponse{
				ThreadID: "thr-new",
				Turn:     json.RawMessage(`{"id":"trn-1","status":"completed","items":[{"type":"agentMessage","id":"m1","text":"hello"}],"itemsView":"full","error":null}`),
			},
		},
		&fakeSessionStore{
			get: func(_ context.Context, _, _ string) (sessionView, error) {
				return sessionView{ID: "sess-1", CodexThreadID: nil}, nil
			},
			set: func(_ context.Context, sessionID string, tid *string) error {
				if sessionID != "sess-1" || tid == nil || *tid != "thr-new" {
					t.Errorf("set called with sessionID=%s tid=%v", sessionID, tid)
				}
				return nil
			},
		},
		sendURL,
		"",
	)
	defer h.dispatcher.Stop()

	body := map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_a",
		"text":           "hi",
	}
	r := newCodexInboundRequest(body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202", w.Code)
	}
	waitFor(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })
	captured := sends.Load().([]*capturedSend)[0]
	if captured.text != "hello" {
		t.Errorf("send text=%q want hello", captured.text)
	}
	if captured.toUser != "wxid_a" {
		t.Errorf("send to=%q", captured.toUser)
	}
}

func TestCodexInboundFailedWithUsageLimit(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	h := newCodexInboundHandler(
		&fakeCodexClient{
			resp: &CodexTurnResponse{
				ThreadID: "thr-x",
				Turn:     json.RawMessage(`{"id":"trn-1","status":"failed","items":[],"itemsView":"full","error":{"message":"quota","codexErrorInfo":"usageLimitExceeded","additionalDetails":null}}`),
			},
		},
		&fakeSessionStore{
			get: func(_ context.Context, _, _ string) (sessionView, error) {
				return sessionView{ID: "sess-1", CodexThreadID: strPtr("thr-x")}, nil
			},
			set: func(context.Context, string, *string) error { return nil },
		},
		sendURL,
		"",
	)
	defer h.dispatcher.Stop()
	r := newCodexInboundRequest(map[string]any{
		"channel_id": "ch", "workspace_id": "ws", "wechat_user_id": "u", "text": "x",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	waitFor(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })
	got := sends.Load().([]*capturedSend)[0]
	if !strings.Contains(got.text, "配额") {
		t.Errorf("text=%q want quota message", got.text)
	}
}

func TestCodexInboundContextWindowClearsThread(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	var cleared int32
	h := newCodexInboundHandler(
		&fakeCodexClient{
			resp: &CodexTurnResponse{
				ThreadID: "thr-old",
				Turn:     json.RawMessage(`{"id":"trn-1","status":"failed","items":[],"itemsView":"full","error":{"message":"too long","codexErrorInfo":"contextWindowExceeded","additionalDetails":null}}`),
			},
		},
		&fakeSessionStore{
			get: func(_ context.Context, _, _ string) (sessionView, error) {
				return sessionView{ID: "sess-1", CodexThreadID: strPtr("thr-old")}, nil
			},
			set: func(_ context.Context, _ string, tid *string) error {
				if tid != nil {
					t.Errorf("want clear (nil), got %v", *tid)
				}
				atomic.AddInt32(&cleared, 1)
				return nil
			},
		},
		sendURL,
		"",
	)
	defer h.dispatcher.Stop()
	r := newCodexInboundRequest(map[string]any{
		"channel_id": "ch", "workspace_id": "ws", "wechat_user_id": "u", "text": "x",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	waitFor(t, func() bool { return atomic.LoadInt32(&cleared) > 0 && len(sends.Load().([]*capturedSend)) == 1 })
	if !strings.Contains(sends.Load().([]*capturedSend)[0].text, "上下文") {
		t.Errorf("want context-window message")
	}
}

func TestCodexInboundTransportError(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()
	h := newCodexInboundHandler(
		&fakeCodexClient{
			resp: &CodexTurnResponse{
				ThreadID:  "thr-x",
				Transport: &CodexTransportError{Code: "brokerTimeout", Message: "..."},
			},
		},
		&fakeSessionStore{
			get: func(_ context.Context, _, _ string) (sessionView, error) {
				return sessionView{ID: "sess-1"}, nil
			},
			set: func(context.Context, string, *string) error { return nil },
		},
		sendURL,
		"",
	)
	defer h.dispatcher.Stop()
	r := newCodexInboundRequest(map[string]any{
		"channel_id": "ch", "workspace_id": "ws", "wechat_user_id": "u", "text": "x",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	waitFor(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })
	if !strings.Contains(sends.Load().([]*capturedSend)[0].text, "超时") {
		t.Errorf("want timeout message")
	}
}

// TestCodexInboundCreatesSessionOnFirstMessage verifies that when
// GetSessionByExternalID returns an empty sessionView (session not found),
// the handler calls CreateSession and then proceeds with the turn normally.
func TestCodexInboundCreatesSessionOnFirstMessage(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	var createCalled int32
	h := newCodexInboundHandler(
		&fakeCodexClient{
			resp: &CodexTurnResponse{
				ThreadID: "thr-new",
				Turn:     json.RawMessage(`{"id":"trn-1","status":"completed","items":[{"type":"agentMessage","id":"m1","text":"session created"}],"itemsView":"full","error":null}`),
			},
		},
		&fakeSessionStore{
			get: func(_ context.Context, _, _ string) (sessionView, error) {
				// Return empty to simulate "not found".
				return sessionView{}, nil
			},
			set: func(_ context.Context, _ string, _ *string) error { return nil },
			create: func(_ context.Context, workspaceID, externalID, title, imChannelID string) (sessionView, error) {
				atomic.AddInt32(&createCalled, 1)
				if externalID != "wxid_new" {
					t.Errorf("create externalID=%q want wxid_new", externalID)
				}
				if workspaceID != "ws-1" {
					t.Errorf("create workspaceID=%q want ws-1", workspaceID)
				}
				if imChannelID != "ch-1" {
					t.Errorf("create imChannelID=%q want ch-1", imChannelID)
				}
				return sessionView{ID: "sess-auto", CodexThreadID: nil}, nil
			},
		},
		sendURL,
		"",
	)
	defer h.dispatcher.Stop()

	r := newCodexInboundRequest(map[string]any{
		"channel_id":     "ch-1",
		"workspace_id":   "ws-1",
		"wechat_user_id": "wxid_new",
		"text":           "hello",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202", w.Code)
	}
	waitFor(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })
	if atomic.LoadInt32(&createCalled) != 1 {
		t.Errorf("createCalled=%d want 1", createCalled)
	}
	got := sends.Load().([]*capturedSend)[0]
	if got.text != "session created" {
		t.Errorf("reply text=%q want 'session created'", got.text)
	}
	if got.toUser != "wxid_new" {
		t.Errorf("reply toUser=%q want wxid_new", got.toUser)
	}
}

// helpers

func newCodexInboundRequest(body map[string]any) *http.Request {
	b, _ := json.Marshal(body)
	return httptest.NewRequest("POST", "/api/internal/imbridge/codex/turn", bytes.NewReader(b))
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("waitFor: condition never satisfied")
}

func strPtr(s string) *string { return &s }

func TestCodexInboundRetriesOnThreadNotFound(t *testing.T) {
	sendURL, sends, stop := newCapturingImbridge(t)
	defer stop()

	var calls int
	var clearedThread bool
	h := newCodexInboundHandler(
		&fakeCodexClient{
			turnFn: func(req CodexTurnRequest) (*CodexTurnResponse, error) {
				calls++
				if calls == 1 {
					if req.ThreadID == nil || *req.ThreadID != "thr-stale" {
						t.Errorf("first call: ThreadID=%v want thr-stale", req.ThreadID)
					}
					return &CodexTurnResponse{
						ThreadID:  "thr-stale",
						Transport: &CodexTransportError{Code: "wsDisconnect", Message: "codex rpc error -32600: thread not found: thr-stale"},
					}, nil
				}
				// Second call should have ThreadID=nil (cleared).
				if req.ThreadID != nil {
					t.Errorf("retry call: ThreadID=%v want nil", req.ThreadID)
				}
				return &CodexTurnResponse{
					ThreadID: "thr-fresh",
					Turn:     json.RawMessage(`{"id":"trn-1","status":"completed","items":[{"type":"agentMessage","id":"m","text":"recovered"}],"itemsView":"full","error":null}`),
				}, nil
			},
		},
		&fakeSessionStore{
			get: func(_ context.Context, _, _ string) (sessionView, error) {
				return sessionView{ID: "sess-1", CodexThreadID: strPtr("thr-stale")}, nil
			},
			set: func(_ context.Context, _ string, tid *string) error {
				if tid == nil {
					clearedThread = true
				}
				return nil
			},
		},
		sendURL, "",
	)
	defer h.Close()

	r := newCodexInboundRequest(map[string]any{
		"channel_id": "ch", "workspace_id": "ws", "wechat_user_id": "u", "text": "hi",
	})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	waitFor(t, func() bool { return len(sends.Load().([]*capturedSend)) == 1 })

	if calls != 2 {
		t.Errorf("calls=%d want 2 (initial + retry)", calls)
	}
	if !clearedThread {
		t.Error("expected SetSessionCodexThreadID(nil) to be called")
	}
	if got := sends.Load().([]*capturedSend)[0].text; got != "recovered" {
		t.Errorf("text=%q want recovered", got)
	}
}

func TestIsThreadNotFoundErr(t *testing.T) {
	cases := map[string]bool{
		"codex rpc error -32600: thread not found: abc": true,
		`thread "thr-abc" unknown`:                      true,
		"missing thread":                                true,
		// "no rollout found for thread id" / "...conversation id" is the
		// stable error string from codex thread_processor.rs:3589/3668,
		// surfaced when the broker calls thread/resume on a thread codex
		// no longer has on disk (e.g. subprocess restarted between turns).
		// Without these matches the retry-on-fresh-thread path doesn't
		// fire and the user sees "⚠️ Codex 处理失败".
		"ensure listener: codex rpc error -32600: no rollout found for thread id 019e4972-6cf4": true,
		"no rollout found for thread id abc":                  true,
		"no rollout found for conversation id def":            true,
		"some other error":                                    false,
		"thread is in progress":                               false,
		"":                                                    false,
		"connection closed":                                   false,
	}
	for in, want := range cases {
		if got := isThreadNotFoundErr(in); got != want {
			t.Errorf("isThreadNotFoundErr(%q) = %v, want %v", in, got, want)
		}
	}
}

// 1×1 transparent PNG — smallest valid PNG, just enough for
// http.DetectContentType to return "image/png".
const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg=="

// TestBuildCodexInput_TextOnly pins the baseline: no media → exactly
// one text item, no extra noise. Guards against accidentally emitting
// an empty image item.
func TestBuildCodexInput_TextOnly(t *testing.T) {
	raw := buildCodexInput(codexInboundRequest{Text: "hello"})
	var got struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Input) != 1 {
		t.Fatalf("input items = %d, want 1", len(got.Input))
	}
	if got.Input[0]["type"] != "text" || got.Input[0]["text"] != "hello" {
		t.Errorf("input[0] = %v, want text/hello", got.Input[0])
	}
}

// TestBuildCodexInput_WithImage covers the main fix: image bytes from
// imbridge become a UserInput::Image data URL in the codex turn input.
func TestBuildCodexInput_WithImage(t *testing.T) {
	raw := buildCodexInput(codexInboundRequest{
		Text:      "look at this",
		MediaType: "image",
		MediaData: tinyPNGBase64,
	})
	var got struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Input) != 2 {
		t.Fatalf("input items = %d, want 2 (text + image)", len(got.Input))
	}
	img := got.Input[1]
	if img["type"] != "image" {
		t.Errorf("input[1].type = %v, want image", img["type"])
	}
	url, _ := img["url"].(string)
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Errorf("input[1].url = %q, want data:image/png;base64,... prefix", url)
	}
	if !strings.HasSuffix(url, tinyPNGBase64) {
		t.Errorf("input[1].url should round-trip the original base64 (no re-encode), got %q", url)
	}
}

// TestBuildCodexInput_QuotedImageBeforeCurrent locks in chronological
// order — the quoted (older) image must come before the current
// message's image so the LLM sees them in conversation order.
func TestBuildCodexInput_QuotedImageBeforeCurrent(t *testing.T) {
	raw := buildCodexInput(codexInboundRequest{
		Text:            "follow-up",
		QuotedText:      "earlier msg",
		QuotedSender:    "alice",
		QuotedMediaType: "image",
		QuotedMediaData: tinyPNGBase64,
		MediaType:       "image",
		MediaData:       tinyPNGBase64,
	})
	var got struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Input) != 3 {
		t.Fatalf("input items = %d, want 3 (text + quoted-image + current-image)", len(got.Input))
	}
	if got.Input[0]["type"] != "text" {
		t.Errorf("input[0].type = %v, want text", got.Input[0]["type"])
	}
	if got.Input[1]["type"] != "image" || got.Input[2]["type"] != "image" {
		t.Errorf("input[1] and input[2] should both be image, got %v %v", got.Input[1]["type"], got.Input[2]["type"])
	}
}

// TestImageInputItem_RejectsNonImageMediaType — imbridge sends "file"
// for non-image attachments (PDF, etc.); we must NOT misclassify those
// as images. codex has no File variant, so handling falls back to the
// "user sent a file: X" text injected by imbridge's describeWeixinMedia.
func TestImageInputItem_RejectsNonImageMediaType(t *testing.T) {
	_, ok := imageInputItem("file", tinyPNGBase64)
	if ok {
		t.Error("expected file mediaType to be rejected")
	}
}

// TestImageInputItem_RejectsBadBase64 — a typo / truncation upstream
// shouldn't crash or generate a malformed data URL; just skip.
func TestImageInputItem_RejectsBadBase64(t *testing.T) {
	_, ok := imageInputItem("image", "not!!!valid$$$base64===")
	if ok {
		t.Error("expected garbage base64 to be rejected")
	}
}

// TestImageInputItem_RejectsNonImageBytes — defense against an upstream
// that misdeclares mediaType=image but ships text/binary. We don't want
// to ship a malformed data URL to codex.
func TestImageInputItem_RejectsNonImageBytes(t *testing.T) {
	// "hello world" base64 — http.DetectContentType returns "text/plain".
	_, ok := imageInputItem("image", "aGVsbG8gd29ybGQ=")
	if ok {
		t.Error("expected non-image bytes to be rejected even when mediaType=image")
	}
}

// TestImageInputItem_DetectsJPEG covers another MIME the iLink CDN
// actually returns. http.DetectContentType reads the first 512 bytes,
// JPEG magic is the first 3 (FF D8 FF).
func TestImageInputItem_DetectsJPEG(t *testing.T) {
	// Minimal JPEG header: SOI (FF D8 FF) + APP0 marker (E0) + length (00 10)
	// + JFIF magic + version + density. Enough for DetectContentType.
	jpegBytes := []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00,
		0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00,
	}
	jpegB64 := base64.StdEncoding.EncodeToString(jpegBytes)
	item, ok := imageInputItem("image", jpegB64)
	if !ok {
		t.Fatal("expected JPEG to be accepted")
	}
	url := item["url"].(string)
	if !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Errorf("expected image/jpeg mime in data URL, got %q", url)
	}
}

// TestFileTextSnippet_TextFileInlined covers the main file fix: text
// files (code, configs, plain) get inlined into the text input.
func TestFileTextSnippet_TextFileInlined(t *testing.T) {
	content := "package main\nfunc main() { println(\"hi\") }\n"
	b64 := base64.StdEncoding.EncodeToString([]byte(content))
	snip := fileTextSnippet("file", b64, "main.go")
	if !strings.Contains(snip, "main.go") {
		t.Errorf("snippet should name the file, got %q", snip)
	}
	if !strings.Contains(snip, content) {
		t.Errorf("snippet should embed file content verbatim, got %q", snip)
	}
	if !strings.Contains(snip, "```") {
		t.Errorf("snippet should wrap content in a fenced code block, got %q", snip)
	}
}

// TestFileTextSnippet_BinaryFileSkipped — PDFs, zips, etc. must NOT
// have raw bytes inlined into text (would be unintelligible noise +
// likely break UTF-8). Caller relies on imbridge's existing
// "[User sent a file: X]" marker in that case.
func TestFileTextSnippet_BinaryFileSkipped(t *testing.T) {
	// PDF magic: "%PDF-"
	pdf := []byte{'%', 'P', 'D', 'F', '-', '1', '.', '4', '\n', 0x00, 0x00, 0x00}
	snip := fileTextSnippet("file", base64.StdEncoding.EncodeToString(pdf), "report.pdf")
	if snip != "" {
		t.Errorf("expected empty snippet for binary PDF, got %q", snip)
	}
}

// TestFileTextSnippet_LargeTextTruncated guards the codex
// MAX_USER_INPUT_TEXT_CHARS budget. A user dropping a 2 MB log
// shouldn't blow the input limit.
func TestFileTextSnippet_LargeTextTruncated(t *testing.T) {
	large := strings.Repeat("ABCD\n", 60_000) // 300 KB of text
	snip := fileTextSnippet("file", base64.StdEncoding.EncodeToString([]byte(large)), "big.log")
	if !strings.Contains(snip, "truncated") {
		t.Errorf("expected truncation marker, got %q", snip[:200])
	}
	// Snippet shouldn't carry the full original content.
	if strings.Count(snip, "ABCD") >= 60_000 {
		t.Errorf("snippet not truncated — got %d ABCD occurrences", strings.Count(snip, "ABCD"))
	}
}

// TestFileTextSnippet_ImageMediaTypeSkipped — `image` mediaType is
// handled by imageInputItem, not here. Avoid double-handling.
func TestFileTextSnippet_ImageMediaTypeSkipped(t *testing.T) {
	if snip := fileTextSnippet("image", tinyPNGBase64, "p.png"); snip != "" {
		t.Errorf("image mediaType should be no-op for file snippet, got %q", snip)
	}
}

// TestFileTextSnippet_NoFilenameUsesFallback — filename is optional in
// some upstreams; ensure we emit a sensible default label.
func TestFileTextSnippet_NoFilenameUsesFallback(t *testing.T) {
	snip := fileTextSnippet("file", base64.StdEncoding.EncodeToString([]byte("hello")), "")
	if !strings.Contains(snip, "file") {
		t.Errorf("expected 'file' as default label, got %q", snip)
	}
}

// TestBuildCodexInput_TextFileInlinedIntoText pins the user-visible
// behavior: the file content appears inside the text input item, NOT
// as a separate image item.
func TestBuildCodexInput_TextFileInlinedIntoText(t *testing.T) {
	raw := buildCodexInput(codexInboundRequest{
		Text:          "review this",
		MediaType:     "file",
		MediaData:     base64.StdEncoding.EncodeToString([]byte("hello world\n")),
		MediaFilename: "notes.txt",
	})
	var got struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Input) != 1 {
		t.Fatalf("expected 1 input item (text only — files don't get their own item), got %d", len(got.Input))
	}
	text, _ := got.Input[0]["text"].(string)
	if !strings.Contains(text, "review this") {
		t.Errorf("user text missing from combined input, got %q", text)
	}
	if !strings.Contains(text, "notes.txt") || !strings.Contains(text, "hello world") {
		t.Errorf("file content/name missing from text, got %q", text)
	}
}
