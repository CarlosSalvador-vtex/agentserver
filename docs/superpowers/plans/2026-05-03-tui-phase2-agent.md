# TUI Phase 2: Agent Binary Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Prerequisites:** Phase 1 (`docs/superpowers/plans/2026-05-03-tui-phase1-backend.md`) MUST be deployed before Phase 2 can be tested end-to-end. Phase 2 unit tests don't require Phase 1 deployment but use httptest fakes.

**Goal:** Build the `agentserver tui` subcommand — a Bubble Tea terminal client that combines the existing `executor` ExecutorClient (manos) with a remote-harness UI driven by SSE events from agentserver.

**Architecture:** Single OS process hosts three independently-managed goroutines: (1) Bubble Tea program; (2) SSE consumer; (3) ExecutorClient (yamux tunnel). Three goroutines share *only* OAuth credentials — no in-process control flow between them. AuthController manages the credential lifecycle as the single source of truth for `LoggedOut` / `LoggingIn` / `LoggedIn` / `Refreshing` states.

**Tech Stack:** Go (`charmbracelet/bubbletea`, `charmbracelet/lipgloss`, `charmbracelet/bubbles`), reuses `internal/agent/login.go` for OAuth Device Flow.

**Spec:** [`docs/superpowers/specs/2026-05-03-agentserver-tui-design.md`](../specs/2026-05-03-agentserver-tui-design.md)

---

## File Structure (Phase 2)

| File | Created/Modified | Responsibility |
|---|---|---|
| `cmd/agentserver-agent/main.go` | Modified | Add `tuiCmd` cobra command + flags |
| `internal/agent/tui.go` | Created | `RunTUI(opts)` entry: wire AuthController + ExecutorClient + Bubble Tea |
| `internal/agent/login.go` | Modified | Extract `RequestDeviceCode` / `PollForToken` as exported reusable funcs (current `RunLogin` keeps using them) |
| `internal/agent/executor_session.go` | Modified | Add optional `RuntimeCwd` field; helpers to read/write the field on existing JSON file |
| `internal/agent/tui/auth.go` | Created | `AuthController` state machine: LoggedOut/LoggingIn/LoggedIn/Refreshing |
| `internal/agent/tui/bus.go` | Created | HTTP client wrapping all agentserver TUI endpoints |
| `internal/agent/tui/sse.go` | Created | SSE consumer with Last-Event-ID reconnect |
| `internal/agent/tui/msg.go` | Created | Bubble Tea Msg types |
| `internal/agent/tui/model.go` | Created | Top-level Model + Init/Update dispatcher |
| `internal/agent/tui/view.go` | Created | View renderer: status bar + viewport + panels + input |
| `internal/agent/tui/keymap.go` | Created | Keymap definitions |
| `internal/agent/tui/timeline.go` | Created | Timeline data structure + per-event renderer types |
| `internal/agent/tui/panels.go` | Created | Permission + AskUser panels |
| `internal/agent/tui/login_panel.go` | Created | OAuth Device Flow panel with QR rendering |
| `internal/agent/tui/logout_panel.go` | Created | Logout confirmation panel |
| `internal/agent/tui/cmds.go` | Created | Slash command parser (L/S/R classification) |
| `internal/agent/tui/attach_picker.go` | Created | `/attach` file picker |
| `internal/agent/tui/styles.go` | Created | lipgloss style definitions |

---

## Task Sequencing

CLI surface first (Task 1), then the cross-cutting controllers that the rest depend on (AuthController T2, Bus T3, SSE consumer T4), then the data structures (Msg T5, Timeline T6), then UI primitives (panels T7-T9, login panel T10), then the assembled Model + View + Update loop (T11-T13), then slash command logic (T14), then attach picker (T15), then `/cd` runtime cwd file coordination (T16), then end-to-end smoke (T17).

---

## Task 1: CLI surface — `tui` cobra command + flags

**Files:**
- Modify: `cmd/agentserver-agent/main.go`
- Create: `internal/agent/tui.go` (stub)

- [ ] **Step 1: Write the cobra command**

In `cmd/agentserver-agent/main.go`, add new flag vars and command. After existing `executorCmd`:

```go
var (
    tuiServer       string
    tuiWorkspaceID  string
    tuiName         string
    tuiWorkDir      string
    tuiResumeID     string
    tuiContinue     bool
    tuiYolo         bool
    tuiSkipBrowser  bool
    tuiModel        string
    tuiResponderTTL string
)

var tuiCmd = &cobra.Command{
    Use:   "tui",
    Short: "Interactive terminal client for stateless cc",
    Long: `Run an interactive Bubble Tea TUI that drives a remote stateless
cc session. The same process also acts as a local executor (hands) so
remote_* tool calls from cc-broker can land on this machine.

On first run, requires --server. After /login the server URL is saved.
Subsequent runs auto-load credentials and reconnect.`,
    Run: func(cmd *cobra.Command, args []string) {
        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()
        go func() {
            sigCh := make(chan os.Signal, 1)
            signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
            <-sigCh
            cancel()
        }()
        err := agent.RunTUI(ctx, agent.TUIOpts{
            Server:          tuiServer,
            WorkspaceID:     tuiWorkspaceID,
            Name:            tuiName,
            WorkDir:         tuiWorkDir,
            Resume:          tuiResumeID,
            Continue:        tuiContinue,
            Yolo:            tuiYolo,
            SkipOpenBrowser: tuiSkipBrowser,
            Model:           tuiModel,
            ResponderTTL:    tuiResponderTTL,
        })
        if err != nil && err != context.Canceled {
            fmt.Fprintf(os.Stderr, "Error: %v\n", err)
            os.Exit(1)
        }
    },
}

func init() {
    // ... existing init() body ...
    rootCmd.AddCommand(tuiCmd)

    defaultName, _ := os.Hostname()
    if defaultName != "" {
        defaultName += " (interactive)"
    } else {
        defaultName = "TUI Agent (interactive)"
    }
    tuiCmd.Flags().StringVar(&tuiServer, "server", "", "agentserver URL (defaults to saved creds)")
    tuiCmd.Flags().StringVar(&tuiWorkspaceID, "workspace-id", "", "workspace ID (required)")
    tuiCmd.Flags().StringVar(&tuiName, "name", defaultName, "display name for this executor")
    tuiCmd.Flags().StringVar(&tuiWorkDir, "work-dir", "", "executor working directory (default: cwd)")
    tuiCmd.Flags().StringVarP(&tuiResumeID, "resume", "r", "", "attach to an existing session by ID")
    tuiCmd.Flags().BoolVarP(&tuiContinue, "continue", "c", false, "attach to most recent TUI session")
    tuiCmd.Flags().BoolVar(&tuiYolo, "yolo", false, "start with permission_mode=bypass")
    tuiCmd.Flags().BoolVar(&tuiSkipBrowser, "skip-open-browser", false, "do not auto-open browser on /login")
    tuiCmd.Flags().StringVar(&tuiModel, "model", "", "sticky model for first turn (default: server choice)")
    tuiCmd.Flags().StringVar(&tuiResponderTTL, "responder-ttl", "", "override responder TTL (informational; server enforces)")
}
```

- [ ] **Step 2: Stub `agent.RunTUI`**

```go
// internal/agent/tui.go
package agent

import (
    "context"
    "errors"
)

type TUIOpts struct {
    Server          string
    WorkspaceID     string
    Name            string
    WorkDir         string
    Resume          string
    Continue        bool
    Yolo            bool
    SkipOpenBrowser bool
    Model           string
    ResponderTTL    string
}

// RunTUI is the entry point for the `tui` subcommand. Stubbed in Task 1;
// completed in Task 11 once Model + Bus + AuthController are wired.
func RunTUI(ctx context.Context, opts TUIOpts) error {
    return errors.New("tui: not yet implemented (Task 11 will wire it)")
}
```

- [ ] **Step 3: Build verification**

Run: `go build ./...`
Expected: success.

- [ ] **Step 4: Run --help on new command**

Run: `go run . tui --help` (from `cmd/agentserver-agent`)
Expected: prints all flags from Step 1.

- [ ] **Step 5: Commit**

```bash
git add cmd/agentserver-agent/main.go internal/agent/tui.go
git commit -m "feat(agent): tui cobra command surface (stub entry)"
```

---

## Task 2: AuthController — state machine

**Files:**
- Create: `internal/agent/tui/auth.go`
- Create: `internal/agent/tui/auth_test.go`
- Modify: `internal/agent/login.go` (export reusable functions)

- [ ] **Step 1: Refactor `internal/agent/login.go` to expose reusable funcs**

Currently `requestDeviceCode` and `pollForToken` are unexported. Rename them to `RequestDeviceCode` and `PollForToken` with the same signatures. `RunLogin` keeps calling them. No behavior change.

```go
// in internal/agent/login.go
func RequestDeviceCode(serverURL string) (*DeviceAuthResponse, error) { /* unchanged body */ }
func PollForToken(serverURL string, dr *DeviceAuthResponse) (*TokenResponse, error) { /* unchanged body */ }
```

Ensure `RunLogin` invokes the exported names. Run `go build ./...` to verify.

Commit this refactor on its own:
```bash
git add internal/agent/login.go
git commit -m "refactor(agent): export RequestDeviceCode + PollForToken for TUI reuse"
```

- [ ] **Step 2: Write failing tests for AuthController**

```go
// internal/agent/tui/auth_test.go
package tui

import (
    "context"
    "errors"
    "sync"
    "testing"
    "time"

    "github.com/agentserver/agentserver/internal/agent"
)

func TestAuth_StartsLoggedOutWhenNoCreds(t *testing.T) {
    ac := NewAuthController(AuthConfig{
        ServerURL:       "https://example",
        CredentialsPath: t.TempDir() + "/creds.json",
    })
    if ac.State() != AuthLoggedOut {
        t.Errorf("state=%v want LoggedOut", ac.State())
    }
}

func TestAuth_StartsLoggedInWhenValidCreds(t *testing.T) {
    p := t.TempDir() + "/creds.json"
    must := func(err error) { if err != nil { t.Fatal(err) } }
    must(agent.SaveCredentials(p, &agent.Credentials{
        ServerURL:    "https://example",
        AccessToken:  "tk",
        RefreshToken: "rt",
        ExpiresAt:    time.Now().Add(time.Hour),
    }))
    ac := NewAuthController(AuthConfig{
        ServerURL: "https://example", CredentialsPath: p,
    })
    if ac.State() != AuthLoggedIn {
        t.Errorf("state=%v want LoggedIn", ac.State())
    }
    tk, err := ac.EnsureValid(context.Background())
    if err != nil || tk != "tk" {
        t.Errorf("EnsureValid → %q %v want tk nil", tk, err)
    }
}

func TestAuth_LoginFlowSuccess(t *testing.T) {
    var states []AuthState
    var mu sync.Mutex
    ac := NewAuthController(AuthConfig{
        ServerURL:       "https://example",
        CredentialsPath: t.TempDir() + "/creds.json",
        OnChange: func(s AuthState) {
            mu.Lock(); states = append(states, s); mu.Unlock()
        },
        // Test seam: replace OAuth funcs.
        RequestDeviceCode: func(_ string) (*agent.DeviceAuthResponse, error) {
            return &agent.DeviceAuthResponse{
                DeviceCode:      "dc",
                UserCode:        "USER-CODE",
                VerificationURI: "https://example/verify",
                ExpiresIn:       60, Interval: 1,
            }, nil
        },
        PollForToken: func(_ string, _ *agent.DeviceAuthResponse) (*agent.TokenResponse, error) {
            return &agent.TokenResponse{AccessToken: "new", RefreshToken: "nr", ExpiresIn: 3600}, nil
        },
    })
    info, err := ac.StartLogin(context.Background())
    if err != nil {
        t.Fatal(err)
    }
    if info.UserCode != "USER-CODE" {
        t.Errorf("user_code=%q", info.UserCode)
    }
    // wait up to 1s for poll to complete and state to flip
    deadline := time.Now().Add(time.Second)
    for time.Now().Before(deadline) {
        if ac.State() == AuthLoggedIn { break }
        time.Sleep(10 * time.Millisecond)
    }
    if ac.State() != AuthLoggedIn {
        t.Errorf("state after poll = %v want LoggedIn", ac.State())
    }
    mu.Lock()
    if len(states) < 2 || states[0] != AuthLoggingIn {
        t.Errorf("state transitions = %v", states)
    }
    mu.Unlock()
}

func TestAuth_LoginFlowDenied(t *testing.T) {
    ac := NewAuthController(AuthConfig{
        ServerURL:       "https://example",
        CredentialsPath: t.TempDir() + "/creds.json",
        RequestDeviceCode: func(_ string) (*agent.DeviceAuthResponse, error) {
            return &agent.DeviceAuthResponse{DeviceCode: "dc", UserCode: "X", ExpiresIn: 60, Interval: 1}, nil
        },
        PollForToken: func(_ string, _ *agent.DeviceAuthResponse) (*agent.TokenResponse, error) {
            return nil, errors.New("authorization denied by user")
        },
    })
    _, _ = ac.StartLogin(context.Background())
    deadline := time.Now().Add(time.Second)
    for time.Now().Before(deadline) {
        if ac.State() == AuthLoggedOut { break }
        time.Sleep(10 * time.Millisecond)
    }
    if ac.State() != AuthLoggedOut {
        t.Errorf("state after deny = %v want LoggedOut", ac.State())
    }
}

func TestAuth_Logout_ClearsCreds(t *testing.T) {
    p := t.TempDir() + "/creds.json"
    _ = agent.SaveCredentials(p, &agent.Credentials{
        ServerURL: "https://example", AccessToken: "tk", ExpiresAt: time.Now().Add(time.Hour),
    })
    ac := NewAuthController(AuthConfig{ServerURL: "https://example", CredentialsPath: p})
    if ac.State() != AuthLoggedIn { t.Fatal("not logged in") }
    if err := ac.Logout(); err != nil { t.Fatal(err) }
    if ac.State() != AuthLoggedOut { t.Errorf("state=%v want LoggedOut", ac.State()) }
    // creds file should be gone
    if _, err := agent.LoadCredentials(p); err == nil {
        t.Errorf("expected creds file removed")
    }
}
```

- [ ] **Step 3: Verify fail**

Run: `go test ./internal/agent/tui/ -run TestAuth -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 4: Implement AuthController**

```go
// internal/agent/tui/auth.go
package tui

import (
    "context"
    "fmt"
    "os"
    "strings"
    "sync"
    "sync/atomic"
    "time"

    "github.com/agentserver/agentserver/internal/agent"
)

type AuthState int32

const (
    AuthLoggedOut AuthState = iota
    AuthLoggingIn
    AuthLoggedIn
    AuthRefreshing
)

func (s AuthState) String() string {
    switch s {
    case AuthLoggedOut: return "logged_out"
    case AuthLoggingIn: return "logging_in"
    case AuthLoggedIn: return "logged_in"
    case AuthRefreshing: return "refreshing"
    }
    return "unknown"
}

type AuthConfig struct {
    ServerURL       string
    CredentialsPath string
    SkipOpenBrowser bool
    OnChange        func(AuthState)

    // Test seams (default to real implementations from internal/agent/login.go)
    RequestDeviceCode func(serverURL string) (*agent.DeviceAuthResponse, error)
    PollForToken      func(serverURL string, dr *agent.DeviceAuthResponse) (*agent.TokenResponse, error)
}

type LoginInfo struct {
    UserCode        string
    VerifyURL       string
    VerifyURLFull   string
    ExpiresIn       int
}

type AuthController struct {
    cfg          AuthConfig
    state        atomic.Int32
    mu           sync.Mutex
    creds        *agent.Credentials
    cancelLogin  context.CancelFunc
    refreshMu    sync.Mutex
}

func NewAuthController(cfg AuthConfig) *AuthController {
    if cfg.RequestDeviceCode == nil { cfg.RequestDeviceCode = agent.RequestDeviceCode }
    if cfg.PollForToken == nil      { cfg.PollForToken = agent.PollForToken }
    if cfg.CredentialsPath == ""    { cfg.CredentialsPath = agent.DefaultCredentialsPath() }

    ac := &AuthController{cfg: cfg}
    creds, err := agent.LoadCredentials(cfg.CredentialsPath)
    if err == nil && creds != nil && time.Now().Before(creds.ExpiresAt.Add(-30*time.Second)) {
        ac.creds = creds
        ac.setState(AuthLoggedIn)
    } else {
        ac.setState(AuthLoggedOut)
    }
    return ac
}

func (a *AuthController) State() AuthState {
    return AuthState(a.state.Load())
}

func (a *AuthController) setState(s AuthState) {
    prev := AuthState(a.state.Swap(int32(s)))
    if prev != s && a.cfg.OnChange != nil {
        a.cfg.OnChange(s)
    }
}

// EnsureValid returns a non-empty access token or an error. If the token is
// near expiry it triggers a refresh (state → Refreshing). Refresh failure
// transitions to LoggedOut.
func (a *AuthController) EnsureValid(ctx context.Context) (string, error) {
    a.mu.Lock()
    creds := a.creds
    a.mu.Unlock()
    if creds == nil {
        return "", fmt.Errorf("not authenticated")
    }
    if time.Now().Before(creds.ExpiresAt.Add(-5 * time.Minute)) {
        return creds.AccessToken, nil
    }
    return a.refresh(ctx)
}

func (a *AuthController) refresh(ctx context.Context) (string, error) {
    a.refreshMu.Lock()
    defer a.refreshMu.Unlock()
    a.mu.Lock()
    creds := a.creds
    a.mu.Unlock()
    if creds == nil {
        a.setState(AuthLoggedOut)
        return "", fmt.Errorf("not authenticated")
    }
    if time.Now().Before(creds.ExpiresAt.Add(-5 * time.Minute)) {
        return creds.AccessToken, nil
    }
    a.setState(AuthRefreshing)
    // Use the existing TokenResponse exchange. Real refresh-token flow uses
    // POST /api/oauth2/token with grant_type=refresh_token.
    newCreds, err := agent.RefreshAccessToken(ctx, a.cfg.ServerURL, creds.RefreshToken)
    if err != nil {
        a.mu.Lock()
        a.creds = nil
        a.mu.Unlock()
        os.Remove(a.cfg.CredentialsPath)
        a.setState(AuthLoggedOut)
        return "", err
    }
    a.mu.Lock()
    a.creds = newCreds
    a.mu.Unlock()
    _ = agent.SaveCredentials(a.cfg.CredentialsPath, newCreds)
    a.setState(AuthLoggedIn)
    return newCreds.AccessToken, nil
}

// StartLogin kicks off OAuth Device Flow. Returns the user-visible code +
// URL synchronously; the polling loop runs in a goroutine and eventually
// transitions state to LoggedIn or LoggedOut.
func (a *AuthController) StartLogin(ctx context.Context) (LoginInfo, error) {
    if a.State() == AuthLoggedIn {
        return LoginInfo{}, fmt.Errorf("already logged in")
    }
    if a.State() == AuthLoggingIn {
        return LoginInfo{}, fmt.Errorf("login already in progress")
    }
    if a.cfg.ServerURL == "" {
        return LoginInfo{}, fmt.Errorf("--server is required for /login on first run")
    }
    a.setState(AuthLoggingIn)
    dr, err := a.cfg.RequestDeviceCode(a.cfg.ServerURL)
    if err != nil {
        a.setState(AuthLoggedOut)
        return LoginInfo{}, err
    }
    pollCtx, cancel := context.WithCancel(ctx)
    a.mu.Lock()
    a.cancelLogin = cancel
    a.mu.Unlock()
    go a.runPoll(pollCtx, dr)
    info := LoginInfo{
        UserCode:      dr.UserCode,
        VerifyURL:     dr.VerificationURI,
        VerifyURLFull: dr.VerificationURIComplete,
        ExpiresIn:     dr.ExpiresIn,
    }
    return info, nil
}

func (a *AuthController) runPoll(ctx context.Context, dr *agent.DeviceAuthResponse) {
    tr, err := a.cfg.PollForToken(a.cfg.ServerURL, dr)
    if ctx.Err() != nil {
        a.setState(AuthLoggedOut)
        return
    }
    if err != nil {
        a.setState(AuthLoggedOut)
        return
    }
    creds := &agent.Credentials{
        ServerURL:    a.cfg.ServerURL,
        AccessToken:  tr.AccessToken,
        RefreshToken: tr.RefreshToken,
        ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
        Scopes:       strings.Split(tr.Scope, " "),
    }
    if err := agent.SaveCredentials(a.cfg.CredentialsPath, creds); err != nil {
        a.setState(AuthLoggedOut)
        return
    }
    a.mu.Lock()
    a.creds = creds
    a.mu.Unlock()
    a.setState(AuthLoggedIn)
}

func (a *AuthController) CancelLogin() {
    a.mu.Lock()
    cancel := a.cancelLogin
    a.cancelLogin = nil
    a.mu.Unlock()
    if cancel != nil {
        cancel()
    }
    if a.State() == AuthLoggingIn {
        a.setState(AuthLoggedOut)
    }
}

func (a *AuthController) Logout() error {
    a.mu.Lock()
    a.creds = nil
    a.mu.Unlock()
    if err := os.Remove(a.cfg.CredentialsPath); err != nil && !os.IsNotExist(err) {
        return err
    }
    a.setState(AuthLoggedOut)
    return nil
}
```

- [ ] **Step 5: Add `agent.RefreshAccessToken` if missing**

Check `internal/agent/token_refresh.go` (exists per file listing). It probably exports something like `EnsureValidToken`. Add a thin wrapper if needed:

```go
// internal/agent/token_refresh.go (append if missing)
func RefreshAccessToken(ctx context.Context, serverURL, refreshToken string) (*Credentials, error) {
    // Same shape as PollForToken's success path, but with grant_type=refresh_token.
    form := url.Values{
        "grant_type":    {"refresh_token"},
        "client_id":     {defaultClientID},
        "refresh_token": {refreshToken},
    }
    req, _ := http.NewRequestWithContext(ctx, "POST",
        strings.TrimRight(serverURL, "/")+"/api/oauth2/token",
        strings.NewReader(form.Encode()))
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    resp, err := http.DefaultClient.Do(req)
    if err != nil { return nil, err }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("refresh failed (%d)", resp.StatusCode)
    }
    var tr TokenResponse
    if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil { return nil, err }
    return &Credentials{
        ServerURL:    serverURL,
        AccessToken:  tr.AccessToken,
        RefreshToken: tr.RefreshToken,
        ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
        Scopes:       strings.Split(tr.Scope, " "),
    }, nil
}
```

If `internal/agent/token_refresh.go` already has equivalent, expose it under this name.

- [ ] **Step 6: Verify tests pass**

Run: `go test ./internal/agent/tui/ -run TestAuth -v`
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/tui/auth.go internal/agent/tui/auth_test.go internal/agent/token_refresh.go
git commit -m "feat(tui): AuthController state machine + login/logout/refresh"
```

---

## Task 3: Bus — HTTP client wrapping all agentserver TUI endpoints

**Files:**
- Create: `internal/agent/tui/bus.go`
- Create: `internal/agent/tui/bus_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/agent/tui/bus_test.go
package tui

import (
    "context"
    "encoding/json"
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"
)

type fakeAuth struct{ tk string }
func (f *fakeAuth) EnsureValid(_ context.Context) (string, error) { return f.tk, nil }

func TestBus_PostInbound(t *testing.T) {
    var receivedAuth, receivedBody string
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        receivedAuth = r.Header.Get("Authorization")
        body, _ := io.ReadAll(r.Body)
        receivedBody = string(body)
        w.WriteHeader(http.StatusAccepted)
        w.Write([]byte(`{"session_id":"cse_x","turn_id":"trn_y"}`))
    }))
    defer srv.Close()
    bus := NewBus(BusConfig{
        ServerURL:   srv.URL,
        WorkspaceID: "ws_test",
        ExecutorID:  "exe_a",
        Auth:        &fakeAuth{tk: "TKN"},
    })
    out, err := bus.PostInbound(context.Background(), InboundRequest{
        Text: "hello", PermissionResponder: true,
    })
    if err != nil { t.Fatal(err) }
    if out.SessionID != "cse_x" || out.TurnID != "trn_y" {
        t.Errorf("response %+v", out)
    }
    if receivedAuth != "Bearer TKN" {
        t.Errorf("auth header = %q", receivedAuth)
    }
    var parsed map[string]any
    json.Unmarshal([]byte(receivedBody), &parsed)
    if parsed["text"] != "hello" || parsed["executor_id"] != "exe_a" {
        t.Errorf("body = %s", receivedBody)
    }
}

func TestBus_PostInbound_Returns409OnTurnInProgress(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusConflict)
        w.Write([]byte(`{"error":{"code":"turn_in_progress","message":"x"}}`))
    }))
    defer srv.Close()
    bus := NewBus(BusConfig{ServerURL: srv.URL, WorkspaceID: "ws", ExecutorID: "e", Auth: &fakeAuth{tk: "t"}})
    _, err := bus.PostInbound(context.Background(), InboundRequest{Text: "x"})
    var apiErr *APIError
    if !errorsAs(err, &apiErr) || apiErr.Code != "turn_in_progress" {
        t.Errorf("err = %v want APIError{turn_in_progress}", err)
    }
}

func TestBus_PostDecision(t *testing.T) {
    var path, body string
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        path = r.URL.Path
        bb, _ := io.ReadAll(r.Body); body = string(bb)
        w.WriteHeader(200); w.Write([]byte(`{"accepted":true}`))
    }))
    defer srv.Close()
    bus := NewBus(BusConfig{ServerURL: srv.URL, WorkspaceID: "ws", ExecutorID: "exe_a", Auth: &fakeAuth{tk: "t"}})
    err := bus.PostDecision(context.Background(), "cse_1", "perm_p1", "allow", "always")
    if err != nil { t.Fatal(err) }
    if !strings.HasSuffix(path, "/permissions/perm_p1") {
        t.Errorf("path = %q", path)
    }
    if !strings.Contains(body, `"decision":"allow"`) || !strings.Contains(body, `"scope":"always"`) {
        t.Errorf("body = %s", body)
    }
    if !strings.Contains(body, `"responder_executor_id":"exe_a"`) {
        t.Errorf("responder id missing in body: %s", body)
    }
}

func TestBus_FetchExecutorStatus(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        w.Write([]byte(`{"executor_id":"exe_a","status":"online"}`))
    }))
    defer srv.Close()
    bus := NewBus(BusConfig{ServerURL: srv.URL, WorkspaceID: "ws", ExecutorID: "exe_a", Auth: &fakeAuth{tk: "t"}})
    st, err := bus.FetchExecutorStatus(context.Background())
    if err != nil { t.Fatal(err) }
    if st.Status != "online" {
        t.Errorf("status = %q", st.Status)
    }
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }
```

(Add `import "errors"` and rename helper if you prefer plain `errors.As`.)

- [ ] **Step 2: Verify fail**

Run: `go test ./internal/agent/tui/ -run TestBus -v`
Expected: FAIL.

- [ ] **Step 3: Implement Bus**

```go
// internal/agent/tui/bus.go
package tui

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strconv"
    "time"
)

type AuthSource interface {
    EnsureValid(ctx context.Context) (string, error)
}

type BusConfig struct {
    ServerURL   string
    WorkspaceID string
    ExecutorID  string
    Auth        AuthSource
    HTTP        *http.Client  // optional; defaults to 30s timeout client
}

type Bus struct {
    cfg  BusConfig
    http *http.Client
}

func NewBus(cfg BusConfig) *Bus {
    h := cfg.HTTP
    if h == nil {
        h = &http.Client{Timeout: 30 * time.Second}
    }
    return &Bus{cfg: cfg, http: h}
}

type APIError struct {
    HTTPStatus int
    Code       string `json:"code"`
    Message    string `json:"message"`
}

func (e *APIError) Error() string {
    return fmt.Sprintf("api: %s (HTTP %d): %s", e.Code, e.HTTPStatus, e.Message)
}

func (b *Bus) do(ctx context.Context, method, path string, body any, out any) error {
    tk, err := b.cfg.Auth.EnsureValid(ctx)
    if err != nil { return err }
    var bodyReader io.Reader
    if body != nil {
        raw, _ := json.Marshal(body)
        bodyReader = bytes.NewReader(raw)
    }
    req, err := http.NewRequestWithContext(ctx, method, b.cfg.ServerURL+path, bodyReader)
    if err != nil { return err }
    req.Header.Set("Authorization", "Bearer "+tk)
    if body != nil {
        req.Header.Set("Content-Type", "application/json")
    }
    resp, err := b.http.Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode >= 400 {
        var wrap struct{ Error APIError `json:"error"` }
        bb, _ := io.ReadAll(resp.Body)
        json.Unmarshal(bb, &wrap)
        wrap.Error.HTTPStatus = resp.StatusCode
        if wrap.Error.Code == "" {
            wrap.Error.Code = fmt.Sprintf("http_%d", resp.StatusCode)
            wrap.Error.Message = string(bb)
        }
        return &wrap.Error
    }
    if out != nil {
        return json.NewDecoder(resp.Body).Decode(out)
    }
    return nil
}

// ---- POST /api/workspaces/{wid}/tui/inbound ----

type InboundRequest struct {
    SessionID           string                 `json:"session_id,omitempty"`
    Text                string                 `json:"text"`
    Attachments         []InboundAttachment    `json:"attachments,omitempty"`
    Metadata            map[string]any         `json:"metadata,omitempty"`
    PermissionResponder bool                   `json:"permission_responder,omitempty"`
}

type InboundAttachment struct {
    Kind       string `json:"kind"`
    Filename   string `json:"filename"`
    Size       int    `json:"size"`
    ContentB64 string `json:"content_b64"`
}

type InboundResponse struct {
    SessionID    string `json:"session_id"`
    TurnID       string `json:"turn_id"`
    NextEventSeq int64  `json:"next_event_seq"`
}

func (b *Bus) PostInbound(ctx context.Context, in InboundRequest) (*InboundResponse, error) {
    in2 := struct {
        InboundRequest
        ExecutorID string `json:"executor_id"`
    }{InboundRequest: in, ExecutorID: b.cfg.ExecutorID}
    var out InboundResponse
    err := b.do(ctx, "POST",
        fmt.Sprintf("/api/workspaces/%s/tui/inbound", b.cfg.WorkspaceID),
        in2, &out)
    return &out, err
}

// ---- POST /api/agent-sessions ----

func (b *Bus) NewSession(ctx context.Context, permissionMode string, preferredExecutorID string) (string, error) {
    var out struct{ SessionID string `json:"session_id"` }
    err := b.do(ctx, "POST", "/api/agent-sessions", map[string]any{
        "workspace_id":          b.cfg.WorkspaceID,
        "executor_id":           b.cfg.ExecutorID,
        "permission_mode":       permissionMode,
        "preferred_executor_id": preferredExecutorID,
    }, &out)
    return out.SessionID, err
}

// ---- POST /api/agent-sessions/{sid}/attach ----

type AttachResponse struct {
    SessionID         string  `json:"session_id"`
    PermResponder     *string `json:"permission_responder"`
    PreviousResponder string  `json:"previous_responder"`
    PreviousPreferred string  `json:"previous_preferred"`
}

func (b *Bus) AttachSession(ctx context.Context, sid, mode string) (*AttachResponse, error) {
    var out AttachResponse
    err := b.do(ctx, "POST", fmt.Sprintf("/api/agent-sessions/%s/attach", sid),
        map[string]any{
            "executor_id":             b.cfg.ExecutorID,
            "mode":                    mode,
            "as_permission_responder": mode == "operator",
            "also_become_preferred":   mode == "operator",
        }, &out)
    return &out, err
}

// ---- GET /api/agent-sessions ----

type SessionListItem struct {
    SessionID         string  `json:"session_id"`
    ExternalID        string  `json:"external_id"`
    Title             string  `json:"title"`
    LastActivityAt    string  `json:"last_activity_at"`
    PermissionResponder *string `json:"permission_responder"`
}

func (b *Bus) ListSessions(ctx context.Context) ([]SessionListItem, error) {
    q := url.Values{}
    q.Set("workspace_id", b.cfg.WorkspaceID)
    q.Set("channel_type", "tui")
    q.Set("executor_id", b.cfg.ExecutorID)
    q.Set("latest", "20")
    var out struct{ Sessions []SessionListItem `json:"sessions"` }
    err := b.do(ctx, "GET", "/api/agent-sessions?"+q.Encode(), nil, &out)
    return out.Sessions, err
}

// ---- POST /api/agent-sessions/{sid}/control ----

func (b *Bus) PostControl(ctx context.Context, sid, command string, args map[string]any) (json.RawMessage, error) {
    var out json.RawMessage
    err := b.do(ctx, "POST", fmt.Sprintf("/api/agent-sessions/%s/control", sid),
        map[string]any{"command": command, "args": args}, &out)
    return out, err
}

// ---- POST /api/agent-sessions/{sid}/turns/{tid}/cancel ----

func (b *Bus) PostCancel(ctx context.Context, sid, tid string) error {
    return b.do(ctx, "POST",
        fmt.Sprintf("/api/agent-sessions/%s/turns/%s/cancel", sid, tid),
        struct{}{}, nil)
}

// ---- POST /api/agent-sessions/{sid}/permissions/{pid} ----

func (b *Bus) PostDecision(ctx context.Context, sid, pid, decision, scope string) error {
    return b.do(ctx, "POST",
        fmt.Sprintf("/api/agent-sessions/%s/permissions/%s", sid, pid),
        map[string]any{
            "decision":              decision,
            "scope":                 scope,
            "responder_executor_id": b.cfg.ExecutorID,
        }, nil)
}

// ---- GET /api/executors/{id}/status ----

type ExecutorStatusResp struct {
    ExecutorID    string `json:"executor_id"`
    Status        string `json:"status"`
    LastHeartbeat string `json:"last_heartbeat_at"`
}

func (b *Bus) FetchExecutorStatus(ctx context.Context) (*ExecutorStatusResp, error) {
    var out ExecutorStatusResp
    err := b.do(ctx, "GET", "/api/executors/"+b.cfg.ExecutorID+"/status", nil, &out)
    return &out, err
}

// ---- Misc ----

func (b *Bus) ServerURL() string  { return b.cfg.ServerURL }
func (b *Bus) ExecutorID() string { return b.cfg.ExecutorID }

// Used by SSE consumer (Task 4) for token + URL access.
func (b *Bus) AccessToken(ctx context.Context) (string, error) {
    return b.cfg.Auth.EnsureValid(ctx)
}

func parseSeq(s string) int64 { v, _ := strconv.ParseInt(s, 10, 64); return v }
var _ = errors.New  // keep import
```

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/agent/tui/ -run TestBus -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tui/bus.go internal/agent/tui/bus_test.go
git commit -m "feat(tui): Bus HTTP client wrapping all agentserver TUI endpoints"
```

---

## Task 4: SSE consumer with Last-Event-ID reconnect

**Files:**
- Create: `internal/agent/tui/sse.go`
- Create: `internal/agent/tui/sse_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/agent/tui/sse_test.go
package tui

import (
    "context"
    "fmt"
    "net/http"
    "net/http/httptest"
    "sync/atomic"
    "testing"
    "time"
)

func TestSSE_ReceivesEvents(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        w.Header().Set("Content-Type", "text/event-stream")
        f := w.(http.Flusher)
        for i := 1; i <= 3; i++ {
            fmt.Fprintf(w, "id: %d\nevent: tool_use\ndata: {\"n\":%d}\n\n", i, i)
            f.Flush()
        }
    }))
    defer srv.Close()
    bus := NewBus(BusConfig{ServerURL: srv.URL, ExecutorID: "exe_a", WorkspaceID: "ws", Auth: &fakeAuth{tk: "t"}})
    sub := NewSSEConsumer(bus, SSEConfig{SessionID: "cse_x"})
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    ch := sub.Run(ctx)

    received := []SSEEvent{}
    deadline := time.After(time.Second)
loop:
    for len(received) < 3 {
        select {
        case ev, ok := <-ch:
            if !ok { break loop }
            received = append(received, ev)
        case <-deadline:
            break loop
        }
    }
    if len(received) != 3 {
        t.Fatalf("got %d events", len(received))
    }
    if received[2].Type != "tool_use" || received[2].LastEventID != "3" {
        t.Errorf("event[2] = %+v", received[2])
    }
}

func TestSSE_SendsLastEventIDOnReconnect(t *testing.T) {
    var hits atomic.Int32
    var lastID atomic.Value  // string
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        n := hits.Add(1)
        lastID.Store(r.Header.Get("Last-Event-ID"))
        w.Header().Set("Content-Type", "text/event-stream")
        f := w.(http.Flusher)
        if n == 1 {
            fmt.Fprintf(w, "id: 7\nevent: x\ndata: {}\n\n")
            f.Flush()
            // close immediately to force reconnect
            return
        }
        fmt.Fprintf(w, "id: 8\nevent: x\ndata: {}\n\n")
        f.Flush()
    }))
    defer srv.Close()
    bus := NewBus(BusConfig{ServerURL: srv.URL, ExecutorID: "e", WorkspaceID: "w", Auth: &fakeAuth{tk: "t"}})
    sub := NewSSEConsumer(bus, SSEConfig{SessionID: "cse", InitialBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond})
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
    defer cancel()
    ch := sub.Run(ctx)
    // drain 2 events (one per connection)
    var got []SSEEvent
    deadline := time.After(time.Second)
    for len(got) < 2 {
        select {
        case ev, ok := <-ch:
            if !ok { break }
            got = append(got, ev)
        case <-deadline:
            t.Fatalf("got %d events", len(got))
        }
    }
    if v, _ := lastID.Load().(string); v != "7" {
        t.Errorf("Last-Event-ID on reconnect = %q want 7", v)
    }
}
```

- [ ] **Step 2: Verify fail**

Run: `go test ./internal/agent/tui/ -run TestSSE -v`
Expected: FAIL.

- [ ] **Step 3: Implement SSEConsumer**

```go
// internal/agent/tui/sse.go
package tui

import (
    "bufio"
    "context"
    "fmt"
    "io"
    "net/http"
    "strings"
    "time"
)

type SSEEvent struct {
    Type        string
    Data        []byte
    LastEventID string
    Retry       time.Duration
}

type SSEConfig struct {
    SessionID      string
    InitialBackoff time.Duration  // default 1s
    MaxBackoff     time.Duration  // default 30s
    HTTP           *http.Client   // optional; default infinite Read deadline
}

type SSEConsumer struct {
    bus    *Bus
    cfg    SSEConfig
    lastID string
}

func NewSSEConsumer(bus *Bus, cfg SSEConfig) *SSEConsumer {
    if cfg.InitialBackoff == 0 { cfg.InitialBackoff = time.Second }
    if cfg.MaxBackoff == 0     { cfg.MaxBackoff = 30 * time.Second }
    return &SSEConsumer{bus: bus, cfg: cfg}
}

func (s *SSEConsumer) Run(ctx context.Context) <-chan SSEEvent {
    out := make(chan SSEEvent, 64)
    go func() {
        defer close(out)
        backoff := s.cfg.InitialBackoff
        for {
            if ctx.Err() != nil { return }
            err := s.connectOnce(ctx, out)
            if ctx.Err() != nil { return }
            _ = err
            // Reconnect with backoff
            select {
            case <-ctx.Done(): return
            case <-time.After(backoff):
            }
            backoff *= 2
            if backoff > s.cfg.MaxBackoff { backoff = s.cfg.MaxBackoff }
        }
    }()
    return out
}

func (s *SSEConsumer) connectOnce(ctx context.Context, out chan<- SSEEvent) error {
    tk, err := s.bus.AccessToken(ctx)
    if err != nil { return err }
    url := s.bus.ServerURL() + "/api/agent-sessions/" + s.cfg.SessionID + "/events"
    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil { return err }
    req.Header.Set("Accept", "text/event-stream")
    req.Header.Set("Authorization", "Bearer "+tk)
    if s.lastID != "" {
        req.Header.Set("Last-Event-ID", s.lastID)
    }
    client := s.cfg.HTTP
    if client == nil {
        client = &http.Client{}  // no timeout — SSE is long-lived
    }
    resp, err := client.Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        return fmt.Errorf("sse status %d", resp.StatusCode)
    }
    sc := bufio.NewScanner(resp.Body)
    sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
    var (
        ev   SSEEvent
        data []byte
    )
    for sc.Scan() {
        line := sc.Text()
        switch {
        case line == "":
            if ev.Type != "" || len(data) > 0 {
                ev.Data = data
                if ev.LastEventID != "" {
                    s.lastID = ev.LastEventID
                }
                select {
                case out <- ev:
                case <-ctx.Done():
                    return ctx.Err()
                }
            }
            ev = SSEEvent{}
            data = nil
        case strings.HasPrefix(line, ":"):
            // comment/keepalive — ignore
        case strings.HasPrefix(line, "event: "):
            ev.Type = strings.TrimPrefix(line, "event: ")
        case strings.HasPrefix(line, "id: "):
            ev.LastEventID = strings.TrimPrefix(line, "id: ")
        case strings.HasPrefix(line, "data: "):
            if len(data) > 0 { data = append(data, '\n') }
            data = append(data, []byte(strings.TrimPrefix(line, "data: "))...)
        case strings.HasPrefix(line, "retry: "):
            // ignore for v1
        }
    }
    if err := sc.Err(); err != nil && err != io.EOF {
        return err
    }
    return nil
}
```

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/agent/tui/ -run TestSSE -v -timeout 10s`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tui/sse.go internal/agent/tui/sse_test.go
git commit -m "feat(tui): SSE consumer with Last-Event-ID reconnect"
```

---

## Task 5: Bubble Tea Msg types

**Files:**
- Create: `internal/agent/tui/msg.go`

- [ ] **Step 1: Create msg.go**

```go
// internal/agent/tui/msg.go
package tui

import (
    "encoding/json"
)

// SSE-driven
type EventArrivedMsg struct{ Event SSEEvent }
type SSEStatusMsg    struct{ Status string; Reason string } // live | reconnecting | delayed

// HTTP-driven (Bus replies)
type InboundAcceptedMsg struct{ SessionID, TurnID string }
type InboundRejectedMsg struct{ Code, Message string }
type ControlReplyMsg    struct{ Command string; Body json.RawMessage; Err error }
type CancelReplyMsg     struct{ Err error }
type DecisionAckMsg     struct{ PermissionID string; Err error }
type AttachReplyMsg     struct{ Resp *AttachResponse; Err error }
type NewSessionReplyMsg struct{ SessionID string; Err error }
type ListSessionsReplyMsg struct{ Sessions []SessionListItem; Err error }

// Periodic
type StatusTickMsg     struct{ Tunnel *ExecutorStatusResp; Err error }
type InitialStateMsg   struct{ SessionID string; Model string; PermMode string }

// Auth
type AuthStateChangedMsg struct{ State AuthState }
type DeviceCodeReadyMsg  struct{ Info LoginInfo }
type LoginPollDoneMsg    struct{ Err error }
type LogoutDoneMsg       struct{ Err error }

// Internal user actions
type SendPromptMsg          struct{ Text string; Attachments []InboundAttachment; Metadata map[string]any }
type AttachmentPickedMsg    struct{ Attachment InboundAttachment }
type AttachmentRemovedMsg   struct{ Index int }
type CommandSelectedMsg     struct{ Command, Args string }
type ResumeRequestedMsg     struct{ SessionID string }
type ClearRequestedMsg      struct{}

// Fatal
type FatalErrorMsg struct{ Err error }
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/agent/tui/...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tui/msg.go
git commit -m "feat(tui): Bubble Tea Msg types"
```

---

## Task 6: Timeline data structure + per-event renderer types

**Files:**
- Create: `internal/agent/tui/timeline.go`
- Create: `internal/agent/tui/timeline_test.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/agent/tui/timeline_test.go
package tui

import (
    "encoding/json"
    "strings"
    "testing"
)

func TestTimeline_AppendAndRender_BasicEvents(t *testing.T) {
    tl := NewTimeline(100)
    tl.Append(SSEEvent{Type: "user_message", Data: []byte(`{"text":"hi"}`), LastEventID: "1"})
    tl.Append(SSEEvent{Type: "assistant_message", Data: []byte(`{"text":"hello"}`), LastEventID: "2"})
    tl.Append(SSEEvent{Type: "tool_use", Data: []byte(`{"tool_use_id":"tu1","tool":"remote_bash","executor_id":"exe_a","args":{"command":"ls"}}`), LastEventID: "3"})
    tl.Append(SSEEvent{Type: "tool_result", Data: []byte(`{"tool_use_id":"tu1","output":"a\nb","exit_code":0}`), LastEventID: "4"})
    out := tl.Render(80, "exe_a")
    if !strings.Contains(out, "hi") || !strings.Contains(out, "hello") {
        t.Errorf("missing text: %s", out)
    }
    if !strings.Contains(out, "remote_bash") {
        t.Errorf("missing tool name: %s", out)
    }
    if !strings.Contains(out, "executed locally") {
        t.Errorf("local executor tag missing: %s", out)
    }
}

func TestTimeline_NonLocalExecutorTagged(t *testing.T) {
    tl := NewTimeline(100)
    tl.Append(SSEEvent{Type: "tool_use", Data: []byte(`{"tool_use_id":"tu","tool":"remote_bash","executor_id":"exe_other","args":{}}`), LastEventID: "1"})
    out := tl.Render(80, "exe_self")
    if strings.Contains(out, "executed locally") {
        t.Errorf("should NOT tag locally for non-self executor")
    }
    if !strings.Contains(out, "exe_other") {
        t.Errorf("should label foreign executor: %s", out)
    }
}

func TestTimeline_DropsOldestWhenOverCap(t *testing.T) {
    tl := NewTimeline(3)
    for i := 1; i <= 5; i++ {
        tl.Append(SSEEvent{Type: "user_message",
            Data: []byte(`{"text":"msg`+string(rune('0'+i))+`"}`),
            LastEventID: "x"})
    }
    if tl.Len() != 3 {
        t.Errorf("len=%d want 3", tl.Len())
    }
    out := tl.Render(80, "")
    if strings.Contains(out, "msg1") || strings.Contains(out, "msg2") {
        t.Errorf("oldest items not dropped: %s", out)
    }
    if !strings.Contains(out, "msg5") {
        t.Errorf("newest missing: %s", out)
    }
}

func TestTimeline_PermissionResolvedReplacesRequestState(t *testing.T) {
    tl := NewTimeline(100)
    tl.Append(SSEEvent{Type: "permission_request",
        Data: []byte(`{"permission_id":"p1","tool":"remote_bash"}`),
        LastEventID: "1"})
    tl.Append(SSEEvent{Type: "permission_resolved",
        Data: []byte(`{"permission_id":"p1","decision":{"verdict":"allow","scope":"once"}}`),
        LastEventID: "2"})
    out := tl.Render(80, "")
    if !strings.Contains(out, "allow") {
        t.Errorf("resolved decision not visible: %s", out)
    }
    // verify both items present (request → "✓ allowed" annotation in v1)
    if strings.Count(out, "p1") < 1 {
        t.Errorf("expected at least one mention of perm id: %s", out)
    }
}

func mustMarshal(v any) []byte { b, _ := json.Marshal(v); return b }
```

- [ ] **Step 2: Verify fail**

Run: `go test ./internal/agent/tui/ -run TestTimeline -v`
Expected: FAIL.

- [ ] **Step 3: Implement Timeline**

```go
// internal/agent/tui/timeline.go
package tui

import (
    "encoding/json"
    "fmt"
    "strings"
    "sync"

    "github.com/charmbracelet/lipgloss"
)

// TimelineItem is one rendered row (or block) in the message stream.
type TimelineItem struct {
    EventID    string
    EventType  string
    Payload    json.RawMessage
    Resolution map[string]any  // for permission_request items, populated when resolved
}

type Timeline struct {
    mu      sync.Mutex
    items   []TimelineItem
    cap     int
    indexBy map[string]int  // event_id → index (for permission_request linkage)
    permIdx map[string]int  // permission_id → timeline index
}

func NewTimeline(cap int) *Timeline {
    return &Timeline{
        cap:     cap,
        indexBy: map[string]int{},
        permIdx: map[string]int{},
    }
}

func (t *Timeline) Len() int {
    t.mu.Lock(); defer t.mu.Unlock()
    return len(t.items)
}

func (t *Timeline) Append(ev SSEEvent) {
    t.mu.Lock()
    defer t.mu.Unlock()
    item := TimelineItem{
        EventID:   ev.LastEventID,
        EventType: ev.Type,
        Payload:   ev.Data,
    }
    // Special handling: permission_resolved updates a prior permission_request
    if ev.Type == "permission_resolved" {
        var p struct {
            PermissionID string         `json:"permission_id"`
            Decision     map[string]any `json:"decision"`
        }
        json.Unmarshal(ev.Data, &p)
        if idx, ok := t.permIdx[p.PermissionID]; ok && idx < len(t.items) {
            t.items[idx].Resolution = p.Decision
            return  // don't add a separate row; the request's resolution is enough
        }
    }
    t.items = append(t.items, item)
    if ev.LastEventID != "" {
        t.indexBy[ev.LastEventID] = len(t.items) - 1
    }
    if ev.Type == "permission_request" {
        var p struct{ PermissionID string `json:"permission_id"` }
        json.Unmarshal(ev.Data, &p)
        if p.PermissionID != "" {
            t.permIdx[p.PermissionID] = len(t.items) - 1
        }
    }
    if len(t.items) > t.cap {
        drop := len(t.items) - t.cap
        t.items = append([]TimelineItem(nil), t.items[drop:]...)
        // rebuild indexes
        t.indexBy = map[string]int{}
        t.permIdx = map[string]int{}
        for i, it := range t.items {
            if it.EventID != "" { t.indexBy[it.EventID] = i }
            if it.EventType == "permission_request" {
                var p struct{ PermissionID string `json:"permission_id"` }
                json.Unmarshal(it.Payload, &p)
                if p.PermissionID != "" { t.permIdx[p.PermissionID] = i }
            }
        }
    }
}

// Render produces a single multi-line string ready for the viewport.
// selfExecID is the local executor's id; tool_use rows tagged "executed locally"
// when their executor_id matches.
func (t *Timeline) Render(width int, selfExecID string) string {
    t.mu.Lock()
    items := append([]TimelineItem(nil), t.items...)
    t.mu.Unlock()
    var b strings.Builder
    for _, it := range items {
        b.WriteString(renderItem(it, selfExecID))
        b.WriteByte('\n')
    }
    return b.String()
    _ = width
}

var (
    styleUser     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7AB7FF"))
    styleAssist   = lipgloss.NewStyle().Foreground(lipgloss.Color("#B8E07F"))
    styleTool     = lipgloss.NewStyle().Foreground(lipgloss.Color("#D7D7AF"))
    styleResult   = lipgloss.NewStyle().Foreground(lipgloss.Color("#AFAFD7"))
    styleSystem   = lipgloss.NewStyle().Faint(true)
    styleErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF7A7A"))
    styleLocalTag = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FFF87")).Italic(true)
)

func renderItem(it TimelineItem, selfExecID string) string {
    switch it.EventType {
    case "user_message":
        var p struct{ Text string `json:"text"` }
        json.Unmarshal(it.Payload, &p)
        return styleUser.Render("▸ user") + "\n  " + p.Text
    case "assistant_message":
        var p struct{ Text string `json:"text"` }
        json.Unmarshal(it.Payload, &p)
        return styleAssist.Render("▸ assistant") + "\n  " + p.Text
    case "tool_use":
        var p struct {
            Tool       string         `json:"tool"`
            ExecutorID string         `json:"executor_id"`
            Args       map[string]any `json:"args"`
        }
        json.Unmarshal(it.Payload, &p)
        tag := "→ " + p.ExecutorID
        if p.ExecutorID == selfExecID && selfExecID != "" {
            tag = styleLocalTag.Render("→ executed locally")
        }
        argsBrief := briefArgs(p.Args)
        return styleTool.Render(fmt.Sprintf("▸ tool_use  %s  %s", p.Tool, tag)) + "\n  " + argsBrief
    case "tool_result":
        var p struct {
            Output   string `json:"output"`
            ExitCode int    `json:"exit_code"`
            IsError  bool   `json:"is_error"`
        }
        json.Unmarshal(it.Payload, &p)
        mark := "✓"
        st := styleResult
        if p.IsError || p.ExitCode != 0 {
            mark = "✗"
            st = styleErr
        }
        outBrief := briefOutput(p.Output)
        return st.Render(fmt.Sprintf("▸ tool_result  %s", mark)) + "\n  " + outBrief
    case "permission_request":
        var p struct {
            PermissionID string `json:"permission_id"`
            Tool         string `json:"tool"`
            ExecutorID   string `json:"executor_id"`
        }
        json.Unmarshal(it.Payload, &p)
        suffix := ""
        if it.Resolution != nil {
            verdict, _ := it.Resolution["verdict"].(string)
            scope, _ := it.Resolution["scope"].(string)
            suffix = styleSystem.Render(fmt.Sprintf("  (%s, %s)", verdict, scope))
        }
        return styleSystem.Render(fmt.Sprintf("▸ permission_request %s %s on %s", p.PermissionID, p.Tool, p.ExecutorID)) + suffix
    case "turn_done", "turn_started", "turn_cancelled":
        return styleSystem.Render(fmt.Sprintf("— %s —", it.EventType))
    case "compaction":
        return styleSystem.Render("─── context compacted ───")
    case "send_message":
        var p struct{ Text string `json:"text"` }
        json.Unmarshal(it.Payload, &p)
        return styleAssist.Render("▸ assistant (im)") + "\n  " + p.Text
    case "send_image":
        return styleSystem.Render("▸ image attached (download or render with terminal protocol — v1 stub)")
    case "send_file":
        var p struct{ Filename string `json:"filename"` }
        json.Unmarshal(it.Payload, &p)
        return styleSystem.Render("▸ file: " + p.Filename)
    case "ask_user":
        return styleSystem.Render("▸ ask_user — answer in panel")
    case "permission_responder_lost", "permission_responder_changed":
        return styleSystem.Render("⚠ control transferred")
    default:
        return styleSystem.Render("▸ " + it.EventType)
    }
}

func briefArgs(m map[string]any) string {
    if len(m) == 0 { return "{}" }
    b, _ := json.Marshal(m)
    s := string(b)
    if len(s) > 200 {
        s = s[:200] + "…"
    }
    return s
}

func briefOutput(s string) string {
    lines := strings.Split(s, "\n")
    if len(lines) > 8 {
        lines = append(lines[:8], "… (truncated)")
    }
    return strings.Join(lines, "\n  ")
}
```

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/agent/tui/ -run TestTimeline -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tui/timeline.go internal/agent/tui/timeline_test.go
git commit -m "feat(tui): Timeline data structure + per-event renderers"
```

---

## Task 7: lipgloss styles + Permission panel

**Files:**
- Create: `internal/agent/tui/styles.go`
- Create: `internal/agent/tui/panels.go`
- Create: `internal/agent/tui/panels_test.go`

- [ ] **Step 1: Write failing tests for permission panel**

```go
// internal/agent/tui/panels_test.go
package tui

import (
    "encoding/json"
    "strings"
    "testing"

    tea "github.com/charmbracelet/bubbletea"
)

func TestPermissionPanel_RendersFields(t *testing.T) {
    p := NewPermissionPanel(PermissionPanelInput{
        PID:        "perm_p1",
        Tool:       "remote_bash",
        ExecutorID: "exe_a",
        SelfExecID: "exe_a",
        Args:       json.RawMessage(`{"command":"git diff"}`),
    })
    out := p.View(80)
    if !strings.Contains(out, "perm_p1") || !strings.Contains(out, "remote_bash") {
        t.Errorf("missing fields: %s", out)
    }
    if !strings.Contains(out, "this machine") {
        t.Errorf("self exec hint missing: %s", out)
    }
    if !strings.Contains(out, "git diff") {
        t.Errorf("args not shown: %s", out)
    }
}

func TestPermissionPanel_KeysProduceCorrectOutcome(t *testing.T) {
    p := NewPermissionPanel(PermissionPanelInput{
        PID: "p1", Tool: "remote_bash", ExecutorID: "e", SelfExecID: "e",
        Args: json.RawMessage(`{}`),
    })
    cases := []struct {
        key      string
        wantVerd string
        wantScope string
    }{
        {"y", "allow", "once"},
        {"a", "allow", "always"},
        {"n", "deny", "once"},
        {"enter", "deny", "once"},
    }
    for _, c := range cases {
        var msg tea.KeyMsg
        switch c.key {
        case "enter": msg = tea.KeyMsg{Type: tea.KeyEnter}
        default:      msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(c.key)}
        }
        np, cmd, dismissed := p.HandleKey(msg)
        if !dismissed {
            t.Errorf("key %q should dismiss panel", c.key)
            continue
        }
        // The cmd should resolve to a SendDecisionMsg-like outcome we can inspect.
        out := cmd()
        sd, ok := out.(SendDecisionMsg)
        if !ok {
            t.Errorf("key %q produced %T", c.key, out)
            continue
        }
        if sd.Verdict != c.wantVerd || sd.Scope != c.wantScope {
            t.Errorf("key %q produced verdict=%q scope=%q want %q %q",
                c.key, sd.Verdict, sd.Scope, c.wantVerd, c.wantScope)
        }
        _ = np
    }
}

func TestPermissionPanel_DisablesAlwaysOnNestedShell(t *testing.T) {
    // bash -c / sh -c — TUI should hide / mark always disabled (spec §10)
    p := NewPermissionPanel(PermissionPanelInput{
        PID: "p1", Tool: "remote_bash", ExecutorID: "e", SelfExecID: "e",
        Args: json.RawMessage(`{"command":"bash -c \"rm -rf /\""}`),
    })
    out := p.View(80)
    if !strings.Contains(out, "always disabled") {
        t.Errorf("nested shell warning missing: %s", out)
    }
    msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
    _, cmd, dismissed := p.HandleKey(msg)
    if dismissed {
        t.Errorf("'a' must NOT dismiss when always is disabled")
    }
    if cmd != nil {
        t.Errorf("'a' must produce no cmd when always is disabled")
    }
}
```

- [ ] **Step 2: Verify fail**

Run: `go test ./internal/agent/tui/ -run TestPermissionPanel -v`
Expected: FAIL.

- [ ] **Step 3: Implement styles + permission panel**

```go
// internal/agent/tui/styles.go
package tui

import "github.com/charmbracelet/lipgloss"

var (
    StyleBorder       = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
    StylePanelTitle   = lipgloss.NewStyle().Bold(true)
    StyleStatusBar    = lipgloss.NewStyle().Background(lipgloss.Color("#222")).Foreground(lipgloss.Color("#ccc"))
    StyleStatusBarErr = StyleStatusBar.Foreground(lipgloss.Color("#FF7A7A"))
    StyleHint         = lipgloss.NewStyle().Faint(true)
    StyleAuthErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF7A7A")).Bold(true)
    StyleAuthOk       = lipgloss.NewStyle().Foreground(lipgloss.Color("#5FFF87"))
)
```

```go
// internal/agent/tui/panels.go
package tui

import (
    "encoding/json"
    "fmt"
    "strings"

    tea "github.com/charmbracelet/bubbletea"
)

type Panel interface {
    View(width int) string
    HandleKey(msg tea.KeyMsg) (Panel, tea.Cmd, bool)
    ID() string
}

// ---- Permission Panel ----

type PermissionPanelInput struct {
    PID         string
    Tool        string
    ExecutorID  string
    SelfExecID  string
    Args        json.RawMessage
}

type SendDecisionMsg struct {
    PID, Verdict, Scope string
}

type permissionPanel struct {
    in            PermissionPanelInput
    nestedDisable bool
}

func NewPermissionPanel(in PermissionPanelInput) Panel {
    p := &permissionPanel{in: in}
    p.nestedDisable = looksLikeNestedShell(in.Args)
    return p
}

func looksLikeNestedShell(args json.RawMessage) bool {
    var m map[string]any
    json.Unmarshal(args, &m)
    cmd, _ := m["command"].(string)
    head := strings.Fields(cmd)
    if len(head) < 2 { return false }
    switch head[0] {
    case "bash", "sh", "zsh", "dash", "ash", "fish":
        if head[1] == "-c" { return true }
    }
    return false
}

func (p *permissionPanel) ID() string { return p.in.PID }

func (p *permissionPanel) View(width int) string {
    var sb strings.Builder
    location := "elsewhere"
    if p.in.ExecutorID == p.in.SelfExecID && p.in.SelfExecID != "" {
        location = "this machine"
    }
    sb.WriteString(StylePanelTitle.Render(fmt.Sprintf("permission_request %s", p.in.PID)))
    sb.WriteByte('\n')
    sb.WriteString(fmt.Sprintf("%s on %s (%s)\n", p.in.Tool, p.in.ExecutorID, location))
    sb.WriteString("  args: ")
    sb.Write([]byte(briefRaw(p.in.Args, 120)))
    sb.WriteByte('\n')
    if p.nestedDisable {
        sb.WriteString(StyleAuthErr.Render("[ a ] always disabled (nested shell command)"))
        sb.WriteByte('\n')
        sb.WriteString("[ y ] allow once   [ N ] deny   [ esc ] later")
    } else {
        sb.WriteString("[ y ] allow once   [ a ] always   [ N ] deny   [ esc ] later")
    }
    return StyleBorder.Render(sb.String())
}

func (p *permissionPanel) HandleKey(msg tea.KeyMsg) (Panel, tea.Cmd, bool) {
    var verdict, scope string
    switch {
    case keyIs(msg, "y"): verdict, scope = "allow", "once"
    case keyIs(msg, "a"):
        if p.nestedDisable { return p, nil, false }
        verdict, scope = "allow", "always"
    case keyIs(msg, "n"), msg.Type == tea.KeyEnter:
        verdict, scope = "deny", "once"
    case msg.Type == tea.KeyEsc:
        return p, nil, true  // dismissed but no decision; Model should re-queue
    default:
        return p, nil, false
    }
    pid := p.in.PID
    return p, func() tea.Msg {
        return SendDecisionMsg{PID: pid, Verdict: verdict, Scope: scope}
    }, true
}

func keyIs(msg tea.KeyMsg, s string) bool {
    if msg.Type != tea.KeyRunes { return false }
    return string(msg.Runes) == s
}

func briefRaw(raw json.RawMessage, max int) string {
    s := string(raw)
    if len(s) > max { s = s[:max] + "…" }
    return s
}
```

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/agent/tui/ -run TestPermissionPanel -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tui/styles.go internal/agent/tui/panels.go internal/agent/tui/panels_test.go
git commit -m "feat(tui): styles + permission panel with nested-shell always-disable"
```

---

## Task 8: AskUser panel

**Files:**
- Modify: `internal/agent/tui/panels.go` (append)
- Modify: `internal/agent/tui/panels_test.go` (append)

- [ ] **Step 1: Write failing test**

```go
// internal/agent/tui/panels_test.go (append)
func TestAskUserPanel_SingleSelect(t *testing.T) {
    p := NewAskUserPanel(AskUserPanelInput{
        QID:      "q1",
        Question: "Pick one:",
        Options:  []string{"foo", "bar", "baz"},
    })
    // Down twice → bar; Enter → submit "bar"
    p2, _, _ := p.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
    p2, _, _ = p2.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
    p2, cmd, dismissed := p2.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
    if !dismissed {
        t.Fatal("not dismissed on enter")
    }
    out := cmd()
    ans, ok := out.(SendAnswerMsg)
    if !ok { t.Fatalf("got %T", out) }
    if ans.QID != "q1" || ans.Selected[0] != "baz" {
        t.Errorf("answer = %+v", ans)
    }
    _ = p2
}

func TestAskUserPanel_MultiSelect(t *testing.T) {
    p := NewAskUserPanel(AskUserPanelInput{
        QID: "q2", Question: "Pick many", Options: []string{"a","b","c"},
        MultiSelect: true,
    })
    p2, _, _ := p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})  // toggle a
    p2, _, _ = p2.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
    p2, _, _ = p2.HandleKey(tea.KeyMsg{Type: tea.KeyDown})
    p2, _, _ = p2.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})  // toggle c
    _, cmd, _ := p2.HandleKey(tea.KeyMsg{Type: tea.KeyEnter})
    ans := cmd().(SendAnswerMsg)
    if len(ans.Selected) != 2 || ans.Selected[0] != "a" || ans.Selected[1] != "c" {
        t.Errorf("answer = %+v", ans.Selected)
    }
}
```

- [ ] **Step 2: Verify fail**

Run: `go test ./internal/agent/tui/ -run TestAskUserPanel -v`
Expected: FAIL.

- [ ] **Step 3: Implement AskUserPanel (append to panels.go)**

```go
type AskUserPanelInput struct {
    QID         string
    Question    string
    Options     []string
    MultiSelect bool
}

type SendAnswerMsg struct {
    QID      string
    Selected []string
}

type askUserPanel struct {
    in     AskUserPanelInput
    cursor int
    picked map[int]bool
}

func NewAskUserPanel(in AskUserPanelInput) Panel {
    return &askUserPanel{in: in, picked: map[int]bool{}}
}

func (p *askUserPanel) ID() string { return p.in.QID }

func (p *askUserPanel) View(width int) string {
    var sb strings.Builder
    sb.WriteString(StylePanelTitle.Render(p.in.Question))
    sb.WriteByte('\n')
    for i, opt := range p.in.Options {
        marker := "  "
        if i == p.cursor { marker = "▸ " }
        check := "[ ]"
        if p.in.MultiSelect && p.picked[i] { check = "[x]" }
        if !p.in.MultiSelect { check = "" }
        sb.WriteString(fmt.Sprintf("%s%s %s\n", marker, check, opt))
    }
    if p.in.MultiSelect {
        sb.WriteString(StyleHint.Render("space toggle · enter submit · esc cancel"))
    } else {
        sb.WriteString(StyleHint.Render("enter submit · esc cancel"))
    }
    return StyleBorder.Render(sb.String())
}

func (p *askUserPanel) HandleKey(msg tea.KeyMsg) (Panel, tea.Cmd, bool) {
    switch {
    case msg.Type == tea.KeyDown:
        if p.cursor < len(p.in.Options)-1 { p.cursor++ }
    case msg.Type == tea.KeyUp:
        if p.cursor > 0 { p.cursor-- }
    case keyIs(msg, " ") && p.in.MultiSelect:
        p.picked[p.cursor] = !p.picked[p.cursor]
    case msg.Type == tea.KeyEnter:
        var sel []string
        if p.in.MultiSelect {
            for i, opt := range p.in.Options {
                if p.picked[i] { sel = append(sel, opt) }
            }
        } else {
            sel = []string{p.in.Options[p.cursor]}
        }
        qid := p.in.QID
        return p, func() tea.Msg { return SendAnswerMsg{QID: qid, Selected: sel} }, true
    case msg.Type == tea.KeyEsc:
        return p, nil, true
    }
    return p, nil, false
}
```

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/agent/tui/ -run TestAskUserPanel -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tui/panels.go internal/agent/tui/panels_test.go
git commit -m "feat(tui): ask_user panel (single + multi select)"
```

---

## Task 9: Login panel (OAuth Device Flow with QR)

**Files:**
- Create: `internal/agent/tui/login_panel.go`
- Create: `internal/agent/tui/login_panel_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/agent/tui/login_panel_test.go
package tui

import (
    "strings"
    "testing"

    tea "github.com/charmbracelet/bubbletea"
)

func TestLoginPanel_ShowsCodeAndURL(t *testing.T) {
    p := NewLoginPanel(LoginInfo{
        UserCode:      "ABCD-EFGH",
        VerifyURL:     "https://example/device",
        VerifyURLFull: "https://example/device?code=ABCD-EFGH",
        ExpiresIn:     900,
    })
    out := p.View(80)
    if !strings.Contains(out, "ABCD-EFGH") || !strings.Contains(out, "https://example/device") {
        t.Errorf("missing details: %s", out)
    }
    if !strings.Contains(out, "[ o ] open browser") {
        t.Errorf("hint missing: %s", out)
    }
}

func TestLoginPanel_OOpensURL(t *testing.T) {
    var openedURL string
    origOpen := openBrowser
    openBrowser = func(u string) error { openedURL = u; return nil }
    defer func() { openBrowser = origOpen }()

    p := NewLoginPanel(LoginInfo{UserCode: "X", VerifyURL: "u1", VerifyURLFull: "u1full"})
    _, cmd, dismissed := p.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
    if dismissed { t.Errorf("'o' should not dismiss") }
    _ = cmd
    if openedURL != "u1full" {
        t.Errorf("openedURL=%q want u1full", openedURL)
    }
}

func TestLoginPanel_EscDismissesAndCancels(t *testing.T) {
    p := NewLoginPanel(LoginInfo{UserCode: "X", VerifyURL: "u"})
    _, cmd, dismissed := p.HandleKey(tea.KeyMsg{Type: tea.KeyEsc})
    if !dismissed { t.Errorf("esc should dismiss") }
    if msg := cmd(); msg == nil {
        t.Errorf("esc should produce a CancelLoginMsg")
    }
}
```

- [ ] **Step 2: Verify fail**

Run: `go test ./internal/agent/tui/ -run TestLoginPanel -v`
Expected: FAIL.

- [ ] **Step 3: Implement LoginPanel**

```go
// internal/agent/tui/login_panel.go
package tui

import (
    "bytes"
    "fmt"
    "strings"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/mdp/qrterminal/v3"
    "github.com/pkg/browser"
)

type CancelLoginMsg struct{}

// Test seam.
var openBrowser = func(u string) error { return browser.OpenURL(u) }

type loginPanel struct {
    info LoginInfo
    qr   string
}

func NewLoginPanel(info LoginInfo) Panel {
    return &loginPanel{info: info, qr: renderQR(firstNonEmpty(info.VerifyURLFull, info.VerifyURL))}
}

func (p *loginPanel) ID() string { return "login" }

func (p *loginPanel) View(width int) string {
    var sb strings.Builder
    sb.WriteString(StylePanelTitle.Render("Authenticate to agentserver"))
    sb.WriteString("\n\n")
    sb.WriteString(fmt.Sprintf("  Visit: %s\n", firstNonEmpty(p.info.VerifyURLFull, p.info.VerifyURL)))
    sb.WriteString(fmt.Sprintf("  Code:  %s\n\n", p.info.UserCode))
    sb.WriteString(p.qr)
    sb.WriteByte('\n')
    sb.WriteString(StyleHint.Render("[ o ] open browser   [ esc ] cancel"))
    return StyleBorder.Render(sb.String())
}

func (p *loginPanel) HandleKey(msg tea.KeyMsg) (Panel, tea.Cmd, bool) {
    switch {
    case keyIs(msg, "o"):
        u := firstNonEmpty(p.info.VerifyURLFull, p.info.VerifyURL)
        return p, func() tea.Msg { _ = openBrowser(u); return nil }, false
    case msg.Type == tea.KeyEsc:
        return p, func() tea.Msg { return CancelLoginMsg{} }, true
    }
    return p, nil, false
}

func renderQR(url string) string {
    var buf bytes.Buffer
    qrterminal.GenerateWithConfig(url, qrterminal.Config{
        Level: qrterminal.L, Writer: &buf, HalfBlocks: true,
        BlackChar: qrterminal.BLACK_BLACK, BlackWhiteChar: qrterminal.BLACK_WHITE,
        WhiteBlackChar: qrterminal.WHITE_BLACK, WhiteChar: qrterminal.WHITE_WHITE,
        QuietZone: 1,
    })
    return buf.String()
}

func firstNonEmpty(s ...string) string {
    for _, v := range s { if v != "" { return v } }
    return ""
}
```

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/agent/tui/ -run TestLoginPanel -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tui/login_panel.go internal/agent/tui/login_panel_test.go
git commit -m "feat(tui): login panel (Device Flow + QR + open browser)"
```

---

## Task 10: Logout confirmation panel + Slash command parser

**Files:**
- Create: `internal/agent/tui/logout_panel.go`
- Create: `internal/agent/tui/cmds.go`
- Create: `internal/agent/tui/cmds_test.go`

- [ ] **Step 1: Write failing test for slash parser**

```go
// internal/agent/tui/cmds_test.go
package tui

import "testing"

func TestParseSlashCommand_ClassifiesCorrectly(t *testing.T) {
    cases := []struct {
        in    string
        class CommandClass
        name  string
        args  string
    }{
        {"/quit", LocalClass, "quit", ""},
        {"/cd /tmp", LocalClass, "cd", "/tmp"},
        {"/yolo", LocalClass, "yolo", ""},
        {"/login", LocalClass, "login", ""},
        {"/logout", LocalClass, "logout", ""},
        {"/attach foo.png", LocalClass, "attach", "foo.png"},
        {"/help", LocalClass, "help", ""},
        {"/clear", SessionClass, "clear", ""},
        {"/resume cse_x", SessionClass, "resume", "cse_x"},
        {"/take-control", SessionClass, "take-control", ""},
        {"/observe", SessionClass, "observe", ""},
        {"/model claude-opus-4-7", RemoteClass, "model", "claude-opus-4-7"},
        {"/permission bypass", RemoteClass, "permission", "bypass"},
        {"/compact", RemoteClass, "compact", ""},
        {"/cost", RemoteClass, "cost", ""},
        {"/agents", RemoteClass, "agents", ""},
        {"/whatever", RemoteClass, "whatever", ""},
    }
    for _, c := range cases {
        cmd, ok := ParseSlashCommand(c.in)
        if !ok { t.Errorf("%q parse failed", c.in); continue }
        if cmd.Class != c.class || cmd.Name != c.name || cmd.Args != c.args {
            t.Errorf("%q → %+v want class=%v name=%q args=%q",
                c.in, cmd, c.class, c.name, c.args)
        }
    }
}

func TestParseSlashCommand_RejectsNonSlash(t *testing.T) {
    if _, ok := ParseSlashCommand("not a slash command"); ok {
        t.Error("plain text should not parse as slash")
    }
}
```

- [ ] **Step 2: Verify fail**

Run: `go test ./internal/agent/tui/ -run TestParseSlashCommand -v`
Expected: FAIL.

- [ ] **Step 3: Implement parser**

```go
// internal/agent/tui/cmds.go
package tui

import "strings"

type CommandClass int

const (
    LocalClass CommandClass = iota   // L: handled in TUI process
    SessionClass                     // S: changes which session TUI is attached to
    RemoteClass                      // R: forwarded to agentserver /control
)

type ParsedCommand struct {
    Class CommandClass
    Name  string
    Args  string
}

var (
    localCommands   = map[string]bool{"quit": true, "cd": true, "yolo": true, "login": true, "logout": true, "attach": true, "help": true}
    sessionCommands = map[string]bool{"clear": true, "resume": true, "take-control": true, "observe": true, "sessions": true}
)

func ParseSlashCommand(line string) (ParsedCommand, bool) {
    line = strings.TrimSpace(line)
    if !strings.HasPrefix(line, "/") {
        return ParsedCommand{}, false
    }
    rest := strings.TrimPrefix(line, "/")
    parts := strings.SplitN(rest, " ", 2)
    name := parts[0]
    var args string
    if len(parts) == 2 { args = strings.TrimSpace(parts[1]) }
    cls := RemoteClass
    switch {
    case localCommands[name]:   cls = LocalClass
    case sessionCommands[name]: cls = SessionClass
    }
    return ParsedCommand{Class: cls, Name: name, Args: args}, true
}
```

- [ ] **Step 4: Implement Logout panel**

```go
// internal/agent/tui/logout_panel.go
package tui

import (
    "strings"

    tea "github.com/charmbracelet/bubbletea"
)

type ConfirmLogoutMsg struct{}

type logoutPanel struct{}

func NewLogoutPanel() Panel { return &logoutPanel{} }

func (p *logoutPanel) ID() string { return "logout" }

func (p *logoutPanel) View(width int) string {
    var sb strings.Builder
    sb.WriteString(StylePanelTitle.Render("Logout?"))
    sb.WriteByte('\n')
    sb.WriteString("Local credentials and executor session will be cleared.\n")
    sb.WriteString(StyleHint.Render("[ y ] confirm   [ N ] cancel"))
    return StyleBorder.Render(sb.String())
}

func (p *logoutPanel) HandleKey(msg tea.KeyMsg) (Panel, tea.Cmd, bool) {
    switch {
    case keyIs(msg, "y"):
        return p, func() tea.Msg { return ConfirmLogoutMsg{} }, true
    case keyIs(msg, "n"), msg.Type == tea.KeyEsc, msg.Type == tea.KeyEnter:
        return p, nil, true
    }
    return p, nil, false
}
```

- [ ] **Step 5: Verify tests pass + build**

Run: `go test ./internal/agent/tui/ -run TestParseSlashCommand -v && go build ./internal/agent/tui/...`
Expected: PASS + build success.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/tui/cmds.go internal/agent/tui/cmds_test.go internal/agent/tui/logout_panel.go
git commit -m "feat(tui): slash command parser + logout panel"
```

---

## Task 11: Keymap

**Files:**
- Create: `internal/agent/tui/keymap.go`

- [ ] **Step 1: Implement keymap**

```go
// internal/agent/tui/keymap.go
package tui

import "github.com/charmbracelet/bubbles/key"

type KeyMap struct {
    Send         key.Binding
    Newline      key.Binding
    Cancel       key.Binding
    Quit         key.Binding
    SessionPick  key.Binding
    Slash        key.Binding
    Help         key.Binding
    PageUp       key.Binding
    PageDown     key.Binding
    Top          key.Binding
    Bottom       key.Binding
}

func NewKeyMap() KeyMap {
    return KeyMap{
        Send:        key.NewBinding(key.WithKeys("enter"),     key.WithHelp("enter", "send")),
        Newline:     key.NewBinding(key.WithKeys("shift+enter"), key.WithHelp("shift+enter", "newline")),
        Cancel:      key.NewBinding(key.WithKeys("esc"),       key.WithHelp("esc", "cancel turn / clear input")),
        Quit:        key.NewBinding(key.WithKeys("ctrl+c"),    key.WithHelp("ctrl+c x2", "quit")),
        SessionPick: key.NewBinding(key.WithKeys("ctrl+t"),    key.WithHelp("ctrl+t", "session switcher")),
        Slash:       key.NewBinding(key.WithKeys("/"),         key.WithHelp("/", "command palette")),
        Help:        key.NewBinding(key.WithKeys("?"),         key.WithHelp("?", "key help")),
        PageUp:      key.NewBinding(key.WithKeys("pgup"),      key.WithHelp("pgup", "page up")),
        PageDown:    key.NewBinding(key.WithKeys("pgdown"),    key.WithHelp("pgdn", "page down")),
        Top:         key.NewBinding(key.WithKeys("home"),      key.WithHelp("home", "top")),
        Bottom:      key.NewBinding(key.WithKeys("end"),       key.WithHelp("end", "bottom")),
    }
}
```

- [ ] **Step 2: Build**

Run: `go build ./internal/agent/tui/...`
Expected: success.

- [ ] **Step 3: Commit**

```bash
git add internal/agent/tui/keymap.go
git commit -m "feat(tui): keymap definitions"
```

---

## Task 12: Top-level Model + Init + Update dispatcher

**Files:**
- Create: `internal/agent/tui/model.go`
- Create: `internal/agent/tui/model_test.go`

- [ ] **Step 1: Write failing tests for model behaviour**

```go
// internal/agent/tui/model_test.go
package tui

import (
    "encoding/json"
    "strings"
    "testing"

    tea "github.com/charmbracelet/bubbletea"
)

func newTestModel(t *testing.T) *Model {
    return NewModel(ModelConfig{
        ServerURL:   "https://example",
        WorkspaceID: "ws",
        ExecutorID:  "exe_a",
        Bus:         &Bus{cfg: BusConfig{ServerURL: "https://example", WorkspaceID: "ws", ExecutorID: "exe_a", Auth: &fakeAuth{tk: "t"}}},
        Auth:        nil,  // tests don't drive login
    })
}

func TestModel_LoggedOut_DisablesInput(t *testing.T) {
    m := newTestModel(t)
    m.SetAuthState(AuthLoggedOut)
    if m.InputEnabled() {
        t.Errorf("input should be disabled when LoggedOut")
    }
    out := m.View()
    if !strings.Contains(out, "logged out") || !strings.Contains(out, "/login") {
        t.Errorf("missing logged-out hint in view: %s", out)
    }
}

func TestModel_EventArrived_AppendsToTimeline(t *testing.T) {
    m := newTestModel(t)
    m.SetAuthState(AuthLoggedIn)
    m, _ = updateAndAssertModel(t, m, EventArrivedMsg{Event: SSEEvent{
        Type: "user_message", Data: []byte(`{"text":"hi"}`), LastEventID: "1",
    }})
    if m.timeline.Len() != 1 {
        t.Errorf("timeline len=%d", m.timeline.Len())
    }
}

func TestModel_PermissionRequestEvent_OpensPanel(t *testing.T) {
    m := newTestModel(t)
    m.SetAuthState(AuthLoggedIn)
    m, _ = updateAndAssertModel(t, m, EventArrivedMsg{Event: SSEEvent{
        Type: "permission_request",
        Data: []byte(`{"permission_id":"p1","tool":"remote_bash","executor_id":"exe_a","args":{"command":"ls"}}`),
        LastEventID: "1",
    }})
    if m.mode != ModeAwaitPerm {
        t.Errorf("mode=%v want AwaitPerm", m.mode)
    }
    if m.activePanel == nil || m.activePanel.ID() != "p1" {
        t.Errorf("panel = %+v", m.activePanel)
    }
}

func TestModel_SendDecisionMsg_ProducesPostCmd(t *testing.T) {
    m := newTestModel(t)
    m.SetAuthState(AuthLoggedIn)
    m.sessionID = "cse_1"
    _, cmd := m.Update(SendDecisionMsg{PID: "p1", Verdict: "allow", Scope: "once"})
    if cmd == nil { t.Fatal("expected a Cmd") }
    // Don't run cmd here; just check it's produced.
}

func updateAndAssertModel(t *testing.T, m *Model, msg tea.Msg) (*Model, tea.Cmd) {
    t.Helper()
    next, cmd := m.Update(msg)
    return next.(*Model), cmd
}

func TestModel_SlashLogin_StartsLoginWhenLoggedOut(t *testing.T) {
    m := newTestModel(t)
    m.SetAuthState(AuthLoggedOut)
    // Stub auth controller via test seam.
    var started bool
    m.startLoginFn = func() tea.Cmd {
        started = true
        return func() tea.Msg { return DeviceCodeReadyMsg{Info: LoginInfo{UserCode: "X"}} }
    }
    m, cmd := updateAndAssertModel(t, m, CommandSelectedMsg{Command: "login"})
    if !started {
        t.Errorf("startLoginFn not invoked")
    }
    if cmd == nil { t.Fatal("expected cmd") }
    msg := cmd()
    if _, ok := msg.(DeviceCodeReadyMsg); !ok {
        t.Errorf("cmd → %T want DeviceCodeReadyMsg", msg)
    }
}

func TestModel_DeviceCodeReady_OpensLoginPanel(t *testing.T) {
    m := newTestModel(t)
    m.SetAuthState(AuthLoggingIn)
    m, _ = updateAndAssertModel(t, m, DeviceCodeReadyMsg{Info: LoginInfo{
        UserCode: "AAA", VerifyURL: "https://x", VerifyURLFull: "https://x/full",
    }})
    if m.mode != ModeAwaitLogin {
        t.Errorf("mode=%v want AwaitLogin", m.mode)
    }
    if m.activePanel == nil || m.activePanel.ID() != "login" {
        t.Errorf("panel id %v", m.activePanel)
    }
}

func TestModel_AuthStateChanged_LoggedIn_ClearsLoginPanel(t *testing.T) {
    m := newTestModel(t)
    m.SetAuthState(AuthLoggingIn)
    m.activePanel = NewLoginPanel(LoginInfo{UserCode: "X"})
    m.mode = ModeAwaitLogin
    m, _ = updateAndAssertModel(t, m, AuthStateChangedMsg{State: AuthLoggedIn})
    if m.mode != ModeNormal {
        t.Errorf("mode=%v want Normal", m.mode)
    }
    if m.activePanel != nil {
        t.Errorf("activePanel should be cleared")
    }
}

func TestModel_View_StatusBarShowsAuthAndTunnel(t *testing.T) {
    m := newTestModel(t)
    m.SetAuthState(AuthLoggedIn)
    m.statusTunnel = "online"
    m.sessionID = "cse_xyz"
    out := m.View()
    if !strings.Contains(out, "logged_in") || !strings.Contains(out, "online") || !strings.Contains(out, "cse_xyz") {
        t.Errorf("status bar missing fields: %s", out)
    }
    _ = json.Marshal  // keep import
}
```

- [ ] **Step 2: Verify fail**

Run: `go test ./internal/agent/tui/ -run TestModel -v`
Expected: FAIL.

- [ ] **Step 3: Implement Model**

```go
// internal/agent/tui/model.go
package tui

import (
    "context"
    "encoding/json"
    "fmt"
    "strings"
    "time"

    "github.com/charmbracelet/bubbles/textarea"
    "github.com/charmbracelet/bubbles/viewport"
    tea "github.com/charmbracelet/bubbletea"
)

type Mode int

const (
    ModeNormal Mode = iota
    ModeAwaitPerm
    ModeAwaitAskUser
    ModeAwaitLogin
    ModeAwaitLogout
    ModeCommand
    ModeAttachPicker
    ModeQuitting
)

type ModelConfig struct {
    ServerURL   string
    WorkspaceID string
    ExecutorID  string
    Bus         *Bus
    Auth        *AuthController
    Yolo        bool
    InitialModel string
    Resume       string
    Continue     bool
}

type Model struct {
    cfg    ModelConfig
    bus    *Bus
    auth   *AuthController

    mode         Mode
    authState    AuthState
    sessionID    string
    turnID       string
    cwd          string
    model        string
    permMode     string
    statusTunnel string
    statusEvents string
    statusTurn   string

    timeline *Timeline
    viewport viewport.Model
    input    textarea.Model
    keys     KeyMap

    activePanel Panel
    permQueue   []Panel
    askQueue    []Panel

    pendingAttachments []InboundAttachment

    // test seams
    startLoginFn func() tea.Cmd
}

func NewModel(cfg ModelConfig) *Model {
    ta := textarea.New()
    ta.Placeholder = "Type a message…"
    ta.SetHeight(3)
    ta.Focus()
    vp := viewport.New(80, 20)
    return &Model{
        cfg:          cfg,
        bus:          cfg.Bus,
        auth:         cfg.Auth,
        timeline:     NewTimeline(5000),
        viewport:     vp,
        input:        ta,
        keys:         NewKeyMap(),
        statusTunnel: "unknown",
        statusEvents: "live",
        statusTurn:   "idle",
        model:        cfg.InitialModel,
    }
}

func (m *Model) SetAuthState(s AuthState) {
    m.authState = s
    if s == AuthLoggedIn { m.input.Focus() }
}

func (m *Model) InputEnabled() bool {
    return m.authState == AuthLoggedIn || m.authState == AuthRefreshing
}

func (m *Model) Init() tea.Cmd {
    cmds := []tea.Cmd{textarea.Blink}
    if m.auth != nil {
        m.SetAuthState(m.auth.State())
    }
    if m.authState == AuthLoggedIn {
        cmds = append(cmds, m.startSessionCmds()...)
    }
    return tea.Batch(cmds...)
}

func (m *Model) startSessionCmds() []tea.Cmd {
    var out []tea.Cmd
    // On --resume / --continue, attach existing session and start SSE; else
    // wait for first prompt to implicitly create one.
    if m.cfg.Resume != "" {
        m.sessionID = m.cfg.Resume
        out = append(out, m.attachAndSubscribe(m.sessionID))
    } else if m.cfg.Continue {
        out = append(out, m.continueLatestCmd())
    }
    out = append(out, m.statusTickCmd())
    return out
}

func (m *Model) statusTickCmd() tea.Cmd {
    return tea.Tick(10*time.Second, func(t time.Time) tea.Msg {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        st, err := m.bus.FetchExecutorStatus(ctx)
        return StatusTickMsg{Tunnel: st, Err: err}
    })
}

func (m *Model) continueLatestCmd() tea.Cmd {
    return func() tea.Msg {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        list, err := m.bus.ListSessions(ctx)
        if err != nil || len(list) == 0 {
            return ListSessionsReplyMsg{Err: err}
        }
        return ResumeRequestedMsg{SessionID: list[0].SessionID}
    }
}

func (m *Model) attachAndSubscribe(sid string) tea.Cmd {
    return func() tea.Msg {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        resp, err := m.bus.AttachSession(ctx, sid, "operator")
        return AttachReplyMsg{Resp: resp, Err: err}
    }
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    // Always update timeline on event arrival (regardless of mode)
    if ev, ok := msg.(EventArrivedMsg); ok {
        m.timeline.Append(ev.Event)
        m.viewport.SetContent(m.timeline.Render(m.viewport.Width, m.cfg.ExecutorID))
        m.viewport.GotoBottom()
        if cmd := m.maybeOpenPanelForEvent(ev.Event); cmd != nil {
            return m, cmd
        }
        return m, nil
    }

    // Auth state propagation
    if a, ok := msg.(AuthStateChangedMsg); ok {
        m.SetAuthState(a.State)
        if a.State == AuthLoggedIn && m.mode == ModeAwaitLogin {
            m.mode = ModeNormal
            m.activePanel = nil
            return m, tea.Batch(m.startSessionCmds()...)
        }
        return m, nil
    }

    // Panel-driven input
    if m.activePanel != nil {
        if k, ok := msg.(tea.KeyMsg); ok {
            np, cmd, dismissed := m.activePanel.HandleKey(k)
            m.activePanel = np
            if dismissed {
                m.activePanel = nil
                m.popPanelQueue()
                if m.activePanel == nil { m.mode = ModeNormal }
            }
            return m, cmd
        }
    }

    // SendDecisionMsg / SendAnswerMsg / etc → bus call
    switch v := msg.(type) {
    case DeviceCodeReadyMsg:
        m.activePanel = NewLoginPanel(v.Info)
        m.mode = ModeAwaitLogin
        return m, nil
    case SendDecisionMsg:
        sid := m.sessionID
        return m, func() tea.Msg {
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()
            err := m.bus.PostDecision(ctx, sid, v.PID, v.Verdict, v.Scope)
            return DecisionAckMsg{PermissionID: v.PID, Err: err}
        }
    case ConfirmLogoutMsg:
        return m, func() tea.Msg {
            err := m.auth.Logout()
            return LogoutDoneMsg{Err: err}
        }
    case CancelLoginMsg:
        if m.auth != nil { m.auth.CancelLogin() }
        m.mode = ModeNormal
        m.activePanel = nil
        return m, nil
    case CommandSelectedMsg:
        return m.runCommand(v)
    case ResumeRequestedMsg:
        m.sessionID = v.SessionID
        return m, m.attachAndSubscribe(v.SessionID)
    case StatusTickMsg:
        if v.Tunnel != nil {
            m.statusTunnel = v.Tunnel.Status
        } else if v.Err != nil {
            m.statusTunnel = "unknown"
        }
        return m, m.statusTickCmd()
    }

    // Default: pass keys to viewport / textarea.
    var cmd tea.Cmd
    if k, ok := msg.(tea.KeyMsg); ok && m.mode == ModeNormal {
        if m.handleNormalKey(k) {
            return m, nil
        }
        if m.InputEnabled() {
            m.input, cmd = m.input.Update(msg)
        }
        return m, cmd
    }
    if m.InputEnabled() {
        m.input, cmd = m.input.Update(msg)
    }
    return m, cmd
}

func (m *Model) handleNormalKey(k tea.KeyMsg) bool {
    s := strings.ToLower(k.String())
    switch {
    case s == "enter":
        if !m.InputEnabled() { return true }
        text := strings.TrimSpace(m.input.Value())
        if text == "" { return true }
        if cmd, ok := ParseSlashCommand(text); ok {
            m.input.Reset()
            tea.Cmd(func() tea.Msg {
                return CommandSelectedMsg{Command: cmd.Name, Args: cmd.Args}
            })()
            // Note: Bubble Tea's Cmd return — for simplicity we synthesize via Update below
            // by stuffing into a queued msg.
            return false
        }
        m.input.Reset()
        // Send via PostInbound
        sid := m.sessionID
        bus := m.bus
        attachments := m.pendingAttachments
        m.pendingAttachments = nil
        tea.Cmd(func() tea.Msg {
            ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
            defer cancel()
            req := InboundRequest{
                SessionID: sid, Text: text,
                Attachments: attachments,
                PermissionResponder: true,
            }
            resp, err := bus.PostInbound(ctx, req)
            if err != nil {
                return InboundRejectedMsg{Code: "post_failed", Message: err.Error()}
            }
            return InboundAcceptedMsg{SessionID: resp.SessionID, TurnID: resp.TurnID}
        })()
        return true
    }
    return false
}

func (m *Model) runCommand(c CommandSelectedMsg) (tea.Model, tea.Cmd) {
    name := c.Command
    args := c.Args
    parsed, _ := ParseSlashCommand("/" + name + " " + args)
    switch parsed.Class {
    case LocalClass:
        return m.runLocalCommand(parsed.Name, parsed.Args)
    case SessionClass:
        return m.runSessionCommand(parsed.Name, parsed.Args)
    case RemoteClass:
        return m, m.runRemoteCommand(parsed.Name, parsed.Args)
    }
    return m, nil
}

func (m *Model) runLocalCommand(name, args string) (tea.Model, tea.Cmd) {
    switch name {
    case "quit":
        return m, tea.Quit
    case "yolo":
        m.permMode = "bypass"
        return m, nil
    case "login":
        if m.startLoginFn != nil { return m, m.startLoginFn() }
        if m.auth == nil { return m, nil }
        return m, func() tea.Msg {
            info, err := m.auth.StartLogin(context.Background())
            if err != nil {
                return LoginPollDoneMsg{Err: err}
            }
            return DeviceCodeReadyMsg{Info: info}
        }
    case "logout":
        m.activePanel = NewLogoutPanel()
        m.mode = ModeAwaitLogout
        return m, nil
    case "cd":
        m.cwd = args
        if err := writeRuntimeCwd(m.cfg.ExecutorID, args); err != nil {
            return m, func() tea.Msg { return FatalErrorMsg{Err: err} }
        }
        return m, nil
    case "help":
        return m, nil  // TODO render a help panel; v1 stub
    case "attach":
        // Task 15
        return m, nil
    }
    return m, nil
}

func (m *Model) runSessionCommand(name, args string) (tea.Model, tea.Cmd) {
    switch name {
    case "clear":
        return m, func() tea.Msg {
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()
            sid, err := m.bus.NewSession(ctx, "ask", m.cfg.ExecutorID)
            return NewSessionReplyMsg{SessionID: sid, Err: err}
        }
    case "resume":
        if args == "" { return m, nil }
        return m, func() tea.Msg { return ResumeRequestedMsg{SessionID: args} }
    case "take-control":
        return m, m.attachAndSubscribe(m.sessionID)
    case "observe":
        sid := m.sessionID
        return m, func() tea.Msg {
            ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
            defer cancel()
            resp, err := m.bus.AttachSession(ctx, sid, "observer")
            return AttachReplyMsg{Resp: resp, Err: err}
        }
    }
    return m, nil
}

func (m *Model) runRemoteCommand(name, args string) tea.Cmd {
    sid := m.sessionID
    bus := m.bus
    body := map[string]any{}
    switch name {
    case "model":
        body["model"] = args
    case "permission":
        body["mode"] = args
    }
    return func() tea.Msg {
        ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer cancel()
        out, err := bus.PostControl(ctx, sid, name, body)
        return ControlReplyMsg{Command: name, Body: out, Err: err}
    }
}

func (m *Model) maybeOpenPanelForEvent(ev SSEEvent) tea.Cmd {
    if ev.Type == "permission_request" {
        var p struct {
            PermissionID string          `json:"permission_id"`
            Tool         string          `json:"tool"`
            ExecutorID   string          `json:"executor_id"`
            Args         json.RawMessage `json:"args"`
        }
        json.Unmarshal(ev.Data, &p)
        panel := NewPermissionPanel(PermissionPanelInput{
            PID: p.PermissionID, Tool: p.Tool, ExecutorID: p.ExecutorID,
            SelfExecID: m.cfg.ExecutorID, Args: p.Args,
        })
        if m.activePanel != nil {
            m.permQueue = append(m.permQueue, panel)
        } else {
            m.activePanel = panel
            m.mode = ModeAwaitPerm
        }
    }
    if ev.Type == "ask_user" {
        var p struct {
            QuestionID  string   `json:"question_id"`
            Question    string   `json:"question"`
            Options     []string `json:"options"`
            MultiSelect bool     `json:"multi_select"`
        }
        json.Unmarshal(ev.Data, &p)
        panel := NewAskUserPanel(AskUserPanelInput{
            QID: p.QuestionID, Question: p.Question, Options: p.Options, MultiSelect: p.MultiSelect,
        })
        if m.activePanel != nil {
            m.askQueue = append(m.askQueue, panel)
        } else {
            m.activePanel = panel
            m.mode = ModeAwaitAskUser
        }
    }
    return nil
}

func (m *Model) popPanelQueue() {
    if len(m.permQueue) > 0 {
        m.activePanel = m.permQueue[0]
        m.permQueue = m.permQueue[1:]
        m.mode = ModeAwaitPerm
        return
    }
    if len(m.askQueue) > 0 {
        m.activePanel = m.askQueue[0]
        m.askQueue = m.askQueue[1:]
        m.mode = ModeAwaitAskUser
        return
    }
}

func (m *Model) View() string {
    return RenderView(m)
}

// writeRuntimeCwd is implemented in Task 16. Stubbed here.
func writeRuntimeCwd(executorID, cwd string) error { return nil }
```

(`tea.Cmd(func() tea.Msg{...})()` calls in `handleNormalKey` are wrong shape — Bubble Tea expects Cmds returned from `Update`. Refactor in Step 4 to return cmds properly. Drafted here for shape; final code returns the cmd via the outer `Update` return.)

- [ ] **Step 4: Fix the Cmd-return shape in handleNormalKey**

Refactor: `handleNormalKey` returns `(handled bool, cmd tea.Cmd)`; `Update` propagates the cmd. Final shape:

```go
func (m *Model) handleNormalKey(k tea.KeyMsg) (bool, tea.Cmd) {
    if k.Type != tea.KeyEnter { return false, nil }
    if !m.InputEnabled() { return true, nil }
    text := strings.TrimSpace(m.input.Value())
    if text == "" { return true, nil }
    m.input.Reset()
    if cmd, ok := ParseSlashCommand(text); ok {
        return true, func() tea.Msg { return CommandSelectedMsg{Command: cmd.Name, Args: cmd.Args} }
    }
    sid := m.sessionID; bus := m.bus
    attachments := m.pendingAttachments; m.pendingAttachments = nil
    return true, func() tea.Msg {
        ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        req := InboundRequest{SessionID: sid, Text: text, Attachments: attachments, PermissionResponder: true}
        resp, err := bus.PostInbound(ctx, req)
        if err != nil {
            return InboundRejectedMsg{Code: "post_failed", Message: err.Error()}
        }
        return InboundAcceptedMsg{SessionID: resp.SessionID, TurnID: resp.TurnID}
    }
}
```

Update the caller in `Update` accordingly.

- [ ] **Step 5: Verify tests pass**

Run: `go test ./internal/agent/tui/ -run TestModel -v`
Expected: PASS (some tests may still fail; iterate until green).

- [ ] **Step 6: Commit**

```bash
git add internal/agent/tui/model.go internal/agent/tui/model_test.go
git commit -m "feat(tui): top-level Model with auth-state-aware Update dispatcher"
```

---

## Task 13: View renderer (status bar + viewport + panels + input)

**Files:**
- Create: `internal/agent/tui/view.go`
- Create: `internal/agent/tui/view_test.go`

- [ ] **Step 1: Write failing test (golden-style)**

```go
// internal/agent/tui/view_test.go
package tui

import (
    "strings"
    "testing"
)

func TestRenderView_LoggedOut_HasLoginHint(t *testing.T) {
    m := newTestModel(t)
    m.SetAuthState(AuthLoggedOut)
    m.viewport.Width = 80; m.viewport.Height = 10
    out := RenderView(m)
    if !strings.Contains(out, "Use /login") {
        t.Errorf("missing /login hint: %s", out)
    }
}

func TestRenderView_LoggedIn_ShowsStatusBar(t *testing.T) {
    m := newTestModel(t)
    m.SetAuthState(AuthLoggedIn)
    m.sessionID = "cse_x"
    m.cwd = "/home/me"
    m.statusTunnel = "online"
    m.statusTurn = "idle"
    m.viewport.Width = 80; m.viewport.Height = 10
    out := RenderView(m)
    for _, want := range []string{"cse_x", "/home/me", "online", "idle"} {
        if !strings.Contains(out, want) {
            t.Errorf("missing %q in view:\n%s", want, out)
        }
    }
}

func TestRenderView_PermissionPanelVisible(t *testing.T) {
    m := newTestModel(t)
    m.SetAuthState(AuthLoggedIn)
    m.activePanel = NewPermissionPanel(PermissionPanelInput{
        PID: "p1", Tool: "remote_bash", ExecutorID: "e", SelfExecID: "e",
        Args: []byte(`{}`),
    })
    m.mode = ModeAwaitPerm
    m.viewport.Width = 80; m.viewport.Height = 10
    out := RenderView(m)
    if !strings.Contains(out, "p1") || !strings.Contains(out, "remote_bash") {
        t.Errorf("panel not in view: %s", out)
    }
}
```

- [ ] **Step 2: Verify fail**

Run: `go test ./internal/agent/tui/ -run TestRenderView -v`
Expected: FAIL.

- [ ] **Step 3: Implement RenderView**

```go
// internal/agent/tui/view.go
package tui

import (
    "fmt"
    "strings"

    "github.com/charmbracelet/lipgloss"
)

func RenderView(m *Model) string {
    var sb strings.Builder
    sb.WriteString(renderStatusBar(m))
    sb.WriteByte('\n')
    sb.WriteString(m.viewport.View())
    sb.WriteByte('\n')
    if m.activePanel != nil {
        sb.WriteString(m.activePanel.View(m.viewport.Width))
        sb.WriteByte('\n')
    }
    sb.WriteString(renderInput(m))
    return sb.String()
}

func renderStatusBar(m *Model) string {
    line1 := fmt.Sprintf(" session: %s · cwd: %s · server: %s ",
        emptyDash(m.sessionID), emptyDash(m.cwd), emptyDash(m.cfg.ServerURL))
    line2 := fmt.Sprintf(" auth: %s · tunnel: %s · events: %s · turn: %s · model: %s ",
        m.authState, m.statusTunnel, m.statusEvents, m.statusTurn, emptyDash(m.model))
    style := StyleStatusBar
    if m.authState == AuthLoggedOut {
        style = StyleStatusBarErr
    }
    return style.Render(line1) + "\n" + style.Render(line2)
}

func renderInput(m *Model) string {
    if m.authState == AuthLoggedOut {
        hint := lipgloss.NewStyle().Foreground(lipgloss.Color("#FF7A7A")).Render(
            "Use /login to authenticate")
        return lipgloss.JoinVertical(lipgloss.Left, hint, m.input.View())
    }
    return m.input.View()
}

func emptyDash(s string) string {
    if s == "" { return "—" }
    return s
}
```

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/agent/tui/ -run TestRenderView -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tui/view.go internal/agent/tui/view_test.go
git commit -m "feat(tui): View renderer (status bar + viewport + panel + input)"
```

---

## Task 14: Wire RunTUI — assemble AuthController, Bus, ExecutorClient, BubbleTea

**Files:**
- Modify: `internal/agent/tui.go`

- [ ] **Step 1: Write the assembly**

```go
// internal/agent/tui.go — replace stub from Task 1
package agent

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "time"

    tea "github.com/charmbracelet/bubbletea"

    "github.com/agentserver/agentserver/internal/agent/tui"
)

func RunTUI(ctx context.Context, opts TUIOpts) error {
    // 1. Resolve server URL.
    server := opts.Server
    creds, _ := LoadCredentials(DefaultCredentialsPath())
    if server == "" && creds != nil {
        server = creds.ServerURL
    }
    if opts.WorkspaceID == "" {
        return fmt.Errorf("--workspace-id is required")
    }
    workDir := opts.WorkDir
    if workDir == "" {
        cwd, err := os.Getwd()
        if err != nil { return err }
        workDir = cwd
    }

    // 2. Build AuthController.
    auth := tui.NewAuthController(tui.AuthConfig{
        ServerURL:       server,
        CredentialsPath: DefaultCredentialsPath(),
        SkipOpenBrowser: opts.SkipOpenBrowser,
    })

    var executorID string
    var ec *ExecutorClient

    // 3. If logged in, register executor + start tunnel BEFORE the Bubble Tea program.
    //    If not logged in, the executor goroutine is started after /login completes
    //    (deferred startup; see Model.startSessionCmds + post-login wiring).
    if auth.State() == AuthLoggedIn {
        sess, err := LoadOrRegisterExecutor(ExecutorOpts{
            ServerURL: server, Name: opts.Name, WorkspaceID: opts.WorkspaceID,
        })
        if err != nil {
            return fmt.Errorf("register executor: %w", err)
        }
        executorID = sess.ExecutorID
        ec = NewExecutorClient(sess, workDir)
        go func() { _ = ec.Run(ctx) }()
    }

    // 4. Build Bus.
    bus := tui.NewBus(tui.BusConfig{
        ServerURL: server, WorkspaceID: opts.WorkspaceID, ExecutorID: executorID,
        Auth: auth,
    })

    // 5. Build Model.
    permMode := "ask"
    if opts.Yolo { permMode = "bypass" }
    model := tui.NewModel(tui.ModelConfig{
        ServerURL: server, WorkspaceID: opts.WorkspaceID, ExecutorID: executorID,
        Bus: bus, Auth: auth, Yolo: opts.Yolo,
        InitialModel: opts.Model, Resume: opts.Resume, Continue: opts.Continue,
    })
    _ = permMode  // applied via metadata when posting inbound

    // 6. Wire AuthController OnChange → Model
    p := tea.NewProgram(model, tea.WithContext(ctx), tea.WithAltScreen())
    auth.SetOnChange(func(s tui.AuthState) {
        p.Send(tui.AuthStateChangedMsg{State: s})
    })

    // 7. Wire SSE consumer (only after a session exists; subscribe lazily after attach).
    //    The Model triggers SSE subscription via Bus.SubscribeEvents from its own commands.

    _ = p.Start()
    _ = filepath.Join  // keep import
    _ = time.Now       // keep import
    return nil
}
```

`AuthController.SetOnChange` is a small accessor mutating cfg.OnChange post-construction (add it):

```go
// internal/agent/tui/auth.go (append)
func (a *AuthController) SetOnChange(fn func(AuthState)) {
    a.cfg.OnChange = fn
}
```

- [ ] **Step 2: Build verification**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Manual smoke (no tests yet — Task 17 covers e2e)**

Run: `go run ./cmd/agentserver-agent tui --server https://agent.cs.ac.cn --workspace-id ws_x`
Expected: TUI opens; if no creds, status bar shows "logged out" + /login hint; `/login` produces device code panel.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/tui.go internal/agent/tui/auth.go
git commit -m "feat(tui): RunTUI wires AuthController + ExecutorClient + Bubble Tea program"
```

---

## Task 15: `/attach <path>` file picker

**Files:**
- Create: `internal/agent/tui/attach_picker.go`
- Create: `internal/agent/tui/attach_picker_test.go`

- [ ] **Step 1: Write failing test**

```go
// internal/agent/tui/attach_picker_test.go
package tui

import (
    "encoding/base64"
    "os"
    "path/filepath"
    "testing"
)

func TestAttachFromPath_ReadsAndBase64s(t *testing.T) {
    dir := t.TempDir()
    p := filepath.Join(dir, "test.txt")
    if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil { t.Fatal(err) }
    a, err := AttachFromPath(p)
    if err != nil { t.Fatal(err) }
    if a.Filename != "test.txt" { t.Errorf("filename=%q", a.Filename) }
    if a.Size != 5 { t.Errorf("size=%d", a.Size) }
    decoded, _ := base64.StdEncoding.DecodeString(a.ContentB64)
    if string(decoded) != "hello" {
        t.Errorf("decoded=%q", string(decoded))
    }
    if a.Kind != "file" { t.Errorf("kind=%q", a.Kind) }
}

func TestAttachFromPath_DetectsImageByExt(t *testing.T) {
    dir := t.TempDir()
    p := filepath.Join(dir, "shot.png")
    _ = os.WriteFile(p, []byte("\x89PNGfake"), 0o644)
    a, _ := AttachFromPath(p)
    if a.Kind != "image" { t.Errorf("kind=%q want image", a.Kind) }
}

func TestAttachFromPath_RejectsTooBig(t *testing.T) {
    dir := t.TempDir()
    p := filepath.Join(dir, "big.bin")
    if err := os.WriteFile(p, make([]byte, 9<<20), 0o644); err != nil { t.Fatal(err) }
    _, err := AttachFromPath(p)
    if err == nil { t.Error("expected size cap error") }
}
```

- [ ] **Step 2: Verify fail**

Run: `go test ./internal/agent/tui/ -run TestAttachFromPath -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
// internal/agent/tui/attach_picker.go
package tui

import (
    "encoding/base64"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"
)

const attachPerFileMaxBytes = 8 << 20

func AttachFromPath(path string) (InboundAttachment, error) {
    f, err := os.Open(path)
    if err != nil { return InboundAttachment{}, err }
    defer f.Close()
    info, err := f.Stat()
    if err != nil { return InboundAttachment{}, err }
    if info.Size() > attachPerFileMaxBytes {
        return InboundAttachment{}, fmt.Errorf("attachment exceeds 8 MiB cap (%d bytes)", info.Size())
    }
    raw, err := io.ReadAll(f)
    if err != nil { return InboundAttachment{}, err }
    kind := "file"
    switch strings.ToLower(filepath.Ext(path)) {
    case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
        kind = "image"
    }
    return InboundAttachment{
        Kind:       kind,
        Filename:   filepath.Base(path),
        Size:       int(info.Size()),
        ContentB64: base64.StdEncoding.EncodeToString(raw),
    }, nil
}
```

Then in `model.go`, replace the `case "attach":` stub from Task 12:

```go
case "attach":
    if args == "" { return m, nil }
    a, err := AttachFromPath(args)
    if err != nil {
        return m, func() tea.Msg { return FatalErrorMsg{Err: err} }
    }
    m.pendingAttachments = append(m.pendingAttachments, a)
    return m, func() tea.Msg { return AttachmentPickedMsg{Attachment: a} }
```

- [ ] **Step 4: Verify tests pass**

Run: `go test ./internal/agent/tui/ -run TestAttachFromPath -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/tui/attach_picker.go internal/agent/tui/attach_picker_test.go internal/agent/tui/model.go
git commit -m "feat(tui): /attach reads file as base64; image detected by extension"
```

---

## Task 16: `/cd` — runtime cwd file coordination

**Files:**
- Modify: `internal/agent/executor_session.go`
- Modify: `internal/agent/executortools/tools.go` (or wherever ToolExecutor reads workDir)
- Modify: `internal/agent/tui/model.go` (`writeRuntimeCwd` helper)
- Test: `internal/agent/tui/runtime_cwd_test.go`

- [ ] **Step 1: Add RuntimeCwd to ExecutorSession**

In `internal/agent/executor_session.go`:

```go
type ExecutorSession struct {
    // existing
    ExecutorID    string    `json:"executor_id"`
    Name          string    `json:"name"`
    WorkspaceID   string    `json:"workspace_id"`
    TunnelToken   string    `json:"tunnel_token"`
    RegistryToken string    `json:"registry_token"`
    ServerURL     string    `json:"server_url"`
    CreatedAt     time.Time `json:"created_at"`

    // new (TUI runtime override; absent for non-TUI)
    RuntimeCwd string `json:"runtime_cwd,omitempty"`
}
```

- [ ] **Step 2: Write helper to update only `runtime_cwd` in-place**

In `internal/agent/executor_session.go` add:

```go
// SetRuntimeCwd reads the current session JSON, updates the RuntimeCwd field,
// and writes it back atomically. Other fields are preserved.
func SetRuntimeCwd(executorID, cwd string) error {
    dir, err := executorSessionsDir()
    if err != nil { return err }
    path := filepath.Join(dir, executorID+".json")
    data, err := os.ReadFile(path)
    if err != nil { return err }
    var sess ExecutorSession
    if err := json.Unmarshal(data, &sess); err != nil { return err }
    sess.RuntimeCwd = cwd
    out, err := json.MarshalIndent(&sess, "", "  ")
    if err != nil { return err }
    tmp := path + ".tmp"
    if err := os.WriteFile(tmp, out, 0o600); err != nil { return err }
    return os.Rename(tmp, path)
}

// LoadRuntimeCwd reads only the runtime_cwd field. Returns "" if file missing
// or field absent. Cheap (no full struct parse).
func LoadRuntimeCwd(executorID string) string {
    dir, err := executorSessionsDir()
    if err != nil { return "" }
    data, err := os.ReadFile(filepath.Join(dir, executorID+".json"))
    if err != nil { return "" }
    var s struct{ RuntimeCwd string `json:"runtime_cwd"` }
    json.Unmarshal(data, &s)
    return s.RuntimeCwd
}
```

- [ ] **Step 3: Make ExecutorClient (re)read RuntimeCwd before each Execute()**

The ExecutorClient at `internal/agent/executor_client.go` constructs `executortools.New(workDir)` once at startup. Refactor to consult `LoadRuntimeCwd(executorID)` on every tool call:

In `internal/agent/executor_client.go`, modify the tool dispatch path:

```go
func (c *ExecutorClient) currentWorkDir() string {
    if cwd := LoadRuntimeCwd(c.session.ExecutorID); cwd != "" {
        return cwd
    }
    return c.workDir
}

// Replace `c.executor.Execute(ctx, execReq)` calls with:
toolExec := executortools.New(c.currentWorkDir())
resp := toolExec.Execute(ctx, execReq)
```

(Constructing `ToolExecutor` per call is cheap — it's just a struct with a string field.)

- [ ] **Step 4: Wire from TUI Model**

In `internal/agent/tui/model.go`, replace the stub:

```go
// internal/agent/tui/runtime_cwd.go (new file)
package tui

import "github.com/agentserver/agentserver/internal/agent"

func writeRuntimeCwd(executorID, cwd string) error {
    if executorID == "" || cwd == "" { return nil }
    return agent.SetRuntimeCwd(executorID, cwd)
}
```

Remove the placeholder `func writeRuntimeCwd` from `model.go`.

- [ ] **Step 5: Write integration test**

```go
// internal/agent/tui/runtime_cwd_test.go
package tui

import (
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/agentserver/agentserver/internal/agent"
)

func TestRuntimeCwd_RoundTrip(t *testing.T) {
    home := t.TempDir()
    t.Setenv("HOME", home)
    // Pre-write an ExecutorSession JSON
    dir := filepath.Join(home, ".agentserver", "executors")
    _ = os.MkdirAll(dir, 0o700)
    p := filepath.Join(dir, "exe_a.json")
    _ = os.WriteFile(p, []byte(`{"executor_id":"exe_a","name":"x","workspace_id":"ws","tunnel_token":"t","registry_token":"r","server_url":"u","created_at":"`+time.Now().UTC().Format(time.RFC3339)+`"}`), 0o600)

    if err := agent.SetRuntimeCwd("exe_a", "/tmp/foo"); err != nil { t.Fatal(err) }
    if got := agent.LoadRuntimeCwd("exe_a"); got != "/tmp/foo" {
        t.Errorf("LoadRuntimeCwd=%q want /tmp/foo", got)
    }
    // Ensure other fields preserved
    sess, err := agent.LoadSessionByID("exe_a")  // add helper if absent
    if err != nil { t.Fatal(err) }
    if sess.RegistryToken != "r" {
        t.Errorf("RegistryToken corrupted: %+v", sess)
    }
}
```

If `LoadSessionByID` doesn't exist, add a thin wrapper in `executor_session.go`:
```go
func LoadSessionByID(executorID string) (*ExecutorSession, error) {
    dir, err := executorSessionsDir(); if err != nil { return nil, err }
    data, err := os.ReadFile(filepath.Join(dir, executorID+".json"))
    if err != nil { return nil, err }
    var s ExecutorSession
    if err := json.Unmarshal(data, &s); err != nil { return nil, err }
    return &s, nil
}
```

- [ ] **Step 6: Verify tests pass**

Run: `go test ./internal/agent/... -run "TestRuntimeCwd" -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/executor_session.go internal/agent/executor_client.go internal/agent/tui/runtime_cwd.go internal/agent/tui/model.go internal/agent/tui/runtime_cwd_test.go
git commit -m "feat(agent): /cd writes runtime_cwd; ExecutorClient reloads per-tool-call"
```

---

## Task 17: e2e smoke — TUI talks to fake backend

**Files:**
- Create: `internal/agent/tui/e2e_test.go`

This is the safety net before declaring Phase 2 done. Drives the Bubble Tea program against a fake agentserver and verifies the happy path: login → inbound → SSE → permission panel → decide → tool_result.

- [ ] **Step 1: Write e2e test**

```go
// internal/agent/tui/e2e_test.go
//go:build integration

package tui

import (
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "sync"
    "sync/atomic"
    "testing"
    "time"

    tea "github.com/charmbracelet/bubbletea"
)

func TestE2E_TUIHappyPath(t *testing.T) {
    var (
        sseTriggered atomic.Bool
        decideRcv    chan map[string]string = make(chan map[string]string, 1)
        sseW         http.ResponseWriter
        sseFlush     http.Flusher
        sseStarted   = make(chan struct{})
    )

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        switch {
        case strings.HasSuffix(r.URL.Path, "/tui/inbound"):
            w.WriteHeader(http.StatusAccepted)
            w.Write([]byte(`{"session_id":"cse_e","turn_id":"trn_e"}`))
            sseTriggered.Store(true)
        case strings.Contains(r.URL.Path, "/api/agent-sessions/cse_e/events"):
            w.Header().Set("Content-Type", "text/event-stream")
            sseW = w; sseFlush = w.(http.Flusher)
            close(sseStarted)
            // Wait until inbound triggers, then send events.
            for !sseTriggered.Load() { time.Sleep(10 * time.Millisecond) }
            fmt.Fprint(sseW, "id: 1\nevent: tool_use\ndata: {\"tool\":\"remote_bash\",\"executor_id\":\"exe_a\"}\n\n")
            sseFlush.Flush()
            fmt.Fprint(sseW, "id: 2\nevent: permission_request\ndata: {\"permission_id\":\"p1\",\"tool\":\"remote_bash\",\"executor_id\":\"exe_a\",\"args\":{}}\n\n")
            sseFlush.Flush()
            // Wait for decide
            select {
            case <-decideRcv:
            case <-time.After(2 * time.Second):
                t.Errorf("decide timeout")
            }
            fmt.Fprint(sseW, "id: 3\nevent: tool_result\ndata: {\"output\":\"ok\",\"exit_code\":0}\n\n")
            sseFlush.Flush()
            fmt.Fprint(sseW, "id: 4\nevent: turn_done\ndata: {}\n\n")
            sseFlush.Flush()
        case strings.Contains(r.URL.Path, "/permissions/p1"):
            var b map[string]string
            json.NewDecoder(r.Body).Decode(&b)
            decideRcv <- b
            w.WriteHeader(200)
        case strings.HasSuffix(r.URL.Path, "/api/agent-sessions/cse_e/attach"):
            w.Write([]byte(`{"session_id":"cse_e","permission_responder":"exe_a"}`))
        case strings.Contains(r.URL.Path, "/api/executors/exe_a/status"):
            w.Write([]byte(`{"executor_id":"exe_a","status":"online"}`))
        default:
            w.WriteHeader(404)
        }
    }))
    defer srv.Close()

    bus := NewBus(BusConfig{ServerURL: srv.URL, WorkspaceID: "ws", ExecutorID: "exe_a", Auth: &fakeAuth{tk: "t"}})
    model := NewModel(ModelConfig{
        ServerURL: srv.URL, WorkspaceID: "ws", ExecutorID: "exe_a",
        Bus: bus, Resume: "cse_e",
    })
    model.SetAuthState(AuthLoggedIn)

    // Drive synthetic Msgs
    var mu sync.Mutex
    captured := []tea.Msg{}
    capture := func(msg tea.Msg) { mu.Lock(); captured = append(captured, msg); mu.Unlock() }

    // Step 1: model Init → attach + status tick
    cmd := model.Init()
    if cmd != nil { _ = cmd() }

    // Wait for SSE handler to be ready (open in goroutine)
    go func() {
        sub := NewSSEConsumer(bus, SSEConfig{SessionID: "cse_e"})
        for ev := range sub.Run(t.Context()) {
            capture(EventArrivedMsg{Event: ev})
        }
    }()
    select {
    case <-sseStarted:
    case <-time.After(2 * time.Second):
        t.Fatal("sse not started")
    }

    // Step 2: simulate user typing & enter
    model.input.SetValue("hello")
    handled, sendCmd := model.handleNormalKey(tea.KeyMsg{Type: tea.KeyEnter})
    if !handled || sendCmd == nil { t.Fatal("send cmd not produced") }
    _ = sendCmd()

    // Step 3: drain captured EventArrived msgs and feed them to Update
    deadline := time.After(3 * time.Second)
    for {
        select {
        case <-deadline:
            t.Fatal("timed out waiting for events")
        default:
        }
        mu.Lock()
        if len(captured) == 0 { mu.Unlock(); time.Sleep(10 * time.Millisecond); continue }
        msg := captured[0]; captured = captured[1:]
        mu.Unlock()
        next, _ := model.Update(msg)
        model = next.(*Model)
        if model.activePanel != nil && model.activePanel.ID() == "p1" {
            // Simulate user pressing 'y'
            _, cmd, _ := model.activePanel.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
            if cmd == nil { t.Fatal("decide cmd nil") }
            decideMsg := cmd()
            next2, postCmd := model.Update(decideMsg)
            model = next2.(*Model)
            if postCmd == nil { t.Fatal("post-decision cmd nil") }
            go postCmd()  // POST to fake server
        }
        // Done when turn_done event lands in timeline
        if model.timeline.Len() > 0 {
            tail := model.timeline.items[len(model.timeline.items)-1]
            if tail.EventType == "turn_done" {
                break
            }
        }
    }
    _ = io.Discard
}
```

Run with: `go test -tags=integration ./internal/agent/tui/ -run TestE2E_TUIHappyPath -v -timeout 20s`

This test is intentionally messier than the unit tests — Bubble Tea's program loop is hard to invoke synchronously. The pragmatic shape: drive Update directly with synthesized Msgs, exercise the full HTTP / SSE pipeline through real `Bus` / `SSEConsumer`.

- [ ] **Step 2: Run e2e test**

Run: `go test -tags=integration ./internal/agent/tui/ -run TestE2E_TUIHappyPath -v -timeout 20s`
Expected: PASS.

- [ ] **Step 3: Manual smoke against real backend (Phase 1 deployed)**

Run: `go run ./cmd/agentserver-agent tui --server <real> --workspace-id <real>`
Expected:
- TUI opens; status bar shows logged out.
- `/login` opens panel with QR + URL; complete on browser.
- After login, status bar shows `auth: <user_id> · tunnel: online`.
- Type prompt → SSE events arrive → permission panel appears → press `y` → tool result appears → turn_done.

- [ ] **Step 4: Commit**

```bash
git add internal/agent/tui/e2e_test.go
git commit -m "test(tui): e2e happy path against fake backend (build tag: integration)"
```

---

## Final Verification

- [ ] **All unit tests**: `go test ./internal/agent/...`
- [ ] **e2e integration smoke**: `go test -tags=integration ./internal/agent/tui/ -v`
- [ ] **Build**: `make agent` (or `make agent-all` for cross-platform)
- [ ] **No regressions**: `go test ./...` (Phase 1 backend tests still pass)
- [ ] **Manual smoke against deployed Phase 1 backend** (Task 17 Step 3)
- [ ] **Update README** "Local Agent Tunneling" section: add "Interactive TUI" subsection (3 paragraphs per spec §9.4)

---

## Phase 2 Done Criteria

1. `agentserver tui --server <url> --workspace-id <id>` opens an interactive Bubble Tea TUI.
2. Without saved credentials, TUI starts in `LoggedOut` state with `/login` prompt.
3. `/login` runs OAuth Device Flow inside the TUI (panel with QR + URL); on success transitions to `LoggedIn`.
4. After login, ExecutorClient registers + maintains tunnel; status bar reflects state.
5. User types prompt → POST `/tui/inbound` → SSE events render in timeline.
6. Permission requests pop a confirmation panel; `y/a/n` produce correct decisions.
7. AskUser questions pop a selection panel; answers POST back.
8. `/clear` creates new session; `/resume <id>` switches; `/take-control` / `/observe` switch operator/observer.
9. `/model`, `/permission`, `/compact`, `/cost`, `/agents` forward to agentserver `/control`.
10. `/cd <path>` writes `runtime_cwd` to executor session JSON; ExecutorClient picks it up on next tool call.
11. `/attach <path>` adds a base64 attachment to the next prompt.
12. `/logout` clears credentials with confirmation.
13. SSE reconnects gracefully with Last-Event-ID after transient failure.
14. Token refresh transparent; refresh failure → LoggedOut.

