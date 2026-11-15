//go:build !windows

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// fakeEngine writes a stand-in for denebd at path: a script that answers
// `version` with ver.
func fakeEngine(t *testing.T, path, ver string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n[ \"$1\" = version ] && echo "+ver+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// signEngine writes path's signature, as deploy/sign-engine.go does, saying
// it is version ver.
func signEngine(t *testing.T, path, ver string, key ed25519.PrivateKey) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	js, _ := json.Marshal(engineSig{Version: ver, SHA256: hex.EncodeToString(sum[:]), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, sum[:]))})
	if err := os.WriteFile(sigPath(path), js, 0o644); err != nil {
		t.Fatal(err)
	}
}

// testKey makes engineKeys a key of the test's own, for the test.
func testKey(t *testing.T) ed25519.PrivateKey {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	old := engineKeys
	engineKeys = []string{base64.StdEncoding.EncodeToString(pub)}
	t.Cleanup(func() { engineKeys = old })
	return priv
}

// runningVersion makes version v for the test.
func runningVersion(t *testing.T, v string) {
	old := version
	version = v
	t.Cleanup(func() { version = old })
}

// TestEngineKeysHaveTheWindows: the engine takes what is signed with the key
// of the window's updates (mygo.json).
func TestEngineKeysHaveTheWindows(t *testing.T) {
	b, err := os.ReadFile("../deneb/mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Updates struct {
			PublicKey string `json:"publicKey"`
		} `json:"updates"`
	}
	if err := json.Unmarshal(b, &c); err != nil || c.Updates.PublicKey == "" {
		t.Fatalf("mygo.json: %v", err)
	}
	if !slices.Contains(engineKeys, c.Updates.PublicKey) {
		t.Errorf("engineKeys %v lack mygo.json's %s", engineKeys, c.Updates.PublicKey)
	}
}

// TestEngineSignature: a signature checks out for its file and key only.
func TestEngineSignature(t *testing.T) {
	key := testKey(t)
	dir := t.TempDir()
	exe := filepath.Join(dir, "denebd")
	fakeEngine(t, exe, "0.2.0")
	if _, _, err := readEngineSig(exe); !errors.Is(err, errNoSig) {
		t.Errorf("no signature: %v", err)
	}
	if signedEngine(exe) {
		t.Error("unsigned, and called signed")
	}
	signEngine(t, exe, "0.2.0", key)
	s, _, err := readEngineSig(exe)
	if err != nil || s.check(exe, engineKeys) != nil || !signedEngine(exe) {
		t.Fatalf("signed: %v", err)
	}
	_, other, _ := ed25519.GenerateKey(nil)
	if err := s.check(exe, []string{base64.StdEncoding.EncodeToString(other.Public().(ed25519.PublicKey))}); !errors.Is(err, errBadSig) {
		t.Errorf("another key: %v", err)
	}
	os.WriteFile(exe, []byte("#!/bin/sh\necho 0.2.0 # changed\n"), 0o755)
	if err := s.check(exe, engineKeys); !errors.Is(err, errBadSig) || signedEngine(exe) {
		t.Errorf("changed after signing: %v", err)
	}
	os.WriteFile(sigPath(exe), []byte("{"), 0o644)
	if _, _, err := readEngineSig(exe); !errors.Is(err, errBadSig) {
		t.Errorf("a broken signature file: %v", err)
	}
}

// TestStageEngine: what the service takes in, and what it answers for what
// it does not (engine-self-update.md §2, §6).
func TestStageEngine(t *testing.T) {
	key := testKey(t)
	runningVersion(t, "0.1.5")
	dir := t.TempDir()
	unsignedSelf := filepath.Join(dir, "service", "denebd")
	fakeEngine(t, unsignedSelf, "0.1.5")
	signedSelf := filepath.Join(dir, "signed", "denebd")
	fakeEngine(t, signedSelf, "0.1.5")
	signEngine(t, signedSelf, "0.1.5", key)
	_, otherKey, _ := ed25519.GenerateKey(nil)

	type tc struct {
		name    string
		ver     string // the program's
		sign    string // the version its signature says; "" for none
		key     ed25519.PrivateKey
		tamper  bool
		self    string
		failed  string
		current string
		want    string // the result; "" for taken in
	}
	for _, c := range []tc{
		{name: "newer, signed", ver: "0.2.0", sign: "0.2.0", self: signedSelf},
		{name: "same", ver: "0.1.5", sign: "0.1.5", self: signedSelf, want: apitype.UpdateSame},
		{name: "older", ver: "0.1.0", sign: "0.1.0", self: signedSelf, want: apitype.UpdateOlder},
		{name: "unsigned, to an unsigned service", ver: "0.2.0", self: unsignedSelf, want: apitype.UpdateNeedsInstall},
		{name: "unsigned, to a service that came signed", ver: "0.2.0", self: signedSelf, want: apitype.UpdateBadSignature},
		{name: "changed after signing", ver: "0.2.0", sign: "0.2.0", tamper: true, self: signedSelf, want: apitype.UpdateBadSignature},
		{name: "another key", ver: "0.2.0", sign: "0.2.0", key: otherKey, self: signedSelf, want: apitype.UpdateBadSignature},
		{name: "an old one whose signature says new", ver: "0.1.0", sign: "0.2.0", self: signedSelf, want: apitype.UpdateBadSignature},
		{name: "the one that could not start here", ver: "0.2.0", sign: "0.2.0", self: signedSelf, failed: "0.2.0", want: apitype.UpdateRolledBack},
		{name: "a release, to a development build", ver: "0.2.0", sign: "0.2.0", self: unsignedSelf, current: "dev"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.current != "" {
				runningVersion(t, c.current)
			}
			src := filepath.Join(t.TempDir(), "app", "denebd")
			fakeEngine(t, src, c.ver)
			if c.sign != "" {
				k := key
				if c.key != nil {
					k = c.key
				}
				signEngine(t, src, c.sign, k)
			}
			if c.tamper {
				b, _ := os.ReadFile(src)
				os.WriteFile(src, append(b, '#'), 0o755)
			}
			stagedExe := filepath.Join(filepath.Dir(c.self), "denebd.update")
			st, out := stageEngine(context.Background(), src, stagedExe, c.self, version, c.failed)
			if c.want != "" {
				if st != nil || out.Result != c.want {
					t.Fatalf("got %+v (%v), want %s", out, st, c.want)
				}
				if _, err := os.Stat(stagedExe); err == nil {
					t.Error("the copy is left behind")
				}
				return
			}
			if st == nil || st.version != c.ver || out.Version != c.ver {
				t.Fatalf("not taken in: %+v", out)
			}
			if !signedEngine(st.exe) {
				t.Error("the copy and its signature do not check out")
			}
			st.remove()
		})
	}
}

// TestUpdateNote: the new engine counts its starts, and the old one is put
// back after maxStarts; once put in, the old one goes; an update that never
// got swapped in is cleared (engine-self-update.md §2 steps 10 to 12).
func TestUpdateNote(t *testing.T) {
	dir := t.TempDir()
	note := filepath.Join(dir, "update.json")
	prev := filepath.Join(dir, "denebd.prev")
	var restored, cleaned []string
	left := 0
	restore := func(p string) error { restored = append(restored, p); return nil }
	leftovers := func() { left++ }
	writeNote(note, &updateNote{From: "0.1.5", To: "0.2.0", Prev: prev})
	for i := 1; i <= maxStarts; i++ {
		if startCheck(note, "0.2.0", restore, leftovers, t.Logf) {
			t.Fatalf("went back at start %d", i)
		}
		if n := readNote(note); n == nil || n.Tries != i {
			t.Fatalf("start %d: %+v", i, n)
		}
	}
	if !startCheck(note, "0.2.0", restore, leftovers, t.Logf) || !slices.Equal(restored, []string{prev}) {
		t.Fatalf("did not go back after %d starts: %v", maxStarts, restored)
	}
	if n := readNote(note); n == nil || n.To != "" || n.Failed != "0.2.0" || failedVersion(note) != "0.2.0" {
		t.Errorf("after going back: %+v", n)
	}
	// The old one starts: nothing to count.
	if startCheck(note, "0.1.5", restore, leftovers, t.Logf) || left != 0 {
		t.Error("the old one counted its start")
	}

	// Another update, put in once it ran a while: the old one goes, what
	// failed stays known.
	writeNote(note, &updateNote{From: "0.1.5", To: "0.3.0", Prev: prev, Failed: "0.2.0"})
	if startCheck(note, "0.3.0", restore, leftovers, t.Logf) {
		t.Fatal("went back")
	}
	putIn(note, "0.3.0", func(p string) { cleaned = append(cleaned, p) })
	if !slices.Equal(cleaned, []string{prev}) {
		t.Errorf("cleaned %v", cleaned)
	}
	if n := readNote(note); n == nil || n.To != "" || n.Failed != "0.2.0" {
		t.Errorf("after putting in: %+v", n)
	}

	// Cut off between the note and the swap: the old one starts, clears it.
	writeNote(note, &updateNote{From: "0.3.0", To: "0.4.0", Prev: prev})
	if startCheck(note, "0.3.0", restore, leftovers, t.Logf) || left != 1 {
		t.Errorf("leftovers %d", left)
	}
	if _, err := os.Stat(note); err == nil {
		t.Error("the note of an update never put in stays")
	}
}
