package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/types/key"
)

// The data directory keeps what must outlive a restart: the certificate,
// whose pin the devices trust; the DERP server's key; and who the relay
// is, with its secret. All of it is the relay's alone, mode 0600.

const (
	certFile  = "cert.pem"
	keyFile   = "key.pem"
	derpFile  = "derp.key"
	stateFile = "relay.json"
	// listenFile is the address it listens on, for -healthcheck.
	listenFile = "listen"
)

// state is who the relay is, from its registration.
type state struct {
	Relay   proto.RelayID     `json:"relay"`
	Name    string            `json:"name"`
	Source  proto.RelaySource `json:"source"`
	Secret  string            `json:"secret"`
	Control string            `json:"control"` // the server it registered with
}

func loadState(dir string) (*state, error) {
	b, err := os.ReadFile(filepath.Join(dir, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", stateFile, err)
	}
	if st.Relay == "" || st.Secret == "" {
		return nil, fmt.Errorf("%s: no relay or secret in it", stateFile)
	}
	return &st, nil
}

func saveState(dir string, st *state) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	return writeFile(dir, stateFile, b)
}

// writeFile writes a file the relay alone may read, whole or not at all.
func writeFile(dir, name string, b []byte) error {
	tmp := filepath.Join(dir, "."+name+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}

// loadCert reads the relay's certificate, or makes one the first time: a
// self-signed ECDSA one, good for ten years. Its key is what the pin is
// of, so it is kept as long as the data directory is; the names in it do
// not matter to devices, which check the pin and nothing else.
func loadCert(dir, host string) (tls.Certificate, string, error) {
	certPath, keyPath := filepath.Join(dir, certFile), filepath.Join(dir, keyFile)
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		leaf, err := x509.ParseCertificate(c.Certificate[0])
		if err != nil {
			return tls.Certificate{}, "", err
		}
		c.Leaf = leaf
		return c, proto.Fingerprint(leaf), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, "", fmt.Errorf("%s: %w", certFile, err)
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if a, err := netip.ParseAddr(host); err == nil {
		tmpl.IPAddresses = append(tmpl.IPAddresses, a.AsSlice())
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	kb, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := writeFile(dir, keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := writeFile(dir, certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return tls.Certificate{}, "", err
	}
	return loadCert(dir, host)
}

// loadDERPKey reads the DERP server's private key, or makes one. Clients
// learn the public half in the DERP handshake; keeping it keeps the
// relay the same server to them across restarts.
func loadDERPKey(dir string) (key.NodePrivate, error) {
	var k key.NodePrivate
	b, err := os.ReadFile(filepath.Join(dir, derpFile))
	if err == nil {
		return k, k.UnmarshalText([]byte(strings.TrimSpace(string(b))))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return k, err
	}
	k = key.NewNode()
	t, _ := k.MarshalText()
	return k, writeFile(dir, derpFile, append(t, '\n'))
}
