//go:build linux || darwin

package control

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	mrand "math/rand/v2"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
)

// TestLoad is the load test before the public beta (design/plan/m7/
// control-ops.md §4): a world of DENEB_LOAD devices, three a person, in
// small LANs of three people and one LAN of 100 people (300 devices),
// built straight into a database; the real deneb-control on it, in its own
// process, so its memory is its own, as in production: HTTPS with HTTP/2,
// one CPU (GOMAXPROCS=1, as --cpus 1) and the heap kept within 204 MB (as
// it keeps itself in a 256 MB container); every device signs in and holds
// its stream on its own connection, a third of them with the owner's view,
// as the desktop app does; then changes, timed until every stream that
// should hear of them has. DENEB_LOAD_TLS=0 is plain HTTP;
// DENEB_LOAD_PROCS and DENEB_LOAD_MEMLIMIT (or "off") change the others.
//
//	DENEB_LOAD=3000 go test ./control -run TestLoad -v -timeout 60m
func TestLoad(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("DENEB_LOAD"))
	n -= n % 3 // three a person
	if n == 0 {
		t.Skip("DENEB_LOAD not set")
	}
	raiseFileLimit(t, 4*n+1024)
	dir := t.TempDir()
	devs := buildWorld(t, dir, n)

	bin := filepath.Join(t.TempDir(), "deneb-control")
	if out, err := exec.Command("go", "build", "-o", bin, "../cmd/deneb-control").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	useTLS := os.Getenv("DENEB_LOAD_TLS") != "0"
	url := "http://" + addr
	tlsFlag := "off"
	if useTLS {
		url, tlsFlag = "https://"+addr, "on"
	}
	var serverLog lockedBuffer
	cmd := exec.Command(bin, "-dev", "-data", dir, "-listen", addr, "-public-url", url, "-mail", "none", "-tls", tlsFlag)
	cmd.Stdout, cmd.Stderr = &serverLog, &serverLog
	procs, memlimit := cmp.Or(os.Getenv("DENEB_LOAD_PROCS"), "1"), cmp.Or(os.Getenv("DENEB_LOAD_MEMLIMIT"), "204MiB")
	cmd.Env = append(os.Environ(), "GOMAXPROCS="+procs)
	if memlimit != "off" {
		cmd.Env = append(cmd.Env, "GOMEMLIMIT="+memlimit)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	var tlsConf *tls.Config
	for i := 0; ; i++ {
		if useTLS && tlsConf == nil {
			if pin, err := os.ReadFile(filepath.Join(dir, "tls", "pin.txt")); err == nil {
				tlsConf, _ = proto.PinnedTLSConfig(strings.TrimSpace(string(pin)))
			}
		}
		if !useTLS || tlsConf != nil {
			if resp, err := newLoadClient(tlsConf).Get(url + "/healthz"); err == nil {
				resp.Body.Close()
				break
			}
		}
		if i > 200 {
			t.Fatalf("the server does not come up:\n%s", serverLog.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	loadTLS = tlsConf
	rss := func() int64 {
		out, _ := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(cmd.Process.Pid)).Output()
		kb, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
		return kb << 10
	}
	client := newLoadClient(tlsConf)
	report := func(f string, a ...any) { t.Logf(f, a...) }
	report("world: %d people, %d devices; server up (%s, GOMAXPROCS=%s, GOMEMLIMIT=%s), %s",
		n/3, n, map[bool]string{true: "HTTPS, HTTP/2", false: "HTTP"}[useTLS], procs, memlimit, sizeOf(rss()))

	// Every device signs in for a token.
	start := time.Now()
	parallel(n, 64, func(i int) {
		if err := devs[i].auth(client, url); err != nil {
			t.Errorf("auth %d: %v", i, err)
		}
	})
	report("tokens: %d in %v; server %s", n, time.Since(start).Round(time.Millisecond), sizeOf(rss()))

	// Every device opens its stream, all at once: what a restart of the
	// server sees, every engine coming back within seconds.
	start = time.Now()
	var opened atomic.Int32
	for i := range devs {
		go devs[i].stream(url, i%3 == 0, &opened)
	}
	stormLimit := time.Duration(n) * 200 * time.Millisecond
	if !within(stormLimit, func() bool { return int(opened.Load()) == n }) {
		t.Fatalf("after %v, %d of %d streams had a frame; the server's log ends:\n%s", stormLimit, opened.Load(), n, tail(serverLog.String(), 30))
	}
	firstFrames := time.Since(start)
	// The storm is over when the version stops moving: each device coming
	// online is a new version for everyone.
	settled := waitSettled(t, devs, 30*time.Minute)
	report("streams: %d open, first frames within %v, all caught up after %v; server %s",
		n, firstFrames.Round(time.Millisecond), time.Since(start).Round(time.Millisecond), sizeOf(rss()))
	_ = settled

	// Changes, timed until everyone who should hear of them has.
	small, big := devs[3], devs[0] // a small LAN's owner, the big LAN's owner
	// Who hears of a change: a LAN's rename, every device in it; a
	// service opened, the device itself and the views of the LAN's members
	// (peers enforce nothing for it: the device does).
	inLAN := func(l proto.LANID) func(i int, d *loadDevice) bool {
		return func(i int, d *loadDevice) bool { return d.in[l] }
	}
	views := func(l proto.LANID, self *loadDevice) func(i int, d *loadDevice) bool {
		return func(i int, d *loadDevice) bool { return d == self || d.in[l] && i%3 == 0 }
	}
	cases := []struct {
		what string
		d    *loadDevice
		op   proto.Op
		who  func(i int, d *loadDevice) bool
	}{
		{"rename a LAN of 9 devices", small, proto.Op{Op: proto.OpLANRename, Args: proto.OpArgs{LAN: small.ownLAN, Name: "改名了"}}, inLAN(small.ownLAN)},
		{"rename the LAN of 300 devices", big, proto.Op{Op: proto.OpLANRename, Args: proto.OpArgs{LAN: big.bigLAN, Name: "公司改名了"}}, inLAN(big.bigLAN)},
		{"open a service in the LAN of 300", big, proto.Op{Op: proto.OpServiceSet, Args: proto.OpArgs{Device: big.id, LAN: big.bigLAN, On: true,
			Service: &proto.Service{Kind: proto.ServiceWeb, Ports: []proto.PortRange{{Proto: "tcp", First: 8080, Last: 8080}}}}}, views(big.bigLAN, big)},
	}
	if big.bigLAN == "" {
		cases = cases[:1]
	}
	for _, tc := range cases {
		for round := range 3 {
			tc.op.ID = fmt.Sprintf("load-%s-%d", tc.what, round)
			if tc.op.Op == proto.OpLANRename {
				tc.op.Args.Name = fmt.Sprintf("%s %d", tc.op.Args.Name, round)
			}
			if tc.op.Op == proto.OpServiceSet {
				tc.op.Args.On = round%2 == 0
			}
			t0 := time.Now()
			v, err := tc.d.op(client, url, tc.op)
			took := time.Since(t0)
			if err != nil {
				t.Fatalf("%s: %v", tc.what, err)
			}
			var reached time.Duration
			waitFor(t, 5*time.Minute, func() bool {
				for i, d := range devs {
					if tc.who(i, d) && d.version.Load() < v {
						return false
					}
				}
				reached = time.Since(t0)
				return true
			})
			report("%s: the op answered in %v, everyone who hears of it had version %d after %v",
				tc.what, took.Round(time.Millisecond), v, reached.Round(time.Millisecond))
		}
	}
	report("server at the end: %s; Go heap of this test process: %s", sizeOf(rss()), heapOf())
	for _, d := range devs {
		d.cancel()
	}
}

// loadTLS is the server's pinned TLS, for the streams; nil over HTTP.
var loadTLS *tls.Config

// newLoadClient is a client with a connection of its own, over HTTP/2 when
// the server speaks TLS, as an engine's.
func newLoadClient(conf *tls.Config) *http.Client {
	tr := &http.Transport{MaxIdleConnsPerHost: 256}
	if conf != nil {
		tr.TLSClientConfig, tr.ForceAttemptHTTP2 = conf.Clone(), true
	}
	return &http.Client{Transport: tr}
}

// lockedBuffer is the server's log, written by two goroutines and read by
// the test.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func heapOf() string {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return sizeOf(int64(m.HeapInuse))
}

// loadDevice is one fake engine.
type loadDevice struct {
	id      proto.DeviceID
	mk      proto.MachinePrivate
	token   string
	ownLAN  proto.LANID
	bigLAN  proto.LANID
	in      map[proto.LANID]bool
	version atomic.Uint64 // the newest version its stream brought
	frames  atomic.Int64
	cancel  context.CancelFunc
}

func (d *loadDevice) post(client *http.Client, url, path string, in, out any) error {
	b, _ := json.Marshal(in)
	req, _ := http.NewRequest("POST", url+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if d.token != "" {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %s %s", path, resp.Status, body)
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

func (d *loadDevice) auth(client *http.Client, url string) error {
	var ch proto.AuthChallenge
	if err := d.post(client, url, proto.PathAuthChallenge, proto.AuthChallengeRequest{MachineKey: d.mk.Public()}, &ch); err != nil {
		return err
	}
	var tok proto.AuthToken
	if err := d.post(client, url, proto.PathAuthToken, proto.AuthTokenRequest{MachineKey: d.mk.Public(), Challenge: ch.Challenge, Signature: d.mk.SignChallenge(ch.Challenge)}, &tok); err != nil {
		return err
	}
	d.token = tok.Token
	return nil
}

func (d *loadDevice) op(client *http.Client, url string, op proto.Op) (uint64, error) {
	var res proto.OpResult
	err := d.post(client, url, proto.PathOps, op, &res)
	return res.Version, err
}

// stream holds the device's stream on its own connection, as an engine
// does, and keeps the newest version it brings.
func (d *loadDevice) stream(url string, view bool, opened *atomic.Int32) {
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	q := ""
	if view {
		q = "?userView=1"
	}
	// Like an engine, it tries again after a moment if the connection is
	// refused: a burst of connections overflows the listen queue (128 on
	// macOS), and the engines come back spread over a few seconds anyway.
	var resp *http.Response
	for try := 0; ; try++ {
		req, _ := http.NewRequestWithContext(ctx, "GET", url+proto.PathStream+q, nil)
		req.Header.Set("Authorization", "Bearer "+d.token)
		var err error
		resp, err = newLoadClient(loadTLS).Do(req)
		if err == nil && resp.StatusCode == 200 {
			break
		}
		if err == nil {
			resp.Body.Close()
		}
		if ctx.Err() != nil || try == 50 {
			fmt.Fprintf(os.Stderr, "stream %s: %v\n", d.id, err)
			return
		}
		time.Sleep(time.Duration(100+mrand.IntN(900)) * time.Millisecond)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(nil, 16<<20)
	first := true
	for sc.Scan() {
		var f struct {
			Version uint64 `json:"version"`
		}
		json.Unmarshal(sc.Bytes(), &f)
		if f.Version > d.version.Load() {
			d.version.Store(f.Version)
		}
		d.frames.Add(1)
		if first {
			first = false
			opened.Add(1)
		}
	}
}

// buildWorld writes n devices' world straight into a new database in dir.
func buildWorld(t testing.TB, dir string, n int) []*loadDevice {
	t.Helper()
	s, err := New(Config{DataDir: dir, PublicURL: "http://x", Dev: true, Seed: DevSeed(), Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatal(err)
	}
	block := s.Network()
	people := n / 3
	now := ms(time.Now())
	devs := make([]*loadDevice, n)
	_, err = s.change("", func(tx *sql.Tx, _ *World) error {
		ins := func(q string, args ...any) {
			if err == nil {
				_, err = tx.Exec(q, args...)
			}
		}
		pid := func(i int) string { return fmt.Sprintf("p_load%06d", i) }
		for i := range people {
			ins(`INSERT INTO people (id, email, name, display_name, initial, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
				pid(i), fmt.Sprintf("user%d@load.test", i), fmt.Sprintf("user%d", i), fmt.Sprintf("用户%d", i), "用", now)
		}
		for i := range n {
			d := &loadDevice{id: proto.DeviceID(fmt.Sprintf("d_load%06d", i)), mk: proto.NewMachineKey(), in: map[proto.LANID]bool{}}
			devs[i] = d
			var nk proto.NodePublic
			var dk proto.DiscoPublic
			rand.Read(nk[:])
			rand.Read(dk[:])
			v4 := hostAddr(block.IPv4, byte(1+i/250), byte(1+i%250))
			ins(`INSERT INTO devices (id, owner, machine_key, node_key, disco_key, name, display_name, kind, os, hostname, ipv4, ipv6,
					endpoints, engine_version, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'server', 'linux', ?, ?, ?, ?, 'load', ?, ?)`,
				d.id, pid(i/3), d.mk.Public().String(), nk.String(), dk.String(), fmt.Sprintf("dev%d", i), fmt.Sprintf("设备%d", i),
				fmt.Sprintf("dev%d", i), v4.String(), v6For(block.IPv6, v4).String(),
				fmt.Sprintf(`["203.0.113.%d:41641","198.51.100.%d:41641"]`, 1+i%250, 1+i%250), now, ms(time.Now().Add(180*24*time.Hour)))
		}
		// Person i owns LAN i, with persons i, i+1, i+2 in it and their
		// devices: each device sees about 14 others.
		for i := range people {
			lan := fmt.Sprintf("l_load%06d", i)
			ins(`INSERT INTO lans (id, name, owner, created_at) VALUES (?, ?, ?, ?)`, lan, fmt.Sprintf("家%d", i), pid(i), now)
			for k := range 3 {
				p := (i + k) % people
				role := proto.RoleMember
				if k == 0 {
					role = proto.RoleOwner
				}
				ins(`INSERT INTO members (lan, person, role, state, joined_at) VALUES (?, ?, ?, 'joined', ?)`, lan, pid(p), role, now)
				for j := range 3 {
					dev := devs[3*p+j]
					ins(`INSERT INTO lan_devices (lan, device, joined_at) VALUES (?, ?, ?)`, lan, dev.id, now)
					dev.in[proto.LANID(lan)] = true
				}
			}
			devs[3*i].ownLAN = proto.LANID(lan)
		}
		// The company: person 0's, 100 people and their 300 devices
		// (DENEB_LOAD_BIG people; 0 for none).
		big := 100
		if v, err := strconv.Atoi(os.Getenv("DENEB_LOAD_BIG")); err == nil {
			big = v
		}
		if big == 0 {
			return err
		}
		ins(`INSERT INTO lans (id, name, owner, created_at) VALUES ('l_loadbig', '公司', ?, ?)`, pid(0), now)
		for p := range min(big, people) {
			role := proto.RoleMember
			if p == 0 {
				role = proto.RoleOwner
			}
			ins(`INSERT INTO members (lan, person, role, state, joined_at) VALUES ('l_loadbig', ?, ?, 'joined', ?)`, pid(p), role, now)
			for j := range 3 {
				ins(`INSERT INTO lan_devices (lan, device, joined_at) VALUES ('l_loadbig', ?, ?)`, devs[3*p+j].id, now)
				devs[3*p+j].in["l_loadbig"] = true
			}
		}
		devs[0].bigLAN = "l_loadbig"
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	return devs
}

func parallel(n, workers int, fn func(i int)) {
	var wg sync.WaitGroup
	next := make(chan int)
	for range workers {
		wg.Go(func() {
			for i := range next {
				fn(i)
			}
		})
	}
	for i := range n {
		next <- i
	}
	close(next)
	wg.Wait()
}

func waitFor(t *testing.T, limit time.Duration, ok func() bool) {
	t.Helper()
	if !within(limit, ok) {
		t.Fatal("timed out")
	}
}

func within(limit time.Duration, ok func() bool) bool {
	deadline := time.Now().Add(limit)
	for !ok() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

// waitSettled waits until no stream has had a frame for 2 s (a device
// hears only of versions that change what it sees), and returns the
// highest version.
func waitSettled(t *testing.T, devs []*loadDevice, limit time.Duration) uint64 {
	t.Helper()
	deadline := time.Now().Add(limit)
	var last int64
	stable := time.Now()
	for {
		var frames int64
		var top uint64
		for _, d := range devs {
			frames += d.frames.Load()
			top = max(top, d.version.Load())
		}
		if frames != last {
			last, stable = frames, time.Now()
		} else if time.Since(stable) > 2*time.Second {
			return top
		}
		if time.Now().After(deadline) {
			t.Fatalf("still moving: version %d", top)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// raiseFileLimit lets this process, and the server it starts, hold n
// files open.
func raiseFileLimit(t *testing.T, n int) {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatal(err)
	}
	if lim.Cur >= uint64(n) {
		return
	}
	lim.Cur = min(uint64(n), lim.Max)
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatalf("raise the file limit to %d: %v", n, err)
	}
	if lim.Cur < uint64(n) {
		t.Fatalf("the file limit is %d, %d needed", lim.Cur, n)
	}
}

// BenchmarkChange is one change with DENEB_LOAD devices online, a third
// with their owner's view, in this process: the world reloaded and every
// stream's frame worked out. For a profile:
//
//	DENEB_LOAD=300 go test ./control -run '^$' -bench Change -cpuprofile cpu.out
func BenchmarkChange(b *testing.B) {
	n, _ := strconv.Atoi(os.Getenv("DENEB_LOAD"))
	n -= n % 3
	if n == 0 {
		b.Skip("DENEB_LOAD not set")
	}
	dir := b.TempDir()
	devs := buildWorld(b, dir, n)
	s, err := New(Config{DataDir: dir, PublicURL: "http://x", Dev: true, Seed: DevSeed(), Logf: func(string, ...any) {}})
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	for i, d := range devs {
		sb := &sub{device: d.id, view: i%3 == 0, notify: make(chan struct{}, 1)}
		s.hub.add(sb)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := s.change("", func(*sql.Tx, *World) error { return nil }); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	// What stays: the world and the streams' state, without connections.
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	b.ReportMetric(float64(m.HeapAlloc)/(1<<20), "MB-live")
}
