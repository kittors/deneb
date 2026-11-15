//go:build ignore

// Writes a relay release's SHA256SUMS, its signed manifest relay.json
// (cmd/deneb-relay/update.go) and latest, for the download site:
//
//	MYGO_UPDATER_PRIVATE_KEY=… go run deploy/relay/manifest.go [-n] <out> <version>
//
// <out>/<version>/ holds the programs, deneb-relay-linux-<arch>. Each is
// signed as MyGo signs the window's updates, Ed25519 over its SHA-256,
// with the key whose public half is in cmd/deneb/mygo.json (which the
// relay has built in). -n writes everything but the signatures, for a
// look: relays take no unsigned update.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type file struct {
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

type manifest struct {
	Version    string          `json:"version"`
	SHA256Sums string          `json:"sha256sums"`
	Files      map[string]file `json:"files"`
}

func main() {
	args := os.Args[1:]
	dry := len(args) > 0 && args[0] == "-n"
	if dry {
		args = args[1:]
	}
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run deploy/relay/manifest.go [-n] <out> <version>")
		os.Exit(2)
	}
	if err := run(args[0], args[1], dry); err != nil {
		fmt.Fprintln(os.Stderr, "manifest:", err)
		os.Exit(1)
	}
}

func run(out, version string, dry bool) error {
	var key ed25519.PrivateKey
	if !dry {
		var err error
		if key, err = signingKey(); err != nil {
			return err
		}
	}
	dir := filepath.Join(out, version)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	m := manifest{Version: version, Files: map[string]file{}}
	var sums []string
	for _, e := range ents {
		name := e.Name()
		plat, ok := strings.CutPrefix(name, "deneb-relay-")
		if !ok || e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		f := file{URL: version + "/" + name, Size: int64(len(b)), SHA256: hex.EncodeToString(sum[:])}
		if key != nil {
			f.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(key, sum[:]))
		}
		m.Files[plat] = f
		sums = append(sums, f.SHA256+"  "+name)
	}
	if len(m.Files) == 0 {
		return fmt.Errorf("no deneb-relay-* in %s", dir)
	}
	sort.Strings(sums)
	sumsText := strings.Join(sums, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sumsText), 0o644); err != nil {
		return err
	}
	s := sha256.Sum256([]byte(sumsText))
	m.SHA256Sums = hex.EncodeToString(s[:])
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "relay.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "latest"), []byte(version+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("relay %s: %d programs, SHA256SUMS %s\n", version, len(m.Files), m.SHA256Sums)
	if dry {
		fmt.Println("not signed (-n): relays will not take this release as an update")
	}
	fmt.Printf("install command: curl -fsSL https://<site>/relay | sh -s -- <join code> %s:%s\n", version, m.SHA256Sums)
	return nil
}

// signingKey is MYGO_UPDATER_PRIVATE_KEY, which must be the private half of
// cmd/deneb/mygo.json's updates.publicKey.
func signingKey() (ed25519.PrivateKey, error) {
	text := strings.TrimSpace(os.Getenv("MYGO_UPDATER_PRIVATE_KEY"))
	if text == "" {
		return nil, errors.New("no MYGO_UPDATER_PRIVATE_KEY: relays take only signed updates (or -n for a look)")
	}
	b, err := base64.StdEncoding.DecodeString(text)
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, errors.New("MYGO_UPDATER_PRIVATE_KEY is not an Ed25519 private key in base64")
	}
	key := ed25519.PrivateKey(b)
	conf, err := os.ReadFile("cmd/deneb/mygo.json")
	if err != nil {
		return nil, err
	}
	var c struct {
		Updates struct {
			PublicKey string `json:"publicKey"`
		} `json:"updates"`
	}
	if err := json.Unmarshal(conf, &c); err != nil {
		return nil, err
	}
	if base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)) != c.Updates.PublicKey {
		return nil, errors.New("MYGO_UPDATER_PRIVATE_KEY does not match updates.publicKey in cmd/deneb/mygo.json")
	}
	return key, nil
}
