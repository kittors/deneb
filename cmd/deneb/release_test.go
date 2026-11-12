package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The release (design/plan/m6/client.md, "M7 · 打开自动更新"): mygo.json's
// updates, CHANGELOG.md, and deploy/release.sh, which puts a release on
// GitHub for the installed apps to update to.

// appConfig is what the release reads of mygo.json.
type appConfig struct {
	Version string `json:"version"`
	Updates *struct {
		PublicKey string `json:"publicKey"`
		GitHub    string `json:"github"`
		TagPrefix string `json:"tagPrefix"`
		Changelog string `json:"changelog"`
	} `json:"updates"`
}

func readAppConfig(t *testing.T) appConfig {
	t.Helper()
	b, err := os.ReadFile("mygo.json")
	if err != nil {
		t.Fatal(err)
	}
	var c appConfig
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

// TestChangelog: CHANGELOG.md has the section of mygo.json's version, the
// notes the update window shows; deploy/release.sh publishes nothing
// without it.
func TestChangelog(t *testing.T) {
	c := readAppConfig(t)
	b, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^## \[?` + regexp.QuoteMeta(c.Version) + `\]?(\s|$)`).Match(b) {
		t.Errorf("CHANGELOG.md has no section \"## %s\", the version of mygo.json", c.Version)
	}
}

// TestUpdatesConfig: once mygo.json has updates, apps read them from
// Deneb's releases on GitHub, tagged v<version> as the release workflow's
// tags are, with the notes of CHANGELOG.md, which MyGo would look for
// beside mygo.json, and check them with an Ed25519 public key. Until then
// they never look (the key waits for the user, design/plan/m6/client.md).
func TestUpdatesConfig(t *testing.T) {
	u := readAppConfig(t).Updates
	if u == nil {
		t.Log("no updates in mygo.json yet: a release's apps would not update, and deploy/release.sh refuses to publish one")
		return
	}
	if u.GitHub != "kittors/deneb" {
		t.Errorf("updates.github %q, want kittors/deneb", u.GitHub)
	}
	if u.TagPrefix != "" && u.TagPrefix != "v" {
		t.Errorf("updates.tagPrefix %q: the release workflow pushes v<version>", u.TagPrefix)
	}
	if u.Changelog != "../../CHANGELOG.md" {
		t.Errorf("updates.changelog %q, want ../../CHANGELOG.md", u.Changelog)
	}
	if k, err := base64.StdEncoding.DecodeString(u.PublicKey); err != nil || len(k) != 32 {
		t.Errorf("updates.publicKey is not mygo-update.pub's: %d bytes, %v", len(k), err)
	}
}

// releaseStub writes what deploy/package.sh does for version v, as a real
// build of 11-12 named it: the Windows app's files, its installer, its
// update and manifest, and MyGo's install script; for Linux, the .deb
// packages, and MyGo's archives, install script, PKGBUILD and manifests;
// and MyGo's checksums of all of it.
const releaseStub = `#!/bin/sh
set -eu
out=$(cd "$(dirname "$0")" && pwd)/out/0.2.0
w=$out/windows-amd64
mkdir -p "$w"
for f in Deneb.exe denebd.exe deneb-setup-0.2.0-amd64.exe deneb-0.2.0-windows-amd64.tar.gz update-windows-amd64.json install.ps1; do
	echo "$f" >"$w/$f"
done
for a in amd64 arm64; do
	l=$out/linux-$a
	mkdir -p "$l"
	for f in deneb_0.2.0_$a.deb denebd_0.2.0_$a.deb deneb-server_0.2.0_$a.deb deneb-0.2.0-linux-$a.tar.gz install.sh PKGBUILD update-linux-$a.json; do
		echo "$f" >"$l/$f"
	done
done
echo "0000  everything" >"$out/SHA256SUMS"
`

// TestReleaseChecks: deploy/release.sh stops before it builds anything,
// and says why, when a release would go up wrong: no updates in mygo.json,
// another repository's, a tag that is not the version, no key to sign
// with, no notes for the version. Right, it lists the Windows installer,
// its update and manifest, last, and the Linux packages, with checksums of
// those only: not the window's own files, nor MyGo's archives, scripts and
// manifests for Linux, whose window has no engine and no updates.
func TestReleaseChecks(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	script, err := os.ReadFile("../../deploy/release.sh")
	if err != nil {
		t.Fatal(err)
	}
	write := func(path, s string, mode os.FileMode) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(s), mode); err != nil {
			t.Fatal(err)
		}
	}
	tree := func(conf, changelog string) string {
		dir := t.TempDir()
		write(filepath.Join(dir, "deploy", "release.sh"), string(script), 0o755)
		write(filepath.Join(dir, "deploy", "package.sh"), releaseStub, 0o755)
		write(filepath.Join(dir, "cmd", "deneb", "mygo.json"), conf, 0o644)
		if changelog != "" {
			write(filepath.Join(dir, "CHANGELOG.md"), changelog, 0o644)
		}
		return dir
	}
	run := func(dir string, env ...string) (string, error) {
		cmd := exec.Command(sh, filepath.Join(dir, "deploy", "release.sh"), "-n", "windows", "linux")
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "GITHUB_") && !strings.HasPrefix(e, "MYGO_UPDATER_PRIVATE_KEY=") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		cmd.Env = append(cmd.Env, env...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	// As mygo.json is laid out: a field a line, which the script reads.
	good := `{
  "name": "Deneb",
  "version": "0.2.0",
  "updates": {
    "publicKey": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
    "github": "kittors/deneb",
    "changelog": "../../CHANGELOG.md"
  }
}`
	notes := "# 更新记录\n\n## 0.2.0\n\n新的一版。\n\n## 0.1.0\n\n第一版。\n"
	key := "MYGO_UPDATER_PRIVATE_KEY=not a real key"
	for _, tc := range []struct {
		name, conf, changelog string
		env                   []string
		want                  string
	}{
		{"no updates", "{\n  \"version\": \"0.2.0\"\n}", notes, []string{key}, "no updates in"},
		{"another repository's", strings.Replace(good, "kittors/deneb", "someone/else", 1), notes, []string{key}, "updates.github"},
		{"another tag", good, notes, []string{key, "GITHUB_REF_TYPE=tag", "GITHUB_REF_NAME=v0.1.9"}, "the tag v0.1.9 is not v0.2.0"},
		{"no key", good, notes, nil, "no MYGO_UPDATER_PRIVATE_KEY"},
		{"no notes for the version", good, "# 更新记录\n\n## 0.1.0\n\n第一版。\n", []string{key}, `no section "## 0.2.0"`},
		{"no CHANGELOG.md", good, "", []string{key}, `no section "## 0.2.0"`},
	} {
		dir := tree(tc.conf, tc.changelog)
		out, err := run(dir, tc.env...)
		if err == nil || !strings.Contains(out, tc.want) {
			t.Errorf("%s: %v, said %q, want %q", tc.name, err, out, tc.want)
		}
		if _, err := os.Stat(filepath.Join(dir, "deploy", "out")); err == nil {
			t.Errorf("%s: it built", tc.name)
		}
	}

	dir := tree(good, notes)
	out, err := run(dir, key, "GITHUB_REF_TYPE=tag", "GITHUB_REF_NAME=v0.2.0")
	if err != nil {
		t.Fatalf("a release right: %v\n%s", err, out)
	}
	var up []string
	sums := ""
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "  "); ok {
			up = append(up, filepath.Base(p))
			if filepath.Base(p) == "SHA256SUMS" {
				sums = p
			}
		}
	}
	// The files in any order (the shell's), then the checksums, then the
	// manifest.
	files := []string{
		"deneb-setup-0.2.0-amd64.exe", "deneb-0.2.0-windows-amd64.tar.gz",
		"deneb_0.2.0_amd64.deb", "deneb-server_0.2.0_amd64.deb", "denebd_0.2.0_amd64.deb",
		"deneb_0.2.0_arm64.deb", "deneb-server_0.2.0_arm64.deb", "denebd_0.2.0_arm64.deb",
	}
	n := len(up)
	if n != len(files)+2 || up[n-2] != "SHA256SUMS" || up[n-1] != "update-windows-amd64.json" || !sameSet(up[:n-2], files) {
		t.Errorf("what goes up: %q", up)
	}
	b, err := os.ReadFile(sums)
	if err != nil {
		t.Fatal(err)
	}
	var named []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if f := strings.Fields(line); len(f) == 2 && len(f[0]) == 64 {
			named = append(named, f[1])
		} else {
			t.Errorf("SHA256SUMS line %q", line)
		}
	}
	if !sameSet(named, append(files, "update-windows-amd64.json")) {
		t.Errorf("SHA256SUMS names %q", named)
	}
}

// sameSet reports whether a and b hold the same strings, once each.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		if seen[s] != 1 {
			return false
		}
		seen[s]--
	}
	return true
}
