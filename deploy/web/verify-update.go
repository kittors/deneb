//go:build ignore

// Checks a signed update manifest the way installed apps do: every archive
// and delta it names has the size it says, and an Ed25519 signature of its
// SHA-256 digest made with the key whose public half is in
// cmd/deneb/mygo.json (updates.publicKey). publish-downloads.sh runs it on
// the files before they go up, and on the site's copies after.
//
//	go run deploy/web/verify-update.go <manifest.json | https://…/update-<os>-<arch>.json> [dir]
//
// With dir, the files are read from it by the base name of their URLs (a
// build's output folder); without, they are downloaded from their URLs.
// It prints base names only, never a URL.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type signed struct {
	From      string `json:"from,omitempty"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Signature string `json:"signature"`
}

type manifest struct {
	Version string `json:"version"`
	signed
	Deltas []signed `json:"deltas,omitempty"`
}

var client = &http.Client{Timeout: 10 * time.Minute}

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: go run deploy/web/verify-update.go <manifest | https URL> [dir]")
		os.Exit(2)
	}
	if err := run(os.Args[1], strings.Join(os.Args[2:], "")); err != nil {
		fmt.Fprintln(os.Stderr, "verify-update:", err)
		os.Exit(1)
	}
}

func run(where, dir string) error {
	key, err := publicKey("cmd/deneb/mygo.json")
	if err != nil {
		return err
	}
	b, err := read(where)
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("%s: %w", path.Base(where), err)
	}
	if m.Version == "" || m.URL == "" {
		return fmt.Errorf("%s: no version or url", path.Base(where))
	}
	for _, s := range append([]signed{m.signed}, m.Deltas...) {
		if err := check(key, s, dir); err != nil {
			return err
		}
	}
	fmt.Printf("ok %s: version %s, %d file(s) signed\n", path.Base(where), m.Version, 1+len(m.Deltas))
	return nil
}

// check verifies one archive or delta.
func check(key ed25519.PublicKey, s signed, dir string) error {
	name := path.Base(s.URL)
	where := s.URL
	if dir != "" {
		where = filepath.Join(dir, name)
	} else if !strings.HasPrefix(s.URL, "https://") {
		return fmt.Errorf("%s: not an https URL", name)
	}
	b, err := read(where)
	if err != nil {
		return err
	}
	if int64(len(b)) != s.Size {
		return fmt.Errorf("%s: %d bytes, the manifest says %d", name, len(b), s.Size)
	}
	sig, err := base64.StdEncoding.DecodeString(s.Signature)
	sum := sha256.Sum256(b)
	if err != nil || !ed25519.Verify(key, sum[:], sig) {
		return fmt.Errorf("%s: the signature does not match the key of mygo.json", name)
	}
	fmt.Printf("ok %s: %d bytes, signed\n", name, len(b))
	return nil
}

func publicKey(conf string) (ed25519.PublicKey, error) {
	b, err := os.ReadFile(conf)
	if err != nil {
		return nil, err
	}
	var c struct {
		Updates struct {
			PublicKey string `json:"publicKey"`
		} `json:"updates"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", conf, err)
	}
	k, err := base64.StdEncoding.DecodeString(c.Updates.PublicKey)
	if err != nil || len(k) != ed25519.PublicKeySize {
		return nil, errors.New(conf + ": no Ed25519 updates.publicKey")
	}
	return ed25519.PublicKey(k), nil
}

func read(where string) ([]byte, error) {
	if !strings.HasPrefix(where, "https://") {
		return os.ReadFile(where)
	}
	resp, err := client.Get(where)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path.Base(where), errors.Unwrap(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", path.Base(where), resp.Status)
	}
	return io.ReadAll(resp.Body)
}
