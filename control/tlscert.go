package control

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kittors/deneb/proto"
)

// The server's certificate is self-signed and clients pin its key
// (implementation.md §0.1): no CA, no domain, nothing on the host's nginx.
// The key is made once and kept; the certificate is remade with the same
// key whenever the hosts it must name change or it nears its end, so the
// pin clients ship never changes. Losing the key changes the pin: back up
// <data>/tls.

// certLifetime is how long a certificate is made for. The server remakes
// it when less than certRenew is left.
const (
	certLifetime = 397 * 24 * time.Hour
	certRenew    = 30 * 24 * time.Hour
)

// LoadOrCreateCert returns the certificate for hosts (IP addresses or DNS
// names) and its pin, from dir/tls.
func LoadOrCreateCert(dir string, hosts []string) (tls.Certificate, string, error) {
	tdir := filepath.Join(dir, "tls")
	if err := os.MkdirAll(tdir, 0o700); err != nil {
		return tls.Certificate{}, "", err
	}
	keyPath, certPath := filepath.Join(tdir, "key.pem"), filepath.Join(tdir, "cert.pem")

	key, err := loadKey(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err == nil {
			der, _ := x509.MarshalECPrivateKey(key)
			err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600)
		}
	}
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("tls key: %w", err)
	}

	leaf, err := loadCert(certPath)
	if err != nil || !certFits(leaf, key, hosts) {
		der, err := makeCert(key, hosts)
		if err != nil {
			return tls.Certificate{}, "", err
		}
		if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
			return tls.Certificate{}, "", err
		}
		if leaf, err = x509.ParseCertificate(der); err != nil {
			return tls.Certificate{}, "", err
		}
	}
	pin := proto.Fingerprint(leaf)
	if err := os.WriteFile(filepath.Join(tdir, "pin.txt"), []byte(pin+"\n"), 0o644); err != nil {
		return tls.Certificate{}, "", err
	}
	return tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key, Leaf: leaf}, pin, nil
}

func loadKey(path string) (*ecdsa.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, fmt.Errorf("%s: no PEM block", path)
	}
	return x509.ParseECPrivateKey(blk.Bytes)
}

func loadCert(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, fmt.Errorf("%s: no PEM block", path)
	}
	return x509.ParseCertificate(blk.Bytes)
}

// certFits reports whether cert is for key, names every host, and has
// more than certRenew left.
func certFits(cert *x509.Certificate, key *ecdsa.PrivateKey, hosts []string) bool {
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) || time.Until(cert.NotAfter) < certRenew {
		return false
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			if !slices.ContainsFunc(cert.IPAddresses, ip.Equal) {
				return false
			}
		} else if !slices.Contains(cert.DNSNames, h) {
			return false
		}
	}
	return true
}

func makeCert(key *ecdsa.PrivateKey, hosts []string) ([]byte, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Deneb control server"},
		NotBefore:    now.Add(-time.Hour),
		// Browsers hold even a certificate they were told to accept to the
		// public rules, which cap it at 398 days; renewal reuses the key,
		// so the pin stays.
		NotAfter:    now.Add(certLifetime),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if h != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	return x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
}
