package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"nhooyr.io/websocket"
)

// Conn is one loopback ws to a codex app-server subprocess. Safe for
// concurrent Turn() / StartThread() calls — internally serializes
// writes and demuxes notifications by turnId.
type Conn struct {
	ws        *websocket.Conn
	writeMu   sync.Mutex
	nextID    atomic.Int64
	closeOnce sync.Once
	closed    chan struct{}
}

// dialAndHandshake dials wsURL, runs initialize → initialized, returns
// a ready-to-use Conn. Caller must Close() it.
func dialAndHandshake(ctx context.Context, wsURL string) (*Conn, error) {
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled, // codex rejects permessage-deflate
	})
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", wsURL, err)
	}
	ws.SetReadLimit(64 << 20) // match server-side limit

	c := &Conn{ws: ws, closed: make(chan struct{})}

	// initialize (request)
	initParams := json.RawMessage(`{"clientInfo":{"name":"agentserver-codex-broker","version":"0.1.0"},"protocolVersion":"2025-06-18","capabilities":{}}`)
	if _, err := c.callRaw(ctx, "initialize", initParams); err != nil {
		ws.Close(websocket.StatusInternalError, "")
		return nil, fmt.Errorf("initialize: %w", err)
	}

	// initialized (notification)
	if err := c.notifyRaw(ctx, "initialized", json.RawMessage(`{}`)); err != nil {
		ws.Close(websocket.StatusInternalError, "")
		return nil, fmt.Errorf("initialized: %w", err)
	}
	return c, nil
}

// Close shuts down the ws. Safe to call multiple times.
func (c *Conn) Close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.ws.Close(websocket.StatusNormalClosure, "")
	})
}

// callRaw sends a JSON-RPC request and synchronously reads frames until
// the matching response is found. THIS IS A STUB for handshake only —
// real call flow goes through the demuxed reader in later tasks.
func (c *Conn) callRaw(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	req := rpcRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: params}
	if err := c.writeJSON(ctx, req); err != nil {
		return nil, err
	}
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}
		var resp rpcResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		if resp.ID != nil && *resp.ID == id {
			if resp.Error != nil {
				return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
			}
			return resp.Result, nil
		}
		// Drop notifications during handshake; demux comes later.
	}
}

func (c *Conn) notifyRaw(ctx context.Context, method string, params json.RawMessage) error {
	return c.writeJSON(ctx, rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *Conn) writeJSON(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.ws.Write(ctx, websocket.MessageText, b)
}
