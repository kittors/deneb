package main

// The M2 checks (plan §3 M2, engine): heartbeat only with traffic, a lossy
// line, pause and resume, peers on different relays, a network change and
// sleep.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// msPath is the engine's own view of a path (deneb debug paths): beyond
// State's, whether the peer is idle, why it is on the relay, pings sent.
type msPath struct {
	Kind       string `json:"kind"`
	Relay      string `json:"relay"`
	Reason     string `json:"reason"`
	Idle       bool   `json:"idle"`
	Held       bool   `json:"held"`
	PingsTx    uint64 `json:"pingsTx"`
	PingsRelay uint64 `json:"pingsRelay"`
	PingsRx    uint64 `json:"pingsRx"`
	PongsRx    uint64 `json:"pongsRx"`
	HSBoth     bool   `json:"hsBoth"`
}

func fqdnOf(name string) string {
	for _, n := range nodes {
		if n.name == name {
			return n.name + "." + n.owner + ".deneb.internal"
		}
	}
	return name
}

func debugPath(from, to string) (msPath, error) {
	var p msPath
	out, err := deneb(from, "debug", "paths")
	if err != nil {
		return p, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &all); err != nil {
		return p, err
	}
	raw, ok := all[fqdnOf(to)]
	if !ok {
		return p, fmt.Errorf("%s has no path to %s", from, to)
	}
	return p, json.Unmarshal(raw, &p)
}

var maxRTT = regexp.MustCompile(`最长 ([0-9.]+)ms`)

// checkIdle: an idle peer gets no pings and keeps the path it showed; the
// first packet after idle is answered within 0.2 s.
func checkIdle() error {
	if err := pingOK("mac", "nas", 5*time.Second); err != nil {
		return err
	}
	if err := waitPath("mac", "nas", apitype.PathDirect, 10*time.Second); err != nil {
		return err
	}
	time.Sleep(12 * time.Second) // -idle 6s, and past the trust of the last pong
	p1, err := debugPath("mac", "nas")
	if err != nil {
		return err
	}
	if !p1.Idle || p1.Kind != "direct" {
		return fmt.Errorf("after 12 s without traffic: %+v", p1)
	}
	time.Sleep(3 * time.Second)
	p2, err := debugPath("mac", "nas")
	if err != nil {
		return err
	}
	if p2.PingsTx != p1.PingsTx {
		return fmt.Errorf("an idle peer got %d pings in 3 s (%d through the relay); before %+v, after %+v",
			p2.PingsTx-p1.PingsTx, p2.PingsRelay-p1.PingsRelay, p1, p2)
	}
	out, err := deneb("mac", "ping", "nas", "-c", "1")
	if err != nil {
		return fmt.Errorf("%v %s", err, out)
	}
	m := maxRTT.FindStringSubmatch(out)
	if m == nil {
		return fmt.Errorf("no answer after idle: %s", out)
	}
	ms, _ := strconv.ParseFloat(m[1], 64)
	fmt.Printf("     idle: no pings in 3 s while idle; first answer after idle %.1f ms\n", ms)
	if ms > 200 {
		return fmt.Errorf("first answer after idle took %.1f ms, want within 200", ms)
	}
	return waitPath("mac", "nas", apitype.PathDirect, 5*time.Second)
}

// checkLossy: 20% of UDP lost between the Mac and build. The engine moves
// to the relay within 10 s and back once the line is clean; a long
// connection over it never breaks.
func checkLossy() error {
	if err := waitPath("mac", "build", apitype.PathDirect, 10*time.Second); err != nil {
		return err
	}
	for _, n := range []string{"mac", "build"} { // every packet of the long connection into the logs
		deneb(n, "debug", "trace", "7000")
		defer deneb(n, "debug", "trace", "off")
	}
	long := tickAsync("mac", "build", 32)
	time.Sleep(2 * time.Second)
	t0 := time.Now()
	if _, err := deneb("mac", "debug", "loss", "build", "20"); err != nil {
		return err
	}
	if _, err := waitFor(10*time.Second, func() (string, error) {
		p, err := debugPath("mac", "build")
		if err == nil && (p.Kind != "relay" || p.Reason != "lossy") {
			err = fmt.Errorf("still %s (%s)", p.Kind, p.Reason)
		}
		return "", err
	}); err != nil {
		deneb("mac", "debug", "loss", "build", "0")
		return fmt.Errorf("10 s at 20%% loss: %w", err)
	}
	toRelay := time.Since(t0)
	time.Sleep(8 * time.Second)
	t1 := time.Now()
	if _, err := deneb("mac", "debug", "loss", "build", "0"); err != nil {
		return err
	}
	if err := waitPath("mac", "build", apitype.PathDirect, 15*time.Second); err != nil {
		return err
	}
	back := time.Since(t1)
	r, err := long()
	if err != nil {
		return err
	}
	fmt.Printf("     lossy-line: relay after %s, direct again %s after the line was clean; long connection %d lines, longest stall %d ms\n",
		toRelay.Round(100*time.Millisecond), back.Round(100*time.Millisecond), r.Lines, r.MaxGapMs)
	if r.Broken || r.Lines < 200 {
		return fmt.Errorf("long connection: %+v", r)
	}
	return nil
}

var clockRE = regexp.MustCompile(`(\d\d:\d\d:\d\d\.\d{3})`)

// clockOf reads the first hh:mm:ss.mmm in a line (the containers' clock).
func clockOf(s string) (time.Time, bool) {
	m := clockRE.FindString(s)
	if m == "" {
		return time.Time{}, false
	}
	t, err := time.Parse("15:04:05.000", m)
	return t, err == nil
}

// checkPauseResume: pausing 周末联机 for 10 s leaves a long connection in
// 工作室 alone; how soon the Mac reaches nas again after the resume is
// measured on the Mac's own clock (M2 wants 0.5 s).
func checkPauseResume() error {
	long := tickAsync("mac", "build", 16)
	time.Sleep(2 * time.Second)
	if _, err := deneb("mac", "lan", "pause", "周末联机"); err != nil {
		return err
	}
	time.Sleep(10 * time.Second)
	probe := make(chan string, 1)
	go func() {
		out, _ := deneb("mac", "debug", "probe", "nas", "ping", "0", "100ms", "6")
		probe <- out
	}()
	time.Sleep(time.Second)
	since := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	if _, err := deneb("mac", "lan", "resume", "周末联机"); err != nil {
		return err
	}
	out := <-probe
	r, err := long()
	if err != nil {
		return err
	}
	logs, _ := logsOf("dnb-mac", since)
	var resumed time.Time
	for _, l := range strings.Split(logs, "\n") {
		if strings.Contains(l, "paused=false") {
			resumed, _ = clockOf(l)
		}
	}
	var first time.Time
	for _, l := range strings.Split(out, "\n") {
		if t, ok := clockOf(l); ok && strings.Contains(l, " ok ") && !resumed.IsZero() && t.After(resumed) {
			first = t
			break
		}
	}
	if resumed.IsZero() || first.IsZero() {
		return fmt.Errorf("could not time the resume (resumed %v, first answer %v):\n%s", resumed, first, out)
	}
	d := first.Sub(resumed)
	fmt.Printf("     pause-resume: 工作室's long connection longest stall %d ms; nas answered %s after the resume\n", r.MaxGapMs, d.Round(time.Millisecond))
	if r.Broken || r.MaxGapMs > 500 {
		return fmt.Errorf("工作室's long connection during the pause: %+v", r)
	}
	if d > 500*time.Millisecond {
		return fmt.Errorf("nas answered %s after the resume, want within 0.5 s", d.Round(time.Millisecond))
	}
	return nil
}

// checkCrossRelay: nas lives on the second relay (-home-relay), the Mac on
// the first. With UDP blocked between them, each reaches the other through
// the other's home relay, which it opens on demand.
func checkCrossRelay() error {
	deneb("mac", "debug", "block", "nas")
	deneb("nas", "debug", "block", "mac")
	defer deneb("mac", "debug", "unblock", "nas")
	defer deneb("nas", "debug", "unblock", "mac")
	if err := pingOK("mac", "nas", 10*time.Second); err != nil {
		return err
	}
	if err := waitPath("mac", "nas", apitype.PathRelay, 10*time.Second); err != nil {
		return err
	}
	if err := waitPath("nas", "mac", apitype.PathRelay, 10*time.Second); err != nil {
		return err
	}
	a, err := pathKind("mac", "nas")
	if err != nil {
		return err
	}
	b, err := pathKind("nas", "mac")
	if err != nil {
		return err
	}
	s, err := state("mac")
	if err != nil {
		return err
	}
	var open []string
	for _, r := range s.Relays {
		if r.Connected {
			open = append(open, string(r.ID))
		}
	}
	fmt.Printf("     cross-relay: Mac -> nas via %s, nas -> Mac via %s; the Mac holds %v\n", a.Relay, b.Relay, open)
	if a.Relay != "second" || b.Relay != "local" {
		return errors.New("each should reach the other through the other's home relay")
	}
	return pingOK("nas", "mac", 5*time.Second)
}

// moveMac puts the Mac on another home network: plug into the new one,
// route through its router, unplug the old one.
func moveMac(toNet, ip, gw, fromNet string) error {
	if _, err := docker("network", "connect", "--ip", ip, toNet, "dnb-mac"); err != nil {
		return err
	}
	if _, err := docker("exec", "dnb-mac", "ip", "route", "replace", "default", "via", gw); err != nil {
		return err
	}
	_, err := docker("network", "disconnect", fromNet, "dnb-mac")
	return err
}

// reachAll waits until the Mac reaches all three peers again.
func reachAll(within time.Duration) error {
	for _, p := range []string{"build", "nas", "desktop"} {
		if err := pingOK("mac", p, within); err != nil {
			return err
		}
	}
	return nil
}

// checkNetChange moves the Mac to the other home network with a long
// connection open: the engine notices, moves its socket to the new
// interface and reconnects. M2: everything back within 30 s.
func checkNetChange() error {
	long := tickAsync("mac", "build", 40)
	time.Sleep(3 * time.Second)
	t0 := time.Now()
	if err := moveMac("dnb-homeb", "192.168.120.11", "192.168.120.2", "dnb-homea"); err != nil {
		return err
	}
	if err := reachAll(30 * time.Second); err != nil {
		return fmt.Errorf("after the move: %w", err)
	}
	back := time.Since(t0)
	r, err := long()
	if err != nil {
		return err
	}
	logs, _ := logsOf("dnb-mac", t0.UTC().Format(time.RFC3339))
	rebound := strings.Contains(logs, "now on")
	if err := moveMac("dnb-homea", "192.168.110.10", "192.168.110.2", "dnb-homeb"); err != nil {
		return fmt.Errorf("moving back: %w", err)
	}
	if err := reachAll(30 * time.Second); err != nil {
		return fmt.Errorf("after moving back: %w", err)
	}
	// The control server must be reachable again too, not only the peers
	// (11-08: every request timed out on the dead connection for minutes).
	t1 := time.Now()
	if _, err := waitFor(30*time.Second, func() (string, error) {
		out, err := deneb("mac", "lan", "add", "周末联机", "构建服务器")
		if err != nil {
			return out, err
		}
		_, err = deneb("mac", "lan", "remove", "周末联机", "构建服务器")
		return "", err
	}); err != nil {
		return fmt.Errorf("the control server after the moves: %w", err)
	}
	ctl := time.Since(t1)
	fmt.Printf("     net-change: all peers back %s after the move (socket moved: %v); control ops work %s after moving back; long connection %d lines, longest stall %d ms\n",
		back.Round(100*time.Millisecond), rebound, ctl.Round(100*time.Millisecond), r.Lines, r.MaxGapMs)
	if back > 30*time.Second || r.Broken {
		return fmt.Errorf("long connection: %+v", r)
	}
	return nil
}

// checkSleep freezes the Mac's container for 20 s, as a laptop lid would:
// the engine sees the clock jump and reconnects. M2: back within 30 s.
func checkSleep() error {
	long := tickAsync("mac", "build", 45)
	time.Sleep(3 * time.Second)
	if _, err := docker("pause", "dnb-mac"); err != nil {
		return err
	}
	time.Sleep(20 * time.Second)
	t0 := time.Now()
	if _, err := docker("unpause", "dnb-mac"); err != nil {
		return err
	}
	if err := reachAll(30 * time.Second); err != nil {
		return fmt.Errorf("after waking: %w", err)
	}
	back := time.Since(t0)
	r, err := long()
	if err != nil {
		return err
	}
	logs, _ := logsOf("dnb-mac", t0.Add(-25*time.Second).UTC().Format(time.RFC3339))
	noticed := strings.Contains(logs, "slept") || strings.Contains(logs, "woke")
	// The long connection is reported, not judged: its sender backed off
	// while the Mac slept and retries on its own schedule (TCP's), which no
	// engine on this side can hurry. M2 asks for the lines: all peers back.
	fmt.Printf("     sleep: all peers back %s after waking (noticed: %v); long connection %d lines, longest stall %d ms, broken %v\n",
		back.Round(100*time.Millisecond), noticed, r.Lines, r.MaxGapMs, r.Broken)
	if back > 30*time.Second || !noticed {
		return errors.New("the engine did not notice the sleep, or took too long")
	}
	return nil
}

// longResult is what test/topo/longconn prints.
type longResult struct {
	Host     string  `json:"host"`
	Requests int     `json:"requests"`
	MaxMs    int64   `json:"maxMs"`
	WakesMs  []int64 `json:"wakesMs"`
	Pauses   int     `json:"pauses"`
	Broken   bool    `json:"broken"`
	Err      string  `json:"err"`
	Slow     []struct {
		At time.Time `json:"at"`
		Ms int64     `json:"ms"`
	} `json:"slow"`
}

// checkLongIdle holds one TCP connection per pair for more than three
// minutes, with silences longer than the engines' idle time in between:
// the heartbeat stops, then the next request wakes the path. No connection
// may break, and no answer may take over 0.5 s, the first one after each
// silence included. Run with -m1, the same connections cross M1 and M2
// engines (an M1 engine never goes idle; the M2 one opposite it does).
func checkLongIdle() error {
	for _, c := range [][]string{ // the test page answers the requests
		{"build", "service", "工作室", "web", "tcp/80"},
		{"desktop", "service", "周末联机", "web", "tcp/80"},
		{"nas", "service", "周末联机", "web", "tcp/80"},
	} {
		if _, err := deneb(c[0], append(c[1:], "on")...); err == nil { // else it was open already
			defer deneb(c[0], append(c[1:], "off")...)
		}
	}
	time.Sleep(time.Second)
	owner := map[string]string{}
	for _, n := range nodes {
		owner[n.name] = n.owner
	}
	label := func(n string) string { return n + "." + owner[n] }
	pairs := [][2]string{{"mac", "build"}, {"mac", "nas"}, {"nas", "desktop"}}
	since := time.Now()
	results := make([]longResult, len(pairs))
	errs := make([]error, len(pairs))
	var wg sync.WaitGroup
	for i, pr := range pairs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := docker("exec", "dnb-"+pr[0], "longconn", "-active", "20s", "-pause", "12s", "-total", "200s", pr[1])
			lines := strings.Split(strings.TrimSpace(out), "\n")
			if jerr := json.Unmarshal([]byte(lines[len(lines)-1]), &results[i]); jerr != nil {
				errs[i] = fmt.Errorf("%v: %s", err, out)
			}
		}()
	}
	wg.Wait()
	var bad []string
	for i, pr := range pairs {
		r := results[i]
		if errs[i] != nil {
			bad = append(bad, fmt.Sprintf("%s→%s: %v", pr[0], pr[1], errs[i]))
			continue
		}
		// Which side shows the idle: an M2 engine, on the other one's line.
		idles, wakes := -1, -1
		for _, side := range [][2]string{{pr[0], pr[1]}, {pr[1], pr[0]}} {
			if isM1(side[0]) {
				continue
			}
			logs, _ := logsOf("dnb-"+side[0], since.Format(time.RFC3339))
			idles = strings.Count(logs, "[idle] "+label(side[1])+": no traffic")
			wakes = strings.Count(logs, "[idle] "+label(side[1])+": traffic again")
			break
		}
		fmt.Printf("     %s→%s: %d requests, slowest %d ms, after the %d silences %v ms; idle %d times, woken %d times\n",
			pr[0], pr[1], r.Requests, r.MaxMs, r.Pauses, r.WakesMs, idles, wakes)
		switch {
		case r.Broken:
			bad = append(bad, fmt.Sprintf("%s→%s broke: %s", pr[0], pr[1], r.Err))
		case r.MaxMs > 500:
			bad = append(bad, fmt.Sprintf("%s→%s: an answer took %d ms (slow ones: %+v)", pr[0], pr[1], r.MaxMs, r.Slow))
		case r.Pauses < 5:
			bad = append(bad, fmt.Sprintf("%s→%s: only %d silences", pr[0], pr[1], r.Pauses))
		case idles >= 0 && (idles < r.Pauses || wakes < r.Pauses):
			bad = append(bad, fmt.Sprintf("%s→%s: idle %d times and woken %d times in %d silences", pr[0], pr[1], idles, wakes, r.Pauses))
		}
	}
	if len(bad) > 0 {
		return errors.New(strings.Join(bad, "; "))
	}
	return nil
}

// checkPauseShared: build in both LANs, as on the real machines. The Mac
// pauses 周末联机 and resumes it three times; the long connection to build
// through 工作室 must not stall at all (lines every 100 ms, nothing over
// 300 ms), and nas is back each time.
func checkPauseShared() error {
	if _, err := deneb("mac", "lan", "add", "周末联机", "构建服务器"); err != nil {
		return err
	}
	defer deneb("mac", "lan", "remove", "周末联机", "构建服务器")
	if err := pingOK("nas", "build", 6*time.Second); err != nil {
		return err
	}
	long := tickAsync("mac", "build", 16)
	time.Sleep(2 * time.Second)
	for i := 0; i < 3; i++ {
		if _, err := deneb("mac", "lan", "pause", "周末联机"); err != nil {
			return err
		}
		time.Sleep(time.Second)
		if _, err := deneb("mac", "lan", "resume", "周末联机"); err != nil {
			return err
		}
		if err := pingOK("mac", "nas", 3*time.Second); err != nil {
			return err
		}
		time.Sleep(time.Second)
	}
	r, err := long()
	if err != nil {
		return err
	}
	fmt.Printf("     pause-shared: 工作室's connection to build (in both LANs) through three pauses of 周末联机: %d lines, longest stall %d ms\n", r.Lines, r.MaxGapMs)
	if r.Broken || r.MaxGapMs > 300 {
		return fmt.Errorf("工作室's connection to build: %+v", r)
	}
	return nil
}

// checkLossyBoth: the direct path loses 20% of its UDP and the relay's
// answers come late a quarter of the time, as on the home line to Hong
// Kong, where TCP is lost too. The relay is no cleaner: the path stays
// direct (moving would only add the detour).
func checkLossyBoth() error {
	// Both ends back on direct first: lossy-line, just before, may have
	// left one on the relay for its 20 s.
	for _, pr := range [][2]string{{"mac", "build"}, {"build", "mac"}} {
		if err := waitPath(pr[0], pr[1], apitype.PathDirect, 40*time.Second); err != nil {
			return err
		}
	}
	long := tickAsync("mac", "build", 18)
	for _, c := range [][]string{{"mac", "build"}, {"build", "mac"}} {
		if _, err := deneb(c[0], "debug", "relaylate", c[1], "25", "400"); err != nil {
			return err
		}
	}
	defer func() {
		for _, c := range [][]string{{"mac", "build"}, {"build", "mac"}} {
			deneb(c[0], "debug", "relaylate", c[1], "0", "0")
		}
	}()
	if _, err := deneb("mac", "debug", "loss", "build", "20"); err != nil {
		return err
	}
	defer deneb("mac", "debug", "loss", "build", "0")
	kinds := map[string]bool{}
	end := time.Now().Add(15 * time.Second)
	for time.Now().Before(end) {
		for _, n := range [][2]string{{"mac", "build"}, {"build", "mac"}} {
			if p, err := debugPath(n[0], n[1]); err == nil {
				kinds[n[0]+":"+p.Kind] = true
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	r, _ := long()
	fmt.Printf("     lossy-both: paths seen %v; long connection %d lines, longest stall %d ms\n", kinds, r.Lines, r.MaxGapMs)
	if kinds["mac:relay"] || kinds["build:relay"] {
		return fmt.Errorf("moved to the relay although it is no cleaner: %v", kinds)
	}
	return nil
}

// checkRelayFirst times scene 1 ("先经中转连上，打通后换直连") for a new
// pair of devices: the Mac adds build to 周末联机, and nas, which never
// had build as a peer, reaches it first through the relay, then direct.
func checkRelayFirst() error {
	defer deneb("mac", "lan", "remove", "周末联机", "构建服务器")
	t0 := time.Now()
	if out, err := deneb("mac", "lan", "add", "周末联机", "构建服务器"); err != nil {
		return fmt.Errorf("%v %s", err, out)
	}
	var first, direct time.Duration
	firstKind := ""
	for time.Since(t0) < 15*time.Second && direct == 0 {
		if p, err := debugPath("nas", "build"); err == nil {
			if first == 0 && (p.Kind == "relay" || p.Kind == "direct") {
				first, firstKind = time.Since(t0), p.Kind
			}
			if p.Kind == "direct" {
				direct = time.Since(t0)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if direct == 0 {
		return fmt.Errorf("nas never went direct to build (first: %s after %s)", firstKind, first)
	}
	fmt.Printf("     relay-first: nas reaches build (just added to 周末联机) %s after the add, first through the %s; direct after %s\n",
		first.Round(10*time.Millisecond), firstKind, direct.Round(10*time.Millisecond))
	return nil
}
