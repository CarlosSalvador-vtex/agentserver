package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agentserver/agentserver/internal/agent"
	"github.com/agentserver/agentserver/internal/agent/tui"
)

// defaultServerURL mirrors cmd/agentserver-agent/main.go: when the user
// doesn't pass --server and has no saved credentials, fall back to this.
const defaultServerURL = "https://agent.cs.ac.cn"

func init() {
	agent.RunTUIFunc = runTUI
}

// runTUI is the real implementation of RunTUI. It wires AuthController
// (credential lifecycle), Bus (HTTP client), Bubble Tea program (UI), and
// — when authenticated — an ExecutorClient (yamux tunnel for cc-broker
// remote_* tool calls).
//
// Architecture: three goroutines share only OAuth credentials, no in-process
// control flow between them:
//  1. Bubble Tea program (UI thread, this function blocks on it via Run).
//  2. ExecutorClient (yamux to executor-registry; spawned only if logged in).
//  3. AuthController (callback from a polling goroutine).
func runTUI(ctx context.Context, opts agent.TUIOpts) error {
	// 1. Resolve server URL — flag wins, then saved creds, then the default.
	server := opts.Server
	creds, _ := agent.LoadCredentials(agent.DefaultCredentialsPath())
	if server == "" && creds != nil {
		server = creds.ServerURL
	}
	if server == "" {
		server = defaultServerURL
	}

	// 2. Resolve workspace ID locally if possible (flag → saved executor
	//    session for this server). If neither yields one, defer resolution
	//    until after login (see resolveWorkspacePostLogin below).
	workspaceID := opts.WorkspaceID
	if workspaceID == "" {
		if sess, err := agent.LoadAnyExecutorSessionForServer(server); err == nil && sess != nil {
			workspaceID = sess.WorkspaceID
		}
	}

	workDir := opts.WorkDir
	if workDir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		workDir = cwd
	}

	// 2. Build AuthController.
	auth := tui.NewAuthController(tui.AuthConfig{
		ServerURL:       server,
		CredentialsPath: agent.DefaultCredentialsPath(),
		SkipOpenBrowser: opts.SkipOpenBrowser,
	})

	var executorID string

	// 3. Build Bus (executor ID may be empty if not yet logged in; SetExecutorID
	//    is called by startExecutor after registration. WorkspaceID may also
	//    be empty if the user didn't pass --workspace-id and no saved session
	//    matched; SetWorkspaceID is called by resolveWorkspacePostLogin.)
	bus := tui.NewBus(tui.BusConfig{
		ServerURL:   server,
		WorkspaceID: workspaceID,
		ExecutorID:  executorID,
		Auth:        auth,
	})

	// 4. Build the Bubble Tea program placeholder so callbacks can call p.Send.
	//    We assign model below, after defining the callbacks.
	var p *tea.Program

	// restartSSE (re)starts the SSE consumer goroutine for the given session.
	// Any previous consumer is cancelled first.
	var sseCancel context.CancelFunc
	var sseMu sync.Mutex
	restartSSE := func(sid string) {
		if sid == "" {
			return
		}
		sseMu.Lock()
		defer sseMu.Unlock()
		if sseCancel != nil {
			sseCancel() // cancel any previous consumer
		}
		sseCtx, cancel := context.WithCancel(ctx)
		sseCancel = cancel
		busCapture := bus // capture for goroutine
		go func() {
			consumer := tui.NewSSEConsumer(busCapture, tui.SSEConfig{SessionID: sid})
			evCh := consumer.Run(sseCtx)
			for ev := range evCh {
				p.Send(tui.EventArrivedMsg{Event: ev})
			}
		}()
	}

	// resolveAndSetWorkspace fills in bus.WorkspaceID by querying the server
	// when it isn't already known. Returns nil on success (workspace already
	// set, or just resolved). Errors are non-terminal — the caller surfaces
	// them via FatalErrorMsg and the user may retry by creating a workspace
	// then restarting the TUI.
	resolveAndSetWorkspace := func() error {
		if bus.WorkspaceID() != "" {
			return nil
		}
		listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		ws, err := bus.ListWorkspaces(listCtx)
		if err != nil {
			return fmt.Errorf("list workspaces: %w", err)
		}
		if len(ws) == 0 {
			return fmt.Errorf("no workspaces found for this account; create one in the web UI first or pass --workspace-id")
		}
		sort.Slice(ws, func(i, j int) bool { return ws[i].CreatedAt > ws[j].CreatedAt })
		bus.SetWorkspaceID(ws[0].ID)
		return nil
	}

	// startExecutor resolves the workspace (if needed), then registers the
	// executor with the server (once per process) and starts the yamux tunnel
	// goroutine. Called on login or at startup if already logged in. Workspace
	// resolution is OUTSIDE the once-guard so a transient failure (e.g.
	// network) doesn't permanently lock out registration on subsequent /login
	// attempts.
	var executorOnce sync.Once
	startExecutor := func() {
		if err := resolveAndSetWorkspace(); err != nil {
			p.Send(tui.FatalErrorMsg{Err: err})
			return
		}
		wsID := bus.WorkspaceID()
		executorOnce.Do(func() {
			sess, err := agent.LoadOrRegisterExecutor(agent.ExecutorOpts{
				ServerURL:   server,
				Name:        opts.Name,
				WorkspaceID: wsID,
			})
			if err != nil {
				p.Send(tui.FatalErrorMsg{Err: fmt.Errorf("register executor after login: %w", err)})
				return
			}
			bus.SetExecutorID(sess.ExecutorID)
			ec := agent.NewExecutorClient(sess, workDir)
			go func() { _ = ec.Run(ctx) }()
		})
	}

	// 5. Build Model with lifecycle callbacks.
	model := tui.NewModel(tui.ModelConfig{
		ServerURL:      server,
		WorkspaceID:    workspaceID,
		ExecutorID:     executorID,
		Bus:            bus,
		Auth:           auth,
		Yolo:           opts.Yolo,
		InitialModel:   opts.Model,
		Resume:         opts.Resume,
		Continue:       opts.Continue,
		OnLoggedIn:     startExecutor,
		OnSessionReady: restartSSE,
	})

	// 6. Build the Bubble Tea program and wire AuthController.OnChange to
	//    pump AuthStateChangedMsg into the program.
	p = tea.NewProgram(model, tea.WithContext(ctx), tea.WithAltScreen())
	auth.SetOnChange(func(s tui.AuthState) {
		p.Send(tui.AuthStateChangedMsg{State: s})
	})

	// 7. Wire the OnLoginFailed callback (surfaces OAuth errors to the TUI timeline).
	auth.SetOnLoginFailed(func(err error) {
		p.Send(tui.LoginPollDoneMsg{Err: err})
	})

	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
