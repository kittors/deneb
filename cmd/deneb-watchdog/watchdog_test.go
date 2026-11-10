package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kittors/deneb/control/alert"
	"github.com/kittors/deneb/proto"
	"tailscale.com/net/stun"
)

// fakeServer is a control server's /healthz and a relay's /derp/probe in
// one, over TLS, answering 200 or, when broken, 500.
func fakeServer(t *testing.T) (*httptest.Server, *atomic.Bool) {
	var broken atomic.Bool
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() || r.URL.Path != "/healthz" && r.URL.Path != "/derp/probe" {
			http.Error(w, "no", 500)
			return
		}
		w.Write([]byte("ok"))
	}))
	t.Cleanup(ts.Close)
	return ts, &broken
}

// fakeSTUN answers STUN requests on 127.0.0.1.
func fakeSTUN(t *testing.T) string {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			tx, err := stun.ParseBindingRequest(buf[:n])
			if err != nil {
				continue
			}
			ap := netip.MustParseAddrPort(from.String())
			pc.WriteTo(stun.Response(tx, ap), from)
		}
	}()
	return pc.LocalAddr().String()
}

func ready(t *testing.T, c Check) Check {
	t.Helper()
	if err := c.prepare(); err != nil {
		t.Fatalf("%+v: %v", c, err)
	}
	return c
}

func runCheck(c Check) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return c.run(ctx)
}

// TestChecks: each kind passes when all is well and says why when not.
func TestChecks(t *testing.T) {
	ts, broken := fakeServer(t)
	addr := ts.Listener.Addr().String()
	pin := proto.Fingerprint(ts.Certificate())
	wrongPin := "sha256:" + strings.Repeat("ab", 32)

	// https, pinned.
	if err := runCheck(ready(t, Check{Name: "a", Kind: "https", URL: ts.URL + "/healthz", Pin: pin})); err != nil {
		t.Fatalf("https: %v", err)
	}
	if err := runCheck(ready(t, Check{Name: "a", Kind: "https", URL: ts.URL + "/healthz", Pin: wrongPin})); err == nil || !strings.Contains(err.Error(), "证书") {
		t.Fatalf("https, another key: %v", err)
	}
	broken.Store(true)
	if err := runCheck(ready(t, Check{Name: "a", Kind: "https", URL: ts.URL + "/healthz", Pin: pin})); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("https, a 500: %v", err)
	}
	broken.Store(false)

	// derp: pinned, or by name against the CAs.
	if err := runCheck(ready(t, Check{Name: "a", Kind: "derp", Addr: addr, Pin: pin})); err != nil {
		t.Fatalf("derp: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	testRoots = pool
	defer func() { testRoots = nil }()
	if err := runCheck(ready(t, Check{Name: "a", Kind: "derp", Addr: addr, ServerName: "example.com"})); err != nil {
		t.Fatalf("derp by name: %v", err)
	}
	if err := runCheck(ready(t, Check{Name: "a", Kind: "derp", Addr: addr, ServerName: "relay.example.net"})); err == nil {
		t.Fatal("derp under a name its certificate does not have")
	}

	// stun.
	if err := runCheck(ready(t, Check{Name: "a", Kind: "stun", Addr: fakeSTUN(t)})); err != nil {
		t.Fatalf("stun: %v", err)
	}
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	quiet := pc.LocalAddr().String() // takes the packets, never answers
	defer pc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	silent := ready(t, Check{Name: "a", Kind: "stun", Addr: quiet})
	if err := silent.run(ctx); err == nil {
		t.Fatal("stun with no answer")
	}

	// backup.
	dir := t.TempDir()
	b := ready(t, Check{Name: "a", Kind: "backup", Dir: dir})
	if err := runCheck(b); err == nil || !strings.Contains(err.Error(), "一份备份都没有") {
		t.Fatalf("no backup: %v", err)
	}
	path := filepath.Join(dir, "deneb-20261110.db")
	os.WriteFile(path, []byte("x"), 0o600)
	if err := runCheck(b); err != nil {
		t.Fatalf("a fresh backup: %v", err)
	}
	old := time.Now().Add(-30 * time.Hour)
	os.Chtimes(path, old, old)
	if err := runCheck(b); err == nil || !strings.Contains(err.Error(), "30 小时") {
		t.Fatalf("a stale backup: %v", err)
	}

	// disk.
	if err := runCheck(ready(t, Check{Name: "a", Kind: "disk", Dir: dir, MinFree: 0.000001})); err != nil {
		t.Fatalf("disk: %v", err)
	}
	if err := runCheck(ready(t, Check{Name: "a", Kind: "disk", Dir: dir, MinFree: 1.5})); err == nil || !strings.Contains(err.Error(), "磁盘只剩") {
		t.Fatalf("disk, full: %v", err)
	}
}

// sink keeps what the watchdog sends.
type sink struct {
	mu   sync.Mutex
	sent []alert.Alert
}

func (s *sink) Send(_ context.Context, a alert.Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, a)
	return nil
}

func (s *sink) took() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, a := range s.sent {
		out = append(out, string(a.Kind)+":"+a.Title+" "+a.Text)
	}
	s.sent = nil
	return strings.Join(out, " | ")
}

func writeConfig(t *testing.T, c any) string {
	t.Helper()
	b, _ := json.Marshal(c)
	path := filepath.Join(t.TempDir(), "watch.json")
	os.WriteFile(path, b, 0o600)
	return path
}

// TestRounds: an alert after two failures in a row, its end at the next
// success; "still watching" each morning; a start notice.
func TestRounds(t *testing.T) {
	ts, broken := fakeServer(t)
	path := writeConfig(t, Config{Name: "hk-spare", Checks: []Check{
		{Name: "hk-155 的控制服务器", Kind: "https", URL: ts.URL + "/healthz", Pin: proto.Fingerprint(ts.Certificate())},
	}})
	w, err := load(path, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	out := &sink{}
	// 08:00 Beijing.
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return now }
	w.alerts.Sender, w.alerts.Logf = out, t.Logf
	ctx := context.Background()

	w.start()
	w.step(ctx)
	if got := out.took(); got != "info:看门狗开始看了 看着 hk-155 的控制服务器" {
		t.Fatalf("start: %q", got)
	}
	broken.Store(true)
	w.step(ctx)
	if got := out.took(); got != "" {
		t.Fatalf("one failure: %q", got)
	}
	w.step(ctx)
	if got := out.took(); !strings.HasPrefix(got, "firing:hk-155 的控制服务器出问题了 回答 500") || !strings.Contains(got, "连续 2 次") {
		t.Fatalf("two failures: %q", got)
	}
	w.step(ctx)
	if got := out.took(); got != "" {
		t.Fatalf("a third: %q", got)
	}
	// 09:00 Beijing: still watching, and what is wrong.
	now = now.Add(time.Hour)
	w.step(ctx)
	if got := out.took(); got != "info:看门狗还在看 1 项里有问题的：hk-155 的控制服务器" {
		t.Fatalf("the morning: %q", got)
	}
	broken.Store(false)
	now = now.Add(time.Minute)
	w.step(ctx)
	if got := out.took(); got != "resolved:hk-155 的控制服务器好了 " {
		t.Fatalf("fine again: %q", got)
	}
	// Once a day.
	now = now.Add(time.Hour)
	w.step(ctx)
	now = now.Add(23 * time.Hour)
	w.step(ctx)
	if got := out.took(); got != "info:看门狗还在看 1 项都正常" {
		t.Fatalf("the next morning: %q", got)
	}
}

// TestLoad: the file is checked whole before anything runs.
func TestLoad(t *testing.T) {
	https := Check{Name: "a", Kind: "https", URL: "https://203.0.113.10:47443/healthz"}
	for _, tc := range []struct {
		c    Config
		want string
	}{
		{Config{Checks: []Check{https}}, "names no machine"},
		{Config{Name: "x"}, "no checks"},
		{Config{Name: "x", Checks: []Check{{Name: "a", Kind: "ping"}}}, "kind"},
		{Config{Name: "x", Checks: []Check{https, https}}, "two checks"},
		{Config{Name: "x", Every: "1s", Checks: []Check{https}}, "every"},
		{Config{Name: "x", Daily: "9am", Checks: []Check{https}}, "daily"},
		{Config{Name: "x", Checks: []Check{{Name: "a", Kind: "https", URL: "http://203.0.113.10/healthz"}}}, "https://"},
		{Config{Name: "x", Checks: []Check{{Name: "a", Kind: "stun", Addr: "203.0.113.10"}}}, "host:port"},
		{Config{Name: "x", Checks: []Check{{Name: "a", Kind: "derp", Addr: "203.0.113.10:47445", Pin: "nope"}}}, "pin"},
		{Config{Name: "x", Alert: alert.Config{URL: "http://hooks.example.com/x"}, Checks: []Check{https}}, "https"},
	} {
		if _, err := load(writeConfig(t, tc.c), func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: %v, want %q", tc.c, err, tc.want)
		}
	}
	// The bot's key may come from the environment.
	w, err := load(writeConfig(t, Config{Name: "x", Checks: []Check{https}}), func(k string) string {
		return map[string]string{"DENEB_ALERT_URL": "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=KEYVALUE"}[k]
	})
	if err != nil || w.sender == nil || strings.Contains(w.sendsTo(), "KEYVALUE") || !strings.Contains(w.sendsTo(), "wecom") {
		t.Fatalf("from the environment: %v %q", err, w.sendsTo())
	}
}
