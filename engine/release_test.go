//go:build !denebtest

package engine

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kittors/deneb/engine/localapi"
	"github.com/kittors/deneb/engine/localapi/apitype"
)

// TestReleaseHasNoTestAPI: a release build (no denebtest tag) serves none
// of the test build's /v1/test/... endpoints, which reach past the filter
// (inject, attack-route, filter off) or stop any engine (M7 security
// review). The local API is only what localapi.Handler routes.
func TestReleaseHasNoTestAPI(t *testing.T) {
	if TestBuild {
		t.Fatal("TestBuild in a build without the denebtest tag")
	}
	e, err := New(Options{StateDir: t.TempDir(), Mode: apitype.ModeUserspace, BindInterface: "none", Logf: quiet, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	h := localapi.Handler(e, e.TestHandler())
	for _, r := range []struct{ method, path string }{
		{"POST", "/v1/test/shutdown"},
		{"POST", "/v1/test/filter"},
		{"POST", "/v1/test/inject"},
		{"POST", "/v1/test/attack-route"},
		{"GET", "/v1/test/paths"},
		{"GET", "/v1/test/drops"},
		{"GET", "/v1/test/http"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(r.method, r.path, strings.NewReader("{}")))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s: %d, want 404", r.method, r.path, w.Code)
		}
	}
	select {
	case <-e.StopRequested():
		t.Error("the engine was asked to stop")
	default:
	}
}

// TestReleaseRefusesPlainHTTPControl: a release engine will not talk plain
// http to a control server off this machine (M7 review).
func TestReleaseRefusesPlainHTTPControl(t *testing.T) {
	for url, ok := range map[string]bool{"http://203.0.113.5:47443": false, "http://127.0.0.1:47443": true} {
		e, err := New(Options{StateDir: t.TempDir(), Mode: apitype.ModeUserspace, BindInterface: "none", Logf: quiet, Version: "test", ControlURL: url})
		if err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		_, err = e.newControlClientLocked()
		e.mu.Unlock()
		e.Close()
		if (err == nil) != ok {
			t.Errorf("%s: %v", url, err)
		}
	}
}
