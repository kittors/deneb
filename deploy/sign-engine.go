//go:build ignore

// Command sign-engine signs a denebd for the system service to take it in
// without a password (design/plan/m7/engine-self-update.md §1): Ed25519
// over the file's SHA-256, as MyGo signs the window's updates, with the
// same key, written beside the file as <file>.sig. The key comes from
// MYGO_UPDATER_PRIVATE_KEY only, and must be the private half of
// cmd/deneb/mygo.json's updates.publicKey; it is never printed.
//
//	MYGO_UPDATER_PRIVATE_KEY=… go run deploy/sign-engine.go <denebd> <version>
//
// Run from the repository's root (deploy/package.sh does).
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
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: MYGO_UPDATER_PRIVATE_KEY=… go run deploy/sign-engine.go <denebd> <version>")
		os.Exit(2)
	}
	if err := sign(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "sign-engine:", err)
		os.Exit(1)
	}
}

func sign(file, version string) error {
	key, err := signingKey()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	sig := struct {
		Version   string `json:"version"`
		SHA256    string `json:"sha256"`
		Signature string `json:"signature"`
	}{version, hex.EncodeToString(sum[:]), base64.StdEncoding.EncodeToString(ed25519.Sign(key, sum[:]))}
	js, err := json.MarshalIndent(sig, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(file+".sig", append(js, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("signed %s %s\n", filepath.Base(file), version)
	return nil
}

// signingKey is MYGO_UPDATER_PRIVATE_KEY, which must be the private half of
// cmd/deneb/mygo.json's updates.publicKey (as deploy/relay/manifest.go's).
func signingKey() (ed25519.PrivateKey, error) {
	text := strings.TrimSpace(os.Getenv("MYGO_UPDATER_PRIVATE_KEY"))
	if text == "" {
		return nil, errors.New("no MYGO_UPDATER_PRIVATE_KEY")
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
