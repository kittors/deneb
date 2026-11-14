package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestMacStateImport(t *testing.T) {
	for _, kind := range []string{"identity", "active", "symlink", "public", "invalid", "existing", "resolver-path"} {
		t.Run(kind, func(t *testing.T) {
			from, to := t.TempDir(), t.TempDir()
			if err := os.Chmod(from, 0o700); err != nil {
				t.Fatal(err)
			}
			state := []byte(`{"device":"same-device","loggedIn":true}`)
			for name, b := range map[string][]byte{"state.json": state, "netmap.json": []byte(`{"version":42,"network":{"ipv4":"10.48.0.0/16","domain":"deneb.internal"}}`), "kernel-undo.json": []byte(`{"interface":"utun99"}`)} {
				if err := os.WriteFile(filepath.Join(from, name), b, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			lock, err := os.OpenFile(filepath.Join(from, "engine.lock"), os.O_CREATE|os.O_RDONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			switch kind {
			case "active":
				if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				os.Remove(filepath.Join(from, "state.json"))
				os.Symlink(filepath.Join(from, "netmap.json"), filepath.Join(from, "state.json"))
			case "public":
				os.Chmod(filepath.Join(from, "state.json"), 0o644)
			case "invalid":
				os.WriteFile(filepath.Join(from, "state.json"), []byte("not JSON"), 0o600)
			case "resolver-path":
				os.WriteFile(filepath.Join(from, "netmap.json"), []byte(`{"network":{"domain":"../../arbitrary"}}`), 0o600)
			case "existing":
				os.WriteFile(filepath.Join(to, "state.json"), []byte(`{"device":"already-installed"}`), 0o600)
			}
			err = importMacState(from, to, uint32(os.Getuid()))
			wantOK := kind == "identity" || kind == "existing"
			if (err == nil) != wantOK {
				t.Fatalf("import %s: %v", kind, err)
			}
			if !wantOK {
				if _, err := os.Stat(filepath.Join(to, "state.json")); !os.IsNotExist(err) {
					t.Fatal("refused import left a committed identity")
				}
				return
			}
			b, err := os.ReadFile(filepath.Join(to, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if kind == "identity" && string(b) != string(state) || kind == "existing" && !strings.Contains(string(b), "already-installed") {
				t.Fatalf("identity changed: %s", b)
			}
			if _, err := os.Stat(filepath.Join(to, "kernel-undo.json")); !os.IsNotExist(err) {
				t.Fatal("import trusted a user-supplied privileged undo record")
			}
		})
	}
}

func TestMacServiceDefinition(t *testing.T) {
	p := filepath.Join(t.TempDir(), "service.plist")
	if err := os.WriteFile(p, []byte(macServiceDefinition(501)), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command("/usr/bin/plutil", "-lint", p).CombinedOutput(); err != nil {
		t.Fatalf("launchd plist: %v: %s", err, b)
	}
	for _, item := range []struct{ key, want string }{
		{"Label", macServiceLabel},
		{"ProgramArguments:0", macServiceBin},
		{"ProgramArguments:3", "kernel"},
		{"ProgramArguments:5", macServiceState},
		{"ProgramArguments:9", "501"},
		{"Umask", "18"},
	} {
		b, err := exec.Command("/usr/libexec/PlistBuddy", "-c", "Print :"+item.key, p).CombinedOutput()
		if err != nil || strings.TrimSpace(string(b)) != item.want {
			t.Errorf("%s: %q, %v; want %q", item.key, b, err, item.want)
		}
	}
}

func TestMacAtomicFileRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "outside")
	os.WriteFile(target, []byte("keep"), 0o600)
	link := filepath.Join(dir, "identity")
	os.Symlink(target, link)
	if err := macAtomicFile(link, []byte("replace"), 0o600); err == nil {
		t.Fatal("followed a symlink")
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Fatal("changed a file outside the install target")
	}
}
