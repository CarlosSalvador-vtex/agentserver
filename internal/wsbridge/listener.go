package wsbridge

import (
	"context"
	"net"
	"time"
)

// TCPKeepAlivePeriod is the TCP keepalive period the gateways use on
// every accepted connection. 15s strikes the balance between probing
// often enough to defeat aggressive idle middleboxes (NAT/CGNAT
// timeouts as low as 60-180s) and not flooding healthy paths.
//
// On Linux, Go's net.ListenConfig.KeepAlive sets both TCP_KEEPIDLE
// (idle before first probe) and TCP_KEEPINTVL (between probes) to
// this value, with TCP_KEEPCNT defaulting to 9 — so a dead TCP path
// is detected ~2.5 minutes after the last user/ws-PING traffic.
const TCPKeepAlivePeriod = 15 * time.Second

// ListenWithKeepAlive opens a TCP listener with SO_KEEPALIVE enabled
// and the keepalive period set to TCPKeepAlivePeriod on every
// accepted connection. This complements ws-level PING (RFC 6455
// control frames) by giving us TCP-layer liveness probes that
// middleboxes which only inspect TCP traffic can also see.
func ListenWithKeepAlive(ctx context.Context, network, addr string) (net.Listener, error) {
	lc := net.ListenConfig{KeepAlive: TCPKeepAlivePeriod}
	return lc.Listen(ctx, network, addr)
}
