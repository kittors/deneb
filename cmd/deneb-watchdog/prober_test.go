package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/control"
	"github.com/kittors/deneb/control/controltest"
	"github.com/kittors/deneb/control/probe"
	"github.com/kittors/deneb/proto"
)

// post is a relay program's request to the control server.
func post(t *testing.T, url, secret, path string, in, out any) {
	t.Helper()
	b, _ := json.Marshal(in)
	req, _ := http.NewRequest("POST", url+path, bytes.NewReader(b))
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("%s: %s", path, resp.Status)
	}
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
}

// TestProber: the watchdog as the control server's second place. The
// control server cannot reach a relay, the watchdog can: the relay is
// usable, and its probes say where each was seen from.
func TestProber(t *testing.T) {
	secret := strings.Repeat("p", 32)
	var mu sync.Mutex
	now := time.Now().UTC()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	cs := controltest.NewWith(t, func(c *control.Config) {
		c.Now, c.ProbeSecret = clock, secret
		c.ProbeRelay = func(context.Context, proto.ProbeTarget) proto.RelayProbe {
			return proto.RelayProbe{TCP: proto.RelayProbeTimeout, UDP: proto.RelayProbeTimeout}
		}
	})
	var nk proto.NodePublic
	rand.Read(nk[:])
	mac := cs.Login(t, "kittors@deneb.test", "mac", nk)
	out := mac.MustOp(proto.OpRelayCreate, proto.OpArgs{Name: "东京"})
	var reg proto.RelayRegisterResult
	post(t, cs.URL, "", proto.PathRelayRegister, proto.RelayRegisterRequest{Token: out.RelayToken, RelayInfo: proto.RelayInfo{
		IPv4: netip.MustParseAddr("203.0.113.9"), DERPPort: 47445, STUNPort: 47478, Pin: "sha256:" + strings.Repeat("ab", 32)}}, &reg)

	if _, err := newProber("hk-spare", &ProberConfig{Control: cs.URL, Secret: "short"}, func(string) string { return "" }); err == nil {
		t.Fatal("a short secret")
	}
	p, err := newProber("hk-spare", &ProberConfig{Control: cs.URL, Secret: "from the file is replaced"}, func(k string) string {
		if k == "DENEB_PROBE_SECRET" {
			return secret
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	p.logf = t.Logf
	p.check = func(_ context.Context, tg proto.ProbeTarget) proto.RelayProbe {
		return proto.RelayProbe{TCP: proto.RelayProbeOK, UDP: proto.RelayProbeOK}
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go p.run(ctx)

	// Until the watchdog is polling, the check is the control server's
	// alone; each time ten seconds on, so a check runs again.
	var st proto.RelayStatus
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		mu.Lock()
		now = now.Add(11 * time.Second)
		mu.Unlock()
		post(t, cs.URL, reg.Secret, proto.PathRelayCheck, struct{}{}, &st)
		if len(st.Probes) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the watchdog never checked: %+v", st)
		}
	}
	if st.State != proto.RelayOK || st.Probes[0].From != "control" || st.Probes[0].TCP != proto.RelayProbeTimeout ||
		st.Probes[1].From != "hk-spare" || st.Probes[1].TCP != proto.RelayProbeOK {
		t.Fatalf("with the watchdog: %+v", st)
	}
}

// TestProberOnlyPublic: whoever asks, the watchdog checks only addresses
// on the Internet. This machine's, its networks' and a carrier's NAT's it
// answers as not checked, without a packet sent.
func TestProberOnlyPublic(t *testing.T) {
	done := make(chan proto.ProbeDone, 1)
	cs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var d proto.ProbeDone
		json.NewDecoder(r.Body).Decode(&d)
		done <- d
		w.Write([]byte("{}"))
	}))
	defer cs.Close()
	p, err := newProber("hk-spare", &ProberConfig{Control: cs.URL, Secret: strings.Repeat("p", 32)}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	p.logf = t.Logf
	var mu sync.Mutex
	var checked []netip.Addr
	p.check = func(_ context.Context, tg proto.ProbeTarget) proto.RelayProbe {
		mu.Lock()
		defer mu.Unlock()
		checked = append(checked, tg.Addr)
		return proto.RelayProbe{TCP: proto.RelayProbeOK, UDP: proto.RelayProbeOK}
	}
	public := netip.MustParseAddr("203.0.113.9")
	var targets []proto.ProbeTarget
	for _, s := range []string{"203.0.113.9", "127.0.0.1", "192.168.1.5", "100.64.3.4", "::1", "fd00::5"} {
		a := netip.MustParseAddr(s)
		targets = append(targets, proto.ProbeTarget{Family: probe.Family(a), Addr: a, DERPPort: 47445, STUNPort: 47478})
	}
	p.do(t.Context(), proto.ProbeJob{ID: "pj_1", Targets: targets})
	d := <-done
	if d.ID != "pj_1" || len(d.Results) != len(targets) || !slices.Equal(checked, []netip.Addr{public}) {
		t.Fatalf("answered %+v, checked %v", d, checked)
	}
	for _, r := range d.Results {
		want := proto.RelayProbeSkipped
		if r.Addr == public {
			want = proto.RelayProbeOK
		}
		if r.TCP != want || r.UDP != want || r.Family != probe.Family(r.Addr) {
			t.Fatalf("%s: %+v", r.Addr, r)
		}
	}
}
