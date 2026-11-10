package control

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/control/alert"
	"github.com/kittors/deneb/proto"
)

// alertLog is a Sender that keeps what goes out.
type alertLog struct {
	mu   sync.Mutex
	sent []alert.Alert
}

func (l *alertLog) Send(_ context.Context, a alert.Alert) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sent = append(l.sent, a)
	return nil
}

// took returns what went out since the last call, as "kind:title".
func (l *alertLog) took() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, a := range l.sent {
		out = append(out, string(a.Kind)+":"+a.Title)
	}
	l.sent = nil
	return out
}

// TestHealthAlerts: the server says when a public relay goes offline or
// stays full, when backups fail or go stale, when the disk fills or memory
// runs high, and when devices drop off at once; and when each is right
// again. The admin socket lists them.
func TestHealthAlerts(t *testing.T) {
	sent := &alertLog{}
	clk := &clock{t: time.Now().UTC().Truncate(time.Millisecond)}
	ts := httptest.NewUnstartedServer(nil)
	s, err := New(Config{DataDir: t.TempDir(), PublicURL: "http://" + ts.Listener.Addr().String(), Dev: true, Seed: DevSeed(),
		Now: clk.now, Alerts: sent, Name: "test", Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = s.Handler()
	ts.Start()
	t.Cleanup(func() { ts.Close(); s.Close() })
	sock := serveAdmin(t, s)
	s.cfg.Pin = testPin
	var memory int64 = 50 << 20
	var free uint64 = 50
	s.health.memory = func() int64 { return memory }
	s.health.disk = func(string) (uint64, uint64, error) { return free, 100, nil }
	if _, err := s.backup(); err != nil {
		t.Fatal(err)
	}
	check := func(want ...string) {
		t.Helper()
		s.checkHealth()
		if got := sent.took(); strings.Join(got, " | ") != strings.Join(want, " | ") {
			t.Fatalf("alerts:\n got %q\nwant %q", got, want)
		}
	}

	// A public relay, online once it registers.
	tok, err := AdminRelayAdd(sock, "香港", 0)
	if err != nil {
		t.Fatal(err)
	}
	st, reg := joinRegister(t, s, tok.Code, proto.RelayInfo{})
	if st != 200 {
		t.Fatalf("register: %d", st)
	}
	relay := &testRelay{t: t, url: ts.URL, secret: reg.Secret}
	check("info:控制服务器启动了")
	// Silent for 4 minutes: offline, and said once.
	clk.add(4 * time.Minute)
	s.sweep()
	check("firing:公共中转站「香港」连不上了")
	check()
	// It syncs again, at full CPU: back, and after 10 minutes full.
	busy := &proto.RelayHealth{CPU: 0.95}
	relay.sync(proto.RelaySyncRequest{Health: busy})
	check("resolved:公共中转站「香港」恢复了")
	clk.add(11 * time.Minute)
	relay.sync(proto.RelaySyncRequest{Health: busy})
	check("firing:公共中转站「香港」满了")
	relay.sync(proto.RelaySyncRequest{Health: &proto.RelayHealth{CPU: 0.1}})
	check("resolved:公共中转站「香港」不满了")

	// Backups: a failure, then fine; no new one for 27 hours.
	s.health.mu.Lock()
	s.health.backupErr = errors.New("disk I/O error")
	s.health.mu.Unlock()
	clk.add(31 * time.Minute) // past the relays' cooldown: no leftovers below
	relay.sync(proto.RelaySyncRequest{Health: &proto.RelayHealth{CPU: 0.1}})
	check("firing:每天的备份写不出来")
	s.health.mu.Lock()
	s.health.backupErr = nil
	s.health.mu.Unlock()
	check("resolved:备份又写得出来了")
	clk.add(27 * time.Hour)
	relay.sync(proto.RelaySyncRequest{Health: &proto.RelayHealth{CPU: 0.1}})
	check("firing:超过 26 小时没有新的备份")
	// The copy's time is the file's, from the real clock: put it on the
	// test's.
	name, err := s.backup()
	if err != nil || name == "" {
		t.Fatal(name, err)
	}
	os.Chtimes(filepath.Join(s.cfg.DataDir, BackupDir, name), clk.now(), clk.now())
	check("resolved:又有新的备份了")

	// The disk and the memory, with their hysteresis.
	free, memory = 10, 220<<20
	check("firing:数据目录所在的磁盘快满了", "firing:控制服务器的内存用得太多")
	free, memory = 17, 190<<20
	check()
	free, memory = 25, 100<<20
	check("resolved:磁盘空间够了", "resolved:控制服务器的内存降下来了")

	// Devices dropping off at once.
	var ids []proto.DeviceID
	for range 6 {
		e := newEngine(t, ts.URL, "box", proto.Linux)
		e.login("kittors@deneb.test")
		ids = append(ids, e.id)
	}
	s.hub.mu.Lock()
	for _, id := range ids {
		s.hub.online[id] = true
	}
	s.hub.mu.Unlock()
	clk.add(time.Minute)
	relay.sync(proto.RelaySyncRequest{Health: &proto.RelayHealth{CPU: 0.1}})
	check()
	s.hub.mu.Lock()
	for _, id := range ids[:4] {
		s.hub.online[id] = false
	}
	s.hub.mu.Unlock()
	clk.add(time.Minute)
	relay.sync(proto.RelaySyncRequest{Health: &proto.RelayHealth{CPU: 0.1}})
	check("info:在线设备一下子少了很多")

	// The admin socket lists them. (Past the cooldown, or the disk's
	// second firing would be held back.)
	clk.add(alert.DefaultCooldown)
	relay.sync(proto.RelaySyncRequest{Health: &proto.RelayHealth{CPU: 0.1}})
	os.Chtimes(filepath.Join(s.cfg.DataDir, BackupDir, name), clk.now(), clk.now())
	free = 5
	check("firing:数据目录所在的磁盘快满了")
	a, err := AdminListAlerts(sock)
	if err != nil || len(a.Active) != 1 || a.Active[0].Key != "disk-low" || len(a.Recent) < 15 || a.Recent[0].Title != "控制服务器启动了" {
		t.Fatalf("the admin socket: %+v, %v", a, err)
	}
	if a.Recent[0].Source != "deneb-control（test）" {
		t.Fatalf("the source: %q", a.Recent[0].Source)
	}
}
