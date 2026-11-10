package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/tun"
	"github.com/kittors/deneb/proto"
)

func TestRedactText(t *testing.T) {
	hex := strings.Repeat("0123456789abcdef", 4)
	for _, c := range []struct{ in, want string }{
		{"peer nodekey:" + hex + " up", "peer nodekey:…cdef up"},
		{"discokey:" + hex, "discokey:…cdef"},
		{`"machineKey": "privmachinekey:` + hex + `"`, `"machineKey": "privmachinekey:<removed>"`},
		{"pin sha256:" + hex, "pin sha256:…cdef"},
		{"wg key W+9pAbCdEfGhIjKlMnOpQrStUvWxYz012345678s2is= ok", "wg key …s2is= ok"},
		{"W+9pAbCdEfGhIjKlMnOpQrStUvWxYz012345678s2is=", "…s2is="},
		{"open https://c.example:47443/login?c=AbC-123_x now", "open https://c.example:47443/login?c=<removed> now"},
		{"send https://c.example/i/Zx9_k-2q", "send https://c.example/i/<removed>"},
		{"peer(W+9p…s2is) - Received handshake response", "peer(W+9p…s2is) - Received handshake response"},
		{"10.48.1.4:41721 rtt=100.7ms", "10.48.1.4:41721 rtt=100.7ms"},
	} {
		if got := string(redactText([]byte(c.in))); got != c.want {
			t.Errorf("redactText(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func TestRedactJSON(t *testing.T) {
	in := map[string]any{
		"loginURL": "https://c/login?c=x", "relayToken": "t", "secret": "s", "tokenExpiresAt": "2026",
		"name": "build", "peers": []any{map[string]any{"name": "nas", "password": "p"}},
		"private": nil,
	}
	out := redactJSON(in).(map[string]any)
	for _, k := range []string{"loginURL", "relayToken", "secret", "tokenExpiresAt"} {
		if out[k] != "<removed>" {
			t.Errorf("%s = %v", k, out[k])
		}
	}
	if out["name"] != "build" || out["private"] != nil {
		t.Errorf("kept fields changed: %v", out)
	}
	if p := out["peers"].([]any)[0].(map[string]any); p["password"] != "<removed>" || p["name"] != "nas" {
		t.Errorf("nested: %v", p)
	}
}

// TestDiagnosticsHoldNoKeys builds the diagnostics of a logged-in engine
// whose log names keys and links: every part is there, and no key, private
// or public, and no link's token is in any of them.
func TestDiagnosticsHoldNoKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("runs two engines")
	}
	relay, pool := testRelay(t)
	w := &world{relay: relay, members: map[proto.ScopeID][]*tnode{}, open: map[proto.ScopeID]map[*tnode][]proto.Service{}, refuses: map[[2]*tnode]bool{}}
	mk := func(i int, name string) *tnode {
		e, err := New(Options{StateDir: t.TempDir(), Mode: apitype.ModeUserspace, BindInterface: "none", RelayRoots: pool, Logf: quiet, Version: "test"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(e.Close)
		n := &tnode{e: e, id: proto.DeviceID("dev-" + name), name: name, owner: "kittors", person: "p-kittors",
			addr: netip.AddrFrom4([4]byte{10, 77, 0, byte(i)})}
		w.nodes = append(w.nodes, n)
		return n
	}
	mac := mk(1, "mac")
	build := mk(2, "build")
	w.members["lan/studio"] = []*tnode{mac, build}
	w.push()
	for _, n := range w.nodes {
		n.e.Start()
	}

	// What must not come out: every key the state file holds (private),
	// both devices' public keys, and the links' tokens.
	state, err := os.ReadFile(filepath.Join(mac.e.opts.StateDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	secrets := regexp.MustCompile(`[0-9a-f]{64}`).FindAllString(string(state), -1)
	if len(secrets) == 0 {
		t.Fatalf("no keys found in state.json: %s", state)
	}
	for _, n := range []*tnode{mac, build} {
		nd := n.node()
		secrets = append(secrets, strings.TrimPrefix(nd.NodeKey.String(), "nodekey:"), strings.TrimPrefix(nd.DiscoKey.String(), "discokey:"))
	}
	secrets = append(secrets, "LoginTok3n", "Inv1teTok3n")
	log := "2026/11/09 23:00:00 [wg] peer(W+9p…s2is) - Received handshake response\n" +
		"2026/11/09 23:00:01 key " + build.node().NodeKey.String() + " up\n" +
		"2026/11/09 23:00:02 open https://c.example/login?c=LoginTok3n\n" +
		"2026/11/09 23:00:03 invite https://c.example/i/Inv1teTok3n\n"
	if err := os.WriteFile(filepath.Join(mac.e.opts.StateDir, "denebd.log"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := mac.e.Diagnostics(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		r.Close()
		got[f.Name] = string(b)
	}
	for _, name := range []string{"README.txt", "engine.json", "state.json", "paths.json", "relays.json", "filter.json", "netmap.json", "system.txt", "log.txt", "goroutines.txt"} {
		if _, ok := got[name]; !ok {
			t.Errorf("no %s in the zip (has %d files)", name, len(got))
		}
	}
	for name, body := range got {
		for _, s := range secrets {
			if strings.Contains(body, s) {
				t.Errorf("%s holds %q", name, s)
			}
		}
	}
	if !strings.Contains(got["paths.json"], "build.kittors.deneb.internal") {
		t.Errorf("paths.json does not name the peer: %s", got["paths.json"])
	}
	if !strings.Contains(got["log.txt"], "Received handshake response") || !strings.Contains(got["log.txt"], "nodekey:…") {
		t.Errorf("log.txt: %s", got["log.txt"])
	}
}

// TestShutdownIsForUserSpace: the window stops its own user-space engine
// through the local API when the system service takes over; a kernel-mode
// engine refuses, being the system's to stop.
func TestShutdownIsForUserSpace(t *testing.T) {
	old := openKernel
	openKernel = func(tun.Config, func(string, ...any)) (*tun.Kernel, error) {
		return tun.Fake(newFakeTUN(), "fake0"), nil
	}
	defer func() { openKernel = old }()
	for _, mode := range []apitype.Mode{apitype.ModeUserspace, apitype.ModeKernel} {
		e, err := New(Options{StateDir: t.TempDir(), Mode: mode, BindInterface: "none", Logf: quiet, Version: "test"})
		if err != nil {
			t.Fatal(err)
		}
		err = e.Shutdown(context.Background())
		stopped := false
		select {
		case <-e.StopRequested():
			stopped = true
		default:
		}
		if want := mode == apitype.ModeUserspace; stopped != want || (err == nil) != want {
			t.Errorf("%s: Shutdown = %v, stopped %v", mode, err, stopped)
		}
		if r := e.UpdateEngine(context.Background(), `C:\x\denebd.exe`); r.Result != apitype.UpdateNotService {
			t.Errorf("%s: UpdateEngine = %+v", mode, r)
		}
		e.Close()
	}
}
