package client_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/fake"
	"github.com/kittors/deneb/engine/localapi/localclient"
	"github.com/kittors/deneb/engine/localapi/socket"
)

// lateEngine is an engine whose watch can be handed any event, a State
// older than one it already sent too: what two of the engine's goroutines
// publishing at once can do, each with the State it took (11-08: an op's
// answer and the next op's, in TestLiveRefuse under load). What it is not
// asked is never called.
type lateEngine struct {
	localapi.Backend
	mu  sync.Mutex
	st  *apitype.State
	sub func(apitype.Event)
}

func (e *lateEngine) State() *apitype.State { e.mu.Lock(); defer e.mu.Unlock(); return e.st }
func (e *lateEngine) EngineVersion() string { return "test" }
func (e *lateEngine) Services(context.Context) ([]apitype.LocalService, error) {
	return nil, nil
}
func (e *lateEngine) Subscribe(fn func(apitype.Event)) func() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sub = fn
	return func() {}
}
func (e *lateEngine) push(ev apitype.Event) {
	e.mu.Lock()
	fn := e.sub
	e.mu.Unlock()
	fn(ev)
}

// A State older than the one the store has comes late: the store keeps the
// newer one, so an op that went through does not seem undone.
func TestLiveKeepsTheNewerState(t *testing.T) {
	dir, err := os.MkdirTemp("", "dnb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	newer := fake.New().Snapshot()
	newer.Version = 5
	older := fake.New().Snapshot()
	older.Version = 4
	e := &lateEngine{st: newer}
	path := filepath.Join(dir, "d.sock")
	ln, err := socket.Listen(path, socket.ListenOptions{AllowUID: -1})
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: localapi.Handler(e, nil)}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	l := client.NewLive(localclient.New(path))
	l.Start()
	t.Cleanup(l.Close)

	waitFor(t, l, "the newer state", func(s *model.Snapshot) bool { return s.Version == 5 })
	e.push(apitype.Event{Type: apitype.EventState, State: older})
	// Then something the store shows, after the late State in the stream:
	// once it shows, the late State was read.
	since := time.Now().Add(-time.Minute).Round(time.Second)
	e.push(apitype.Event{Type: apitype.EventUsage, Using: []apitype.ServiceUse{{DeviceID: fake.JiePC, Since: since}}})
	s := waitFor(t, l, "the usage after it", func(s *model.Snapshot) bool { return !s.Using[model.DeviceID(fake.JiePC)].IsZero() })
	if s.Version != 5 {
		t.Errorf("after the late State the store is at version %d, want 5", s.Version)
	}
}
