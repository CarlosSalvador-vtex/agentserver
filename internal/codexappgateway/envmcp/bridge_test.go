package envmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"
)

// fakeExecServer accepts one ws connection, echoes each JSON-RPC
// request as a result whose body is the request's params, and exposes
// the last Authorization header it saw.
type fakeExecServer struct {
	srv        *httptest.Server
	gotAuth    string
	connectErr error
}

func newFakeExecServer(t *testing.T) *fakeExecServer {
	t.Helper()
	f := &fakeExecServer{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.gotAuth = r.Header.Get("Authorization")
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			f.connectErr = err
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var msg JSONRPCMessage
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			if msg.ID == nil {
				continue // notification, no reply
			}
			resp := JSONRPCMessage{JSONRPC: "2.0", ID: msg.ID, Result: msg.Params}
			out, _ := json.Marshal(&resp)
			_ = c.Write(ctx, websocket.MessageText, out)
		}
	}))
	return f
}

func (f *fakeExecServer) wsURL() string {
	return "ws" + strings.TrimPrefix(f.srv.URL, "http")
}

func (f *fakeExecServer) Close() { f.srv.Close() }

func TestBridgeClient_DialAndCall(t *testing.T) {
	f := newFakeExecServer(t)
	defer f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	bc, err := DialBridge(ctx, f.wsURL(), "tok-123")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer bc.Close()

	if f.gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization = %q, want %q", f.gotAuth, "Bearer tok-123")
	}

	res, err := bc.Call(ctx, "ping", json.RawMessage(`{"x":1}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if string(res) != `{"x":1}` {
		t.Errorf("result = %s", res)
	}
}

func TestBridgeClient_Notify_NoReply(t *testing.T) {
	f := newFakeExecServer(t)
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bc, err := DialBridge(ctx, f.wsURL(), "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer bc.Close()
	if err := bc.Notify(ctx, "initialized", nil); err != nil {
		t.Fatalf("notify: %v", err)
	}
}

func TestBridgeClient_Call_AfterClose_Errors(t *testing.T) {
	f := newFakeExecServer(t)
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bc, err := DialBridge(ctx, f.wsURL(), "")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	bc.Close()
	if _, err := bc.Call(ctx, "ping", nil); err == nil {
		t.Fatal("expected error after Close")
	}
}
