package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/control"
	"github.com/kittors/deneb/control/controltest"
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
		DERPPort: 47445, STUNPort: 47478, Pin: "sha256:" + strings.Repeat("ab", 32)}}, &reg)

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
