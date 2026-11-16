package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
)

// TestOwnControlEntrances: a device that saved Deneb's own address, as an
// older release did, goes by all of the server's entrances, starting with
// the one that got through last (design/plan/control-reachability-20261116.md
// §9.3); the window's sign-in learns their pins. One logged in to someone's
// own server stays with it. The entrance that gets through is kept.
func TestOwnControlEntrances(t *testing.T) {
	pin := "sha256:" + strings.Repeat("ab", 32)
	ents := []proto.ControlEntrance{
		{URL: "https://203.0.113.10:47443", Pin: pin},
		{URL: "https://203.0.113.11:47443", Pin: pin},
		{URL: "https://control.example.com"},
	}
	old := "https://198.51.100.7:47443"
	o := Options{StateDir: t.TempDir(), Mode: apitype.ModeUserspace, BindInterface: "none", Logf: quiet, Version: "test",
		ControlEntrances: ents, OwnControls: []string{old}}
	e, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	e.mu.Lock()
	e.st.ControlURL, e.st.ControlPin, e.st.ControlVia = old, pin, ents[1].URL
	cc, err := e.newControlClientLocked()
	pins := e.controlPinsLocked()
	e.mu.Unlock()
	if err != nil || cc.URL() != ents[1].URL {
		t.Fatalf("an older release's address: by %v %v, want %s", cc, err, ents[1].URL)
	}
	if len(pins) != 3 || pins[ents[0].URL] != pin || pins[ents[2].URL] != "" {
		t.Errorf("pins for the sign-in: %v", pins)
	}

	e.mu.Lock()
	e.st.ControlURL = "https://198.51.100.99:47443"
	own := e.ownControlLocked()
	cc, err = e.newControlClientLocked()
	pins = e.controlPinsLocked()
	e.mu.Unlock()
	if own || err != nil || cc.URL() != "https://198.51.100.99:47443" || pins != nil {
		t.Errorf("someone's own server: own %v, by %v %v, pins %v", own, cc, err, pins)
	}

	e.keepEntrance(ents[2].URL)
	b, err := os.ReadFile(filepath.Join(o.StateDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var st persisted
	if json.Unmarshal(b, &st) != nil || st.ControlVia != ents[2].URL {
		t.Errorf("kept %q, want %s", st.ControlVia, ents[2].URL)
	}
}

// TestUpgradeFromOneSavedAddress: a device logged in under a release that
// saved one address and its pin (rc.3 and before), started by this one:
// its state file as it was, it goes by all of Deneb's entrances, the first
// first, whether the address it saved is still among them or only an older
// release's (OwnControls); its login and keys stay.
func TestUpgradeFromOneSavedAddress(t *testing.T) {
	pin := "sha256:" + strings.Repeat("cd", 32)
	ents := []proto.ControlEntrance{
		{URL: "https://203.0.113.10:47443", Pin: pin},
		{URL: "https://203.0.113.11:47443", Pin: pin},
		{URL: "https://control.example.com"},
	}
	for _, saved := range []string{ents[0].URL, "https://198.51.100.7:47443"} {
		dir := t.TempDir()
		// As an older release left it: logged in, one address, no entrance.
		e, err := New(Options{StateDir: dir, Mode: apitype.ModeUserspace, BindInterface: "none", Logf: quiet, Version: "0.1.9-rc.3"})
		if err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		e.st.ControlURL, e.st.ControlPin, e.st.LoggedIn, e.st.Device = saved, pin, true, "dev-mac"
		e.saveState()
		machine := e.st.Machine.Public()
		e.mu.Unlock()
		e.Close()
		var old map[string]any
		if b, err := os.ReadFile(filepath.Join(dir, "state.json")); err != nil || json.Unmarshal(b, &old) != nil || old["controlVia"] != nil {
			t.Fatalf("the older state: %v %v", old, err)
		}

		e, err = New(Options{StateDir: dir, Mode: apitype.ModeUserspace, BindInterface: "none", Logf: quiet, Version: "test",
			ControlEntrances: ents, OwnControls: []string{"https://198.51.100.7:47443"}})
		if err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		cc, err := e.newControlClientLocked()
		loggedIn, same := e.st.LoggedIn, e.st.Machine.Public() == machine
		e.mu.Unlock()
		e.Close()
		if err != nil || cc.URL() != ents[0].URL || cc.Through(saved+"/login?c=x") == "" {
			t.Errorf("saved %s: by %v %v; want the entrances, the first first", saved, cc, err)
		}
		if !loggedIn || !same {
			t.Errorf("saved %s: logged in %v, same machine key %v", saved, loggedIn, same)
		}
	}
}
