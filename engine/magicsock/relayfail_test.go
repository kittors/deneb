package magicsock

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/derp/derpserver"
	"tailscale.com/types/key"
)

// TestFailedRelays: what the device tells the server of the relays on its
// lists it could not use (Report.RelayFailures): nothing listening is
// connect, a key that is not the pinned one is tls, a relay whose key
// checks out and that then turns the device away is refused, each with
// when it began; the engine is told. One that answers again leaves the
// list, and the engine is told that too.
func TestFailedRelays(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nothing answers there now

	quietTLS := func(h http.Handler) *httptest.Server {
		ts := httptest.NewUnstartedServer(h)
		ts.Config.ErrorLog = log.New(io.Discard, "", 0) // the failed handshakes
		ts.StartTLS()
		t.Cleanup(ts.Close)
		return ts
	}
	other := quietTLS(http.NotFoundHandler())
	s := derpserver.New(key.NewNode(), func(string, ...any) {})
	defer s.Close()
	var serve atomic.Bool
	derp := derpserver.Handler(s)
	own := quietTLS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !serve.Load() {
			http.Error(w, "not now", http.StatusServiceUnavailable)
			return
		}
		derp.ServeHTTP(w, r)
	}))
	relay := func(id proto.RelayID, port int, pin string) proto.Relay {
		return proto.Relay{ID: id, Name: string(id), HostName: "example.com", IPv4: netip.MustParseAddr("127.0.0.1"), DERPPort: uint16(port), Pin: pin}
	}
	portOf := func(ts *httptest.Server) int { return ts.Listener.Addr().(*net.TCPAddr).Port }
	relays := []proto.Relay{
		relay("dead", deadPort, ""),
		relay("wrongkey", portOf(other), "sha256:"+strings.Repeat("0", 64)),
		relay("own", portOf(own), proto.Fingerprint(own.Certificate())),
	}

	var told atomic.Int32
	c, err := New(key.NewNode(), [32]byte{1}, Options{Logf: func(string, ...any) {}, OnEndpoints: func() { told.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now().UTC().Truncate(time.Second)
	c.SetRelays(relays, []RelayList{
		{Scope: "lan/a", Relays: []proto.RelayID{"dead"}},
		{Scope: "lan/b", Relays: []proto.RelayID{"wrongkey"}},
		{Scope: "lan/c", Relays: []proto.RelayID{"own"}},
	}, "")
	toldAtStart := told.Load()
	waitFor(t, "three relays on record as failed", func() bool { return len(c.FailedRelays()) == 3 })
	want := map[proto.RelayID]string{"dead": proto.RelayFailConnect, "wrongkey": proto.RelayFailTLS, "own": proto.RelayFailRefused}
	for _, f := range c.FailedRelays() {
		if f.Reason != want[f.Relay] || f.Refused != "" || f.Since.Before(start) || f.Since.After(time.Now()) {
			t.Errorf("%s: %+v, want reason %s, since the test began", f.Relay, f, want[f.Relay])
		}
	}
	if told.Load() <= toldAtStart {
		t.Error("the engine was not told of the failures")
	}

	serve.Store(true)
	toldBefore := told.Load()
	c.mu.Lock()
	c.relayFail["own"].until = time.Now().Add(-time.Second) // its backoff ran out
	c.mu.Unlock()
	c.rechooseRelays()
	waitFor(t, "own off the list once it answers", func() bool {
		for _, f := range c.FailedRelays() {
			if f.Relay == "own" {
				return false
			}
		}
		return true
	})
	if got := c.FailedRelays(); len(got) != 2 {
		t.Errorf("failed relays now %+v, want dead and wrongkey", got)
	}
	if told.Load() == toldBefore {
		t.Error("the engine was not told that own answers again")
	}
}

// TestFailedRelaysBookkeeping: a relay that dropped is not told until a
// try to connect again fails, and keeps when its run of failures began;
// the same failure again is no news; a relay on no list is not told; a
// refusal is forgotten when the lists change (who lets the device in may
// have changed), and the engine hears of that.
func TestFailedRelaysBookkeeping(t *testing.T) {
	var told atomic.Int32
	c, err := New(key.NewNode(), [32]byte{1}, Options{Logf: func(string, ...any) {}, OnEndpoints: func() { told.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	relays := []proto.Relay{{ID: "a", Name: "a"}, {ID: "b", Name: "b"}, {ID: "x", Name: "x"}} // no port: none is dialed
	c.SetRelays(relays, []RelayList{{Scope: "lan/s", Relays: []proto.RelayID{"a", "b"}}}, "")

	t0 := time.Date(2026, 11, 15, 6, 0, 0, 0, time.UTC)
	c.mu.Lock()
	news := []bool{
		c.noteRelayFailForLocked("a", "lost: EOF", "", "", t0, time.Second),
		c.noteRelayFailLocked("a", "unreachable: refused", proto.RelayFailConnect, "", t0.Add(5*time.Second)),
		c.noteRelayFailLocked("a", "unreachable: refused", proto.RelayFailConnect, "", t0.Add(20*time.Second)),
		c.noteRelayFailLocked("b", "refused: notListed", proto.RelayFailRefused, proto.RelayDenyNotListed, t0),
		c.noteRelayFailLocked("x", "unreachable: timeout", proto.RelayFailTimeout, "", t0),
	}
	c.mu.Unlock()
	if want := []bool{false, true, false, true, true}; !slices.Equal(news, want) {
		t.Errorf("news %v, want %v", news, want)
	}
	got := c.FailedRelays()
	want := []proto.RelayFailure{
		{Relay: "a", Reason: proto.RelayFailConnect, Since: t0},
		{Relay: "b", Reason: proto.RelayFailRefused, Refused: proto.RelayDenyNotListed, Since: t0},
	}
	if len(got) != len(want) {
		t.Fatalf("failed relays %+v, want %+v", got, want)
	}
	for i := range want {
		if g, w := got[i], want[i]; g.Relay != w.Relay || g.Reason != w.Reason || g.Refused != w.Refused || !g.Since.Equal(w.Since) {
			t.Errorf("failed relay %d: %+v, want %+v", i, g, w)
		}
	}

	toldBefore := told.Load()
	c.SetRelays(relays, []RelayList{{Scope: "lan/s", Relays: []proto.RelayID{"b", "a"}}}, "") // the list changed
	if got := c.FailedRelays(); len(got) != 1 || got[0].Relay != "a" {
		t.Errorf("after the list changed: %+v, want only a (b's refusal forgotten)", got)
	}
	if told.Load() == toldBefore {
		t.Error("the engine was not told")
	}
}

// TestRelayFailReason: why a relay never came up, as the server is told
// it: the dial's own reason when the dial failed (derphttp keeps only the
// text), else from the client's text.
func TestRelayFailReason(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&net.OpError{Op: "dial", Net: "tcp4", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}, proto.RelayFailConnect},
		{&net.OpError{Op: "dial", Net: "tcp4", Err: os.ErrDeadlineExceeded}, proto.RelayFailTimeout},
		{context.DeadlineExceeded, proto.RelayFailTimeout},
		{&tlsDialError{errors.New("control server key sha256:1 is not the pinned sha256:2")}, proto.RelayFailTLS},
		{&tlsDialError{os.ErrDeadlineExceeded}, proto.RelayFailTLS}, // TCP went through: what answers does not speak TLS
	} {
		if got := dialFailReason(tc.err); got != tc.want {
			t.Errorf("dial %v: %s, want %s", tc.err, got, tc.want)
		}
	}

	through, connect := "", proto.RelayFailConnect
	for _, tc := range []struct {
		pin    string
		dialed *string
		err    string
		want   string
	}{
		{"", nil, "derphttp.Client.Recv connect to example.com: client closed", proto.RelayFailConnect},
		{"", &connect, "derphttp.Client.Recv connect to example.com: dial tcp4 127.0.0.1:1: connect: connection refused", proto.RelayFailConnect},
		{"sha256:1", &through, `derphttp.Client.Recv connect to example.com: GET failed: status 503 Service Unavailable: "not now\n"`, proto.RelayFailRefused},
		{"sha256:1", &through, "derphttp.Client.Recv connect to example.com: EOF", proto.RelayFailRefused},
		{"sha256:1", &through, "context deadline exceeded: derphttp.Client.Recv connect to example.com: read tcp 127.0.0.1:2: i/o timeout", proto.RelayFailTimeout},
		{"", &through, "derphttp.Client.Recv connect to example.com: tls: failed to verify certificate: x509: certificate signed by unknown authority", proto.RelayFailTLS},
		{"", &through, "derphttp.Client.Recv connect to example.com: EOF", proto.RelayFailTLS},
	} {
		rc := &relayConn{relay: proto.Relay{Pin: tc.pin}}
		if tc.dialed != nil {
			rc.dialed.Store(tc.dialed)
		}
		if got := rc.failReason(errors.New(tc.err)); got != tc.want {
			t.Errorf("pin %q, dialed %v, %q: %s, want %s", tc.pin, tc.dialed != nil, tc.err, got, tc.want)
		}
	}
}

// waitFor waits up to 10 s for ok.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("waited 10 s for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
