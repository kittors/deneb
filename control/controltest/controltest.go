// Package controltest runs a development control server inside a test
// and logs fake devices in on it, for the tests of programs that talk to
// one, as the relay does. Nothing here is for a real deployment.
package controltest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/control"
	"github.com/kittors/deneb/proto"
)

// Server is a development control server: the seed's test accounts
// (kittors, jie, mia, leo @deneb.test), plain HTTP, its admin socket.
type Server struct {
	*control.Server
	URL  string
	Sock string // the admin socket

	mu   sync.Mutex
	logs []string
}

// New starts a server for the test; it stops when the test ends.
func New(t testing.TB) *Server {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	s := &Server{URL: "http://" + ts.Listener.Addr().String()}
	cs, err := control.New(control.Config{
		DataDir: t.TempDir(), PublicURL: s.URL, Dev: true, Seed: control.DevSeed(),
		Logf: func(f string, a ...any) { s.mu.Lock(); s.logs = append(s.logs, fmt.Sprintf(f, a...)); s.mu.Unlock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Server = cs
	ts.Config.Handler = cs.Handler()
	ts.Start()
	// A Unix socket's path has to fit in about 100 bytes; test
	// directories are long.
	dir, err := os.MkdirTemp("", "dct")
	if err != nil {
		t.Fatal(err)
	}
	s.Sock = filepath.Join(dir, control.AdminSocketName)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { cs.ServeAdmin(ctx, s.Sock); close(done) }()
	for range 200 {
		if _, err := os.Stat(s.Sock); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Cleanup(func() {
		cancel()
		<-done
		ts.CloseClientConnections()
		ts.Close()
		cs.Close()
		os.RemoveAll(dir)
	})
	return s
}

// Logs is what the server logged so far.
func (s *Server) Logs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.logs...)
}

// client is this package's own: a program under test may change
// http.DefaultClient (the relay does, for derpserver's admission requests).
var client = &http.Client{Timeout: 30 * time.Second}

// Device is a fake engine logged in on the server.
type Device struct {
	t     testing.TB
	url   string
	mk    proto.MachinePrivate
	token string
	n     int
	ID    proto.DeviceID
	Node  proto.NodePublic
}

// Login logs a device with node key node in as the test account email.
func (s *Server) Login(t testing.TB, email, host string, node proto.NodePublic) *Device {
	t.Helper()
	d := &Device{t: t, url: s.URL, mk: proto.NewMachineKey(), Node: node}
	var ch proto.AuthChallenge
	d.must("POST", proto.PathAuthChallenge, proto.AuthChallengeRequest{MachineKey: d.mk.Public()}, &ch)
	var tok proto.AuthToken
	d.must("POST", proto.PathAuthToken, proto.AuthTokenRequest{
		MachineKey: d.mk.Public(), Challenge: ch.Challenge, Signature: d.mk.SignChallenge(ch.Challenge),
	}, &tok)
	d.token = tok.Token
	var disco proto.DiscoPublic
	rand.Read(disco[:])
	req := proto.LoginRequest{NodeKey: node, DiscoKey: disco, Hostname: host, OS: proto.Linux, EngineVersion: "controltest"}
	var st proto.LoginState
	d.must("POST", proto.PathLogin, req, &st)
	d.must("POST", "/dev/approve", map[string]string{"login": st.LoginURL, "email": email}, nil)
	d.must("POST", proto.PathLogin, req, &st)
	if !st.LoggedIn || st.Device == "" {
		t.Fatalf("controltest: %s not logged in: %+v", host, st)
	}
	d.ID = st.Device
	return d
}

// Op runs an op as the device's owner.
func (d *Device) Op(op proto.OpType, a proto.OpArgs) (*proto.OpOutput, error) {
	d.t.Helper()
	d.n++
	var res proto.OpResult
	if err := d.call("POST", proto.PathOps, proto.Op{ID: fmt.Sprintf("controltest-%d", d.n), Op: op, Args: a}, &res); err != nil {
		return nil, err
	}
	if res.Result == nil {
		return &proto.OpOutput{}, nil
	}
	return res.Result, nil
}

// MustOp runs an op that has to go through.
func (d *Device) MustOp(op proto.OpType, a proto.OpArgs) *proto.OpOutput {
	d.t.Helper()
	out, err := d.Op(op, a)
	if err != nil {
		d.t.Fatalf("controltest: %s %+v: %v", op, a, err)
	}
	return out
}

// Logout logs the device out.
func (d *Device) Logout() {
	d.t.Helper()
	d.must("POST", proto.PathLogout, nil, nil)
}

// RelayUsage is what the owner's relays carried this month.
func (d *Device) RelayUsage() proto.RelayUsage {
	d.t.Helper()
	var u proto.RelayUsage
	d.must("GET", proto.PathRelayUsage, nil, &u)
	return u
}

// Report tells the server what the device's engine would.
func (d *Device) Report(r proto.Report) {
	d.t.Helper()
	d.must("POST", proto.PathReport, r, nil)
}

// NetMap is the device's network map now: the first frame of its stream.
func (d *Device) NetMap() *proto.NetMap {
	d.t.Helper()
	req, err := http.NewRequest("GET", d.url+proto.PathStream, nil)
	if err != nil {
		d.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+d.token)
	resp, err := client.Do(req)
	if err != nil {
		d.t.Fatal(err)
	}
	defer resp.Body.Close()
	var f proto.StreamFrame
	if err := json.NewDecoder(resp.Body).Decode(&f); err != nil || f.NetMap == nil {
		d.t.Fatalf("controltest: the stream's first frame: %+v, %v", f, err)
	}
	return f.NetMap
}

func (d *Device) must(method, path string, in, out any) {
	d.t.Helper()
	if err := d.call(method, path, in, out); err != nil {
		d.t.Fatalf("controltest: %s %s: %v", method, path, err)
	}
}

// call does one request; a refusal comes back as a *proto.Error.
func (d *Device) call(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, d.url+path, body)
	if err != nil {
		return err
	}
	if d.token != "" {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		var eb proto.ErrorBody
		if json.Unmarshal(b, &eb) == nil && eb.Error != nil {
			return eb.Error
		}
		return fmt.Errorf("%s: %s", resp.Status, b)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}
