package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Updates follow Deneb's releases (design/plan/m7/relay-install.md §5.3):
// the download site's relay folder holds relay.json, the latest release's
// manifest, which names a program for each platform, its size, SHA-256
// and signature. The signature is MyGo's: Ed25519, over the file's SHA-256
// digest, with the key that signs the window's updates; its public half
// is below (and in cmd/deneb/mygo.json, which a test holds them to).
//
//	{
//	  "version": "0.1.9",
//	  "sha256sums": "<SHA-256 of 0.1.9/SHA256SUMS, which the install command carries>",
//	  "files": {
//	    "linux-amd64": {"url": "0.1.9/deneb-relay-linux-amd64", "size": 12345678,
//	                    "sha256": "<hex>", "signature": "<base64>"},
//	    …
//	  }
//	}
//
// A url is relative to the manifest's. A program is taken only if it is
// signed, newer than this one, and, run with `version`, says it is the
// release the manifest names: an older release, signed but renamed, gets
// nowhere.
var updatePublicKey = "oIpKlr1FzjpMDb9aZiY3pT0cIL89a02CwLA2/sDQztA="

// manifestName is the latest release's manifest, in the feed's folder.
const manifestName = "relay.json"

type manifest struct {
	Version    string                  `json:"version"`
	SHA256Sums string                  `json:"sha256sums,omitempty"`
	Files      map[string]manifestFile `json:"files"`
}

type manifestFile struct {
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

const (
	maxManifest = 1 << 20
	maxProgram  = 128 << 20
)

// updateKey is the public key updates are checked with.
func updateKey() ed25519.PublicKey {
	b, _ := base64.StdEncoding.DecodeString(updatePublicKey)
	return ed25519.PublicKey(b)
}

// errUpdated: a newer release is in place of the program; the relay
// starts it.
var errUpdated = errors.New("updated")

// errNoFeed: this build does not know where releases are (a development
// build, or a release without the site's address).
var errNoFeed = errors.New("这个版本不知道去哪里取更新（开发版）")

var updateClient = &http.Client{Timeout: 10 * time.Minute}

// latest reads the latest release's manifest; base is its URL, which its
// files' URLs are relative to.
func latest(ctx context.Context) (*manifest, *url.URL, error) {
	f := feed()
	if f == "" {
		return nil, nil, errNoFeed
	}
	base, err := url.Parse(strings.TrimSuffix(f, "/") + "/" + manifestName)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	b, err := get(ctx, base.String(), maxManifest)
	if err != nil {
		return nil, nil, err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", manifestName, err)
	}
	if m.Version == "" {
		return nil, nil, fmt.Errorf("%s: no version", manifestName)
	}
	return &m, base, nil
}

func get(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", version())
	resp, err := updateClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", filepath.Base(req.URL.Path), resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: larger than %d bytes", filepath.Base(req.URL.Path), limit)
	}
	return b, nil
}

// verifyProgram: b is the file f names, whole and signed with key.
func verifyProgram(f manifestFile, b []byte, key ed25519.PublicKey) error {
	if int64(len(b)) != f.Size {
		return fmt.Errorf("下载的程序大小不对：%d 字节，清单说 %d", len(b), f.Size)
	}
	sum := sha256.Sum256(b)
	if f.SHA256 != "" && !strings.EqualFold(f.SHA256, hex.EncodeToString(sum[:])) {
		return errors.New("下载的程序校验值不对")
	}
	sig, err := base64.StdEncoding.DecodeString(f.Signature)
	if err != nil || !ed25519.Verify(key, sum[:], sig) {
		return errors.New("下载的程序没有 Deneb 的签名")
	}
	return nil
}

// fetchProgram downloads this platform's program of m, checks it, and
// writes it into dir, next to the one it is to replace. It returns the
// file's path.
func fetchProgram(ctx context.Context, m *manifest, base *url.URL, dir string) (string, error) {
	f, ok := m.Files[platform()]
	if !ok {
		return "", fmt.Errorf("%s 版没有 %s 的程序", m.Version, platform())
	}
	u, err := base.Parse(f.URL)
	if err != nil {
		return "", err
	}
	b, err := get(ctx, u.String(), maxProgram)
	if err != nil {
		return "", err
	}
	if err := verifyProgram(f, b, updateKey()); err != nil {
		return "", err
	}
	path := filepath.Join(dir, ".deneb-relay.new")
	if err := os.WriteFile(path, b, 0o755); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// checkRuns runs the program at path with `version`: it must run here,
// and say it is release want.
func checkRuns(ctx context.Context, path, want string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return fmt.Errorf("新的程序在这台机器上运行不了：%v", err)
	}
	if got := strings.TrimSpace(string(out)); got != want {
		return fmt.Errorf("下载的程序是 %s，不是清单说的 %s", got, want)
	}
	return nil
}

// replaceProgram puts the program at next in place of bin, at once,
// keeping bin as bin.prev to go back to by hand.
func replaceProgram(bin, next string) error {
	prev := bin + ".prev"
	os.Remove(prev)
	if err := os.Link(bin, prev); err != nil {
		// Not on a file system that links: copy it instead.
		if b, err := os.ReadFile(bin); err == nil {
			os.WriteFile(prev, b, 0o755)
		}
	}
	return os.Rename(next, bin)
}

// installedVersion is the release of the program at bin.
func installedVersion(ctx context.Context, bin string) string {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// updatedBin is the program autoUpdate put in place, which reexec starts.
var updatedBin string

// Automatic updates: the first look 10 minutes after the start, then
// every 6 hours, each a little early or late so that the relays of a
// release do not all ask at once.
const (
	updateFirst = 10 * time.Minute
	updateEvery = 6 * time.Hour
)

// autoUpdate looks for a newer release now and then and, once one is in
// place of the program, returns true: run then ends with errUpdated and
// the relay starts it. A development build, or one that cannot write
// where its program is (Docker), never updates.
func autoUpdate(ctx context.Context, logf func(string, ...any)) bool {
	if relayVersion == "" || feed() == "" {
		return false
	}
	bin, err := os.Executable()
	if err == nil {
		bin, err = filepath.EvalSymlinks(bin)
	}
	if err != nil {
		return false
	}
	wait := updateFirst
	for {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(jitter(wait)):
		}
		wait = updateEvery
		if !writable(filepath.Dir(bin)) {
			logf("update: cannot write %s; updates are off", filepath.Dir(bin))
			return false
		}
		m, base, err := latest(ctx)
		if err != nil {
			logf("update: %v", err)
			continue
		}
		if compareVersions(m.Version, relayVersion) <= 0 {
			continue
		}
		logf("update: %s → %s", relayVersion, m.Version)
		next, err := fetchProgram(ctx, m, base, filepath.Dir(bin))
		if err == nil {
			if err = checkRuns(ctx, next, m.Version); err == nil {
				err = replaceProgram(bin, next)
			}
		}
		if err != nil {
			os.Remove(filepath.Join(filepath.Dir(bin), ".deneb-relay.new"))
			logf("update: %v", err)
			continue
		}
		logf("update: %s in place; restarting", m.Version)
		updatedBin = bin
		return true
	}
}

// jitter is d, give or take a tenth.
func jitter(d time.Duration) time.Duration {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(d/5)+1))
	if err != nil {
		return d
	}
	return d - d/10 + time.Duration(n.Int64())
}

// writable: this process may make files in dir.
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}
