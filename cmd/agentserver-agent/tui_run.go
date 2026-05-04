package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agentserver/agentserver/internal/agent"
	"github.com/agentserver/agentserver/internal/agent/tui"
)

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
	// 1. Resolve server URL — flag wins, then saved creds.
	server := opts.Server
	creds, _ := agent.LoadCredentials(agent.DefaultCredentialsPath())
	if server == "" && creds != nil {
		server = creds.ServerURL
	}
	if opts.WorkspaceID == "" {
		return fmt.Errorf("--workspace-id is required")
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
	//    is called by startExecutor after registration).
	bus := tui.NewBus(tui.BusConfig{
		ServerURL:   server,
		WorkspaceID: opts.WorkspaceID,
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

	// startExecutor registers the executor with the server (once) and starts
	// the yamux tunnel goroutine. Called on login or at startup if already
	// logged in.
	var executorOnce sync.Once
	startExecutor := func() {
		executorOnce.Do(func() {
			sess, err := agent.LoadOrRegisterExecutor(agent.ExecutorOpts{
				ServerURL:   server,
				Name:        opts.Name,
				WorkspaceID: opts.WorkspaceID,
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
		WorkspaceID:    opts.WorkspaceID,
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
