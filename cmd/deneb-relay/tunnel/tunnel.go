// Package tunnel is a Deneb relay's way in through a Cloudflare tunnel
// (design/plan/control-reachability-20261116.md §10). Where the relay's own
// address cannot be reached, a device dials wss://<tunnel host>/derp;
// Cloudflare passes the WebSocket on to cloudflared on the relay's machine,
// which brings it to a loopback port, served here. Inside the WebSocket is
// DERP, as derpserver.AddWebSocketSupport serves it to browsers: the same
// DERP server takes the connection, so admission, counting, rate limits
// and closing are those of a direct one.
//
// The device's address is CF-Connecting-IP, which Cloudflare sets whatever
// the device sent; admission and the log see it as they see a direct
// connection's. Cloudflare sees who connects, when, to whom (node keys)
// and how much, never what (WireGuard's keys are on the devices).
package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"

	"github.com/coder/websocket"
	"tailscale.com/derp/derpserver"
	"tailscale.com/net/wsconn"
)

// Path is where a device's WebSocket goes, under the tunnel's host name.
const Path = "/derp"

// Subprotocol is the WebSocket subprotocol a device must ask for.
const Subprotocol = "derp"

// Accept serves one DERP client's connection, from remoteAddr (the
// device's ip:port, port 0), until it ends. A relay wraps c, to count its
// bytes and close it, then hands it to its derpserver.Server.
type Accept func(ctx context.Context, c net.Conn, remoteAddr string)

// ServerAccept is Accept for a plain derpserver.Server, as in tests.
func ServerAccept(s *derpserver.Server) Accept {
	return func(ctx context.Context, c net.Conn, remoteAddr string) {
		s.Accept(ctx, c, bufio.NewReadWriter(bufio.NewReader(c), nil), remoteAddr)
	}
}

// Handler serves DERP over WebSocket at Path to cloudflared on this
// machine. A request must come from a loopback address and carry
// CF-Connecting-IP, or it gets 403; one that is not a WebSocket asking for
// Subprotocol gets 426; any other path 404.
func Handler(accept Accept, logf func(string, ...any)) http.Handler {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != Path {
			http.NotFound(w, r)
			return
		}
		from, ok := deviceAddr(r)
		if !ok {
			http.Error(w, "this port is for the Cloudflare tunnel only", http.StatusForbidden)
			return
		}
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || !strings.Contains(r.Header.Get("Sec-WebSocket-Protocol"), Subprotocol) {
			http.Error(w, "DERP over WebSocket, subprotocol "+Subprotocol, http.StatusUpgradeRequired)
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols: []string{Subprotocol},
			// A device is no browser: no Origin to check, and a page
			// elsewhere could not use a DERP connection anyway.
			OriginPatterns: []string{"*"},
			// WireGuard's packets do not compress.
			CompressionMode: websocket.CompressionDisabled,
		})
		if err != nil {
			logf("tunnel: %s: %v", from, err)
			return
		}
		defer c.Close(websocket.StatusInternalError, "closing")
		if c.Subprotocol() != Subprotocol {
			c.Close(websocket.StatusPolicyViolation, "the "+Subprotocol+" subprotocol only")
			return
		}
		remote := netip.AddrPortFrom(from, 0).String()
		accept(r.Context(), wsconn.NetConn(r.Context(), c, websocket.MessageBinary, remote), remote)
	})
}

// deviceAddr is the device a request from cloudflared is for: its
// CF-Connecting-IP, when the request comes from this machine.
func deviceAddr(r *http.Request) (netip.Addr, bool) {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !peer.Addr().IsLoopback() {
		return netip.Addr{}, false
	}
	from, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP")))
	if err != nil {
		return netip.Addr{}, false
	}
	return from.Unmap(), true
}

// CheckListen checks a -tunnel-listen address: a loopback one, as
// 127.0.0.1:47451, for cloudflared runs on the same machine.
func CheckListen(addr string) error {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil || !ap.Addr().IsLoopback() {
		return fmt.Errorf("-tunnel-listen %q: want a loopback address, as 127.0.0.1:47451; cloudflared runs on this machine", addr)
	}
	return nil
}

// hostRE is a host name with a dot in it, in lower case.
var hostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// CheckHost checks a -tunnel-host: the tunnel's host name alone, as
// relay.deneb.app, with no scheme, port or path.
func CheckHost(h string) error {
	if len(h) > 253 || !hostRE.MatchString(h) {
		return fmt.Errorf("-tunnel-host %q: want the tunnel's host name alone, as relay.deneb.app", h)
	}
	return nil
}
