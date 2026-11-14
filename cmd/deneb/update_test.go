package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// fakeEngine answers followEngine as an engine of version would, its
// update ending in result.
type fakeEngine struct {
	version, result string
	updated, down   []string
}

func (f *fakeEngine) Version(context.Context) (*apitype.VersionInfo, error) {
	if f.version == "" {
		return nil, errors.New("no engine")
	}
	return &apitype.VersionInfo{API: apitype.APIVersion, Engine: f.version}, nil
}

func (f *fakeEngine) UpdateEngine(_ context.Context, path string) (*apitype.UpdateResult, error) {
	f.updated = append(f.updated, path)
	return &apitype.UpdateResult{Result: f.result, Version: "0.2.0"}, nil
}

func (f *fakeEngine) Shutdown(context.Context) error {
	f.down = append(f.down, f.version)
	return nil
}

// TestFollowEngine: the engine the package ships replaces a running one of
// another version, the way the engine says (i4): the service replaces
// itself, one the window started stops for keepEngine to start the shipped
// one, the service of an unsigned release is installed again; an engine of
// the window's version, or a package that ships none, is left as it is.
func TestFollowEngine(t *testing.T) {
	const bundled = `C:\Users\k\AppData\Local\Programs\Deneb\bin\denebd.exe`
	cases := []struct {
		name, bundled, running, result string
		installErr                     error
		wantUpdate, wantDown, wantInst bool
		say                            string
	}{
		{name: "none shipped", running: "0.1.0", say: "no engine shipped"},
		{name: "as shipped", bundled: bundled, running: "0.2.0", say: "as shipped"},
		{name: "no engine", bundled: bundled, say: "engine version"},
		{name: "service", bundled: bundled, running: "0.1.0", result: apitype.UpdateDone, wantUpdate: true, say: "updated"},
		{name: "service newer", bundled: bundled, running: "0.3.0", result: apitype.UpdateOlder, wantUpdate: true, say: "older"},
		{name: "window's", bundled: bundled, running: "0.1.0", result: apitype.UpdateNotService, wantUpdate: true, wantDown: true, say: "stopped"},
		{name: "unsigned", bundled: bundled, running: "0.1.0", result: apitype.UpdateNeedsInstall, wantUpdate: true, wantInst: true, say: "installed"},
		{name: "declined", bundled: bundled, running: "0.1.0", result: apitype.UpdateNeedsInstall, installErr: errors.New("not allowed"), wantUpdate: true, wantInst: true, say: "not allowed"},
		{name: "refused", bundled: bundled, running: "0.1.0", result: apitype.UpdateBadSignature, wantUpdate: true, say: "bad-signature"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &fakeEngine{version: c.running, result: c.result}
			var installed []string
			say := followEngine(context.Background(), e, c.bundled, "0.2.0", func(_ context.Context, exe string) error {
				installed = append(installed, exe)
				return c.installErr
			})
			if !strings.Contains(say, c.say) {
				t.Errorf("said %q, want it to say %q", say, c.say)
			}
			if got := len(e.updated) > 0; got != c.wantUpdate || (got && e.updated[0] != bundled) {
				t.Errorf("asked to update to %q, want %v", e.updated, c.wantUpdate)
			}
			if got := len(e.down) > 0; got != c.wantDown {
				t.Errorf("stopped %q, want %v", e.down, c.wantDown)
			}
			if got := len(installed) > 0; got != c.wantInst || (got && installed[0] != bundled) {
				t.Errorf("installed %q, want %v", installed, c.wantInst)
			}
		})
	}
}

// TestDirSource: a release from a folder, its manifest by this platform's
// name, its files by the names the manifest's URLs end in.
func TestDirSource(t *testing.T) {
	dir := t.TempDir()
	manifest := "update-" + runtime.GOOS + "-" + runtime.GOARCH + ".json"
	if err := os.WriteFile(filepath.Join(dir, manifest), []byte(`{"version":"0.2.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "deneb-0.2.0.tar.gz"), []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := dirSource(dir)
	read := func(r io.ReadCloser, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read(src.Manifest(context.Background(), false)); got != `{"version":"0.2.0"}` {
		t.Errorf("manifest %q", got)
	}
	if got := read(src.Open(context.Background(), "https://github.com/kittors/deneb/releases/download/v0.2.0/deneb-0.2.0.tar.gz")); got != "archive" {
		t.Errorf("archive %q", got)
	}
	if _, err := src.Open(context.Background(), "https://example.com/../../etc/passwd"); err == nil {
		t.Error("opened a file outside the folder")
	}
}

func TestHTTPSUpdateSourceRejectsOtherOrigins(t *testing.T) {
	source := httpsUpdateSource("https://deneb.example/download/")
	for _, url := range []string{"http://deneb.example/download/update.json", "https://other.example/download/update.json", "https://deneb.example/elsewhere/update.json", "https://user@deneb.example/download/update.json"} {
		if body, err := source.Open(context.Background(), url); err == nil {
			body.Close()
			t.Fatalf("accepted untrusted URL %s", url)
		}
	}
}
