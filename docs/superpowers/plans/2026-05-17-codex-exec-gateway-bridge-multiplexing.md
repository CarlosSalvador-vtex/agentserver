# codex-exec-gateway Bridge Multiplexing — Plan

> **For agentic workers:** Use superpowers:subagent-driven-development. Steps tracked via `- [ ]` checkboxes.

**Goal:** Replace one-bridge-per-executor serialisation with stream_id-multiplexed concurrent bridges. Spec: `docs/superpowers/specs/2026-05-17-codex-exec-gateway-bridge-multiplexing.md`.

**Tech:** Go 1.22, nhooyr.io/websocket, protobuf relay envelopes (existing in `internal/codexappgateway/envmcp/relaypb`).

---

## Task 1 — Promote relaypb to a shared package

**Files:**
- Move: `internal/codexappgateway/envmcp/relaypb/` → `internal/relaypb/`
- Update imports in: `internal/codexappgateway/envmcp/bridge.go`, `envmcp/pool.go`, any test fixtures

The codex-exec-gateway now needs to parse relay frames too, and circular import codexappgateway→codexexecgateway must be avoided. Promote to a leaf package.

- [ ] git mv the package directory.
- [ ] Sed-replace import paths in envmcp side.
- [ ] Regenerate proto isn't needed (the .pb.go's `go_package` option points to new path — verify and update).
- [ ] `go build ./... && go test ./internal/codexappgateway/envmcp/...` passes.
- [ ] Commit `refactor: move relaypb to top-level shared package`.

---

## Task 2 — `multiplex.go`: `inboundConn` + `bridgeSession` types

**File:** Create `internal/codexexecgateway/multiplex.go`

- [ ] Define types per spec:
  ```go
  type inboundConn struct {
      exeID    string
      ws       *websocket.Conn
      writeMu  sync.Mutex
      routesMu sync.RWMutex
      routes   map[string]*bridgeSession
      closed   chan struct{}
      closeErr error
      logger   *slog.Logger
  }

  type bridgeSession struct {
      streamID string
      inbound  *inboundConn
      bridgeWS *websocket.Conn
      writeMu  sync.Mutex
      closed   chan struct{}
  }
  ```
- [ ] Methods:
  - `newInboundConn(exeID string, ws *websocket.Conn, logger *slog.Logger) *inboundConn`
  - `(i *inboundConn) addRoute(streamID string, b *bridgeSession) (evicted *bridgeSession)`
  - `(i *inboundConn) removeRoute(streamID string, b *bridgeSession)` — only removes if route still points to b (safe under race)
  - `(i *inboundConn) write(ctx, mt, data) error` — under writeMu
  - `(i *inboundConn) close(err error)` — set closeErr, close `closed`; idempotent
  - `(b *bridgeSession) write(ctx, mt, data) error` — under bridge.writeMu
  - `(b *bridgeSession) close(err error)` — idempotent
- [ ] Unit tests for race safety: parallel addRoute/removeRoute/close, ensure no panics + correct final state.

- [ ] Commit `feat(codex-exec-gateway): inboundConn + bridgeSession types`.

---

## Task 3 — Rewire `ConnRegistry`

**Files:**
- Modify: `internal/codexexecgateway/conn_registry.go` (or wherever Registry lives)
- Modify all callers

- [ ] Registry now stores `*inboundConn`, not `*websocket.Conn`:
  ```go
  type ConnRegistry struct {
      mu sync.Mutex
      conns map[string]*inboundConn
  }
  func (r *ConnRegistry) Register(exeID string, ic *inboundConn) (evicted *inboundConn)
  func (r *ConnRegistry) Lookup(exeID string) (*inboundConn, bool)
  func (r *ConnRegistry) Unregister(exeID string, ic *inboundConn)
  func (r *ConnRegistry) ConnectedIDs() []string
  ```
- [ ] Drop `AcquireBridge` / `ReleaseBridge` — no longer needed.
- [ ] Update bridge_test fakeRegistry / mocks accordingly.

- [ ] Commit `refactor(codex-exec-gateway): ConnRegistry holds *inboundConn`.

---

## Task 4 — Inbound handler: spawn the reader goroutine

**File:** `internal/codexexecgateway/inbound.go`

- [ ] After registering, start `go ic.runReader(ctx)`.
- [ ] runReader loop:
  ```go
  for {
      mt, data, err := i.ws.Read(ctx)
      if err != nil { i.close(err); return }
      if mt != websocket.MessageBinary { continue } // ignore text frames
      var frame relaypb.RelayMessageFrame
      if err := proto.Unmarshal(data, &frame); err != nil {
          i.logger.Warn("inbound: relay frame parse failed", "err", err)
          continue
      }
      i.routesMu.RLock()
      b, ok := i.routes[frame.StreamId]
      i.routesMu.RUnlock()
      if !ok {
          i.logger.Debug("inbound: no route for stream", "stream_id", frame.StreamId)
          continue
      }
      if err := b.write(ctx, mt, data); err != nil {
          i.logger.Warn("inbound: bridge write failed", "stream_id", frame.StreamId, "err", err)
          b.close(err)
          i.removeRoute(frame.StreamId, b)
          continue
      }
      // If Reset, drop the route after forwarding.
      if _, ok := frame.Body.(*relaypb.RelayMessageFrame_Reset_); ok {
          i.removeRoute(frame.StreamId, b)
      }
  }
  ```
- [ ] Remove the existing `<-r.Context().Done()` block; the reader goroutine owns the lifecycle.
- [ ] When inbound exits, fan out close to all routes:
  ```go
  func (i *inboundConn) close(err error) {
      // Set closeErr, close `closed`, then close each route's bridgeWS.
  }
  ```

- [ ] Commit `feat(codex-exec-gateway): inbound conn runs a relay-aware reader`.

---

## Task 5 — Bridge handler: peek Resume, register, simpler pump

**File:** `internal/codexexecgateway/bridge.go`

- [ ] Replace the existing pump-pair model:
  ```go
  func (s *Server) handleBridge(w http.ResponseWriter, r *http.Request) {
      // ... auth (unchanged) ...
      inbound, ok := s.registry.Lookup(exeID)
      if !ok { http.Error(w, ..., 503); return }
      bridgeWS, err := websocket.Accept(...)
      if err != nil { return }
      bridgeWS.SetReadLimit(maxWSFrameBytes)

      // Peek first frame — must be Resume.
      mt, first, err := bridgeWS.Read(r.Context())
      if err != nil || mt != websocket.MessageBinary {
          bridgeWS.Close(StatusProtocolError, "first frame must be binary Resume")
          return
      }
      var firstFrame relaypb.RelayMessageFrame
      if err := proto.Unmarshal(first, &firstFrame); err != nil {
          bridgeWS.Close(StatusProtocolError, "malformed first frame")
          return
      }
      if _, ok := firstFrame.Body.(*relaypb.RelayMessageFrame_Resume); !ok {
          bridgeWS.Close(StatusProtocolError, "first frame must be Resume")
          return
      }
      streamID := firstFrame.StreamId
      if streamID == "" {
          bridgeWS.Close(StatusProtocolError, "Resume missing stream_id"); return
      }

      // Register.
      session := &bridgeSession{streamID: streamID, inbound: inbound, bridgeWS: bridgeWS, closed: make(chan struct{})}
      if evicted := inbound.addRoute(streamID, session); evicted != nil {
          s.logger.Warn("bridge: stream_id collision; evicting prior", "stream_id", streamID, "exe_id", exeID)
          evicted.close(errors.New("evicted by stream_id collision"))
      }
      defer func() {
          inbound.removeRoute(streamID, session)
          session.close(nil)
      }()

      // Forward the Resume frame to inbound.
      if err := inbound.write(r.Context(), websocket.MessageBinary, first); err != nil {
          s.logger.Warn("bridge: forward Resume failed", "err", err)
          return
      }

      // Pump bridge → inbound until either side closes.
      for {
          select {
          case <-session.closed: return
          case <-inbound.closed: return
          case <-r.Context().Done(): return
          default:
          }
          mt, data, err := bridgeWS.Read(r.Context())
          if err != nil { return }
          if mt != websocket.MessageBinary { continue }
          // Optional: validate stream_id in the frame matches session.streamID.
          // Drop on mismatch instead of forwarding (defends against env-mcp bug).
          var f relaypb.RelayMessageFrame
          if proto.Unmarshal(data, &f) == nil && f.StreamId != streamID {
              s.logger.Warn("bridge: ignoring frame with wrong stream_id", "want", streamID, "got", f.StreamId)
              continue
          }
          if err := inbound.write(r.Context(), mt, data); err != nil { return }
      }
  }
  ```
- [ ] Drop the old `pumpFramesDebug` per-direction pumps. The diag logging is gone (we can re-add a single log per frame at debug level inside the reader if needed).
- [ ] Drop `AcquireBridge` / `ReleaseBridge` calls.

- [ ] Commit `feat(codex-exec-gateway): /bridge handler peeks Resume + registers stream route`.

---

## Task 6 — End-to-end test: two concurrent bridges

**File:** Modify `internal/codexexecgateway/bridge_test.go`

- [ ] New test `TestBridge_TwoConcurrentBridgesShareInbound`:
  - Set up exec-gateway with a real inbound conn (fake codex exec-server speaking the relay protocol).
  - Open two `/bridge/{exe_id}` connections concurrently, each with a unique stream_id.
  - Each bridge sends a process/start (wrapped in Data frame) with distinct processId.
  - Verify both succeed (no 409, no frame interleaving): process/start response on bridge A has bridge A's id, etc.
- [ ] New test `TestBridge_StreamIdCollisionEvictsFirst`:
  - Open one bridge with stream_id X, send Resume.
  - Open second bridge with same X.
  - Verify first bridge's ws closes; second stays open.
- [ ] New test `TestBridge_FrameWithWrongStreamId_Dropped`:
  - Open bridge with stream_id X.
  - Send a Data frame with stream_id Y.
  - Verify inbound never receives that frame.

- [ ] Commit `test(codex-exec-gateway): multiplexed bridge end-to-end coverage`.

---

## Task 7 — Chart bump + release v0.53.0

**File:** `deploy/helm/agentserver/Chart.yaml`

- [ ] Bump to `0.53.0` (minor — concurrency behavior change).
- [ ] `go test ./...` clean.
- [ ] Commit + tag + push.
- [ ] CI build.
- [ ] Pulumi stack bump to `0.53.0`; preview; deploy with user authorization.
- [ ] Rollout restart `agentserver-codex-exec-gateway` only (env-mcp unchanged → app-gateway untouched).
- [ ] Smoke test: spawn 2+ codex sub-agents that hit the same env_id in parallel; expect both to succeed.

---

## Out of scope (record for v2)

- Per-inbound max concurrent streams cap (64?)
- Per-stream metrics
- Stream resumption across inbound reconnects
- Removing the diag frame logger entirely (or gating behind a config flag)
