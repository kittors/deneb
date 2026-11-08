package proto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// A device has three keys (implementation.md §2.6.1). Their private halves
// never leave it; the control server only ever sees the public ones.
//
//   - The machine key proves to the control server that a request comes from
//     this device. It is Ed25519, so the standard library signs with it.
//   - The node key is the device's WireGuard key.
//   - The disco key signs the probes engines send one another to find a
//     direct path.
//
// Node and disco keys are Curve25519 and print the way Tailscale's do
// ("nodekey:<hex>", "discokey:<hex>"), so an engine built on Tailscale's key
// package converts them with key.NodePublicFromRaw32(mem.B(k[:])) and back
// with proto.NodePublic(k.Raw32()).

// MachinePublic is the public half of a device's machine key.
type MachinePublic [32]byte

// MachinePrivate is the private half of a machine key: its Ed25519 seed.
type MachinePrivate [32]byte

// NodePublic is a device's WireGuard public key.
type NodePublic [32]byte

// DiscoPublic is the public key a device's path probes are sealed with.
type DiscoPublic [32]byte

const (
	machinePublicPrefix  = "machinekey:"
	machinePrivatePrefix = "privmachinekey:"
	nodePublicPrefix     = "nodekey:"
	discoPublicPrefix    = "discokey:"
)

// NewMachineKey makes a machine key.
func NewMachineKey() MachinePrivate {
	var k MachinePrivate
	if _, err := rand.Read(k[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported systems
	}
	return k
}

// Public returns the public half of the key.
func (k MachinePrivate) Public() MachinePublic {
	var p MachinePublic
	copy(p[:], ed25519.NewKeyFromSeed(k[:]).Public().(ed25519.PublicKey))
	return p
}

// SignChallenge answers an auth challenge (AuthChallenge.Challenge).
func (k MachinePrivate) SignChallenge(challenge []byte) []byte {
	return ed25519.Sign(ed25519.NewKeyFromSeed(k[:]), authMessage(challenge))
}

// VerifyChallenge reports whether sig answers challenge for this key.
func (k MachinePublic) VerifyChallenge(challenge, sig []byte) bool {
	return len(sig) == ed25519.SignatureSize &&
		ed25519.Verify(ed25519.PublicKey(k[:]), authMessage(challenge), sig)
}

// authMessage is what gets signed. The prefix keeps a machine key's
// signature over a challenge from meaning anything anywhere else.
func authMessage(challenge []byte) []byte {
	return append([]byte("deneb device auth v1\n"), challenge...)
}

func (k MachinePublic) IsZero() bool { return k == MachinePublic{} }
func (k NodePublic) IsZero() bool    { return k == NodePublic{} }
func (k DiscoPublic) IsZero() bool   { return k == DiscoPublic{} }

func (k MachinePublic) String() string { return machinePublicPrefix + hex.EncodeToString(k[:]) }
func (k NodePublic) String() string    { return nodePublicPrefix + hex.EncodeToString(k[:]) }
func (k DiscoPublic) String() string   { return discoPublicPrefix + hex.EncodeToString(k[:]) }

func (k MachinePublic) MarshalText() ([]byte, error) { return []byte(k.String()), nil }
func (k NodePublic) MarshalText() ([]byte, error)    { return []byte(k.String()), nil }
func (k DiscoPublic) MarshalText() ([]byte, error)   { return []byte(k.String()), nil }

func (k *MachinePublic) UnmarshalText(b []byte) error {
	return parseKey(machinePublicPrefix, b, (*[32]byte)(k))
}
func (k *NodePublic) UnmarshalText(b []byte) error {
	return parseKey(nodePublicPrefix, b, (*[32]byte)(k))
}
func (k *DiscoPublic) UnmarshalText(b []byte) error {
	return parseKey(discoPublicPrefix, b, (*[32]byte)(k))
}

// MarshalText writes the private key for the engine's key file. It is the
// one key type that must never be sent anywhere.
func (k MachinePrivate) MarshalText() ([]byte, error) {
	return []byte(machinePrivatePrefix + hex.EncodeToString(k[:])), nil
}
func (k *MachinePrivate) UnmarshalText(b []byte) error {
	return parseKey(machinePrivatePrefix, b, (*[32]byte)(k))
}

// String does not print the key, so a log line never carries it.
func (k MachinePrivate) String() string { return "privmachinekey:…" }

func parseKey(prefix string, b []byte, out *[32]byte) error {
	s, ok := strings.CutPrefix(string(b), prefix)
	if !ok {
		return fmt.Errorf("key %.24q: want prefix %q", b, prefix)
	}
	if hex.DecodedLen(len(s)) != 32 {
		return fmt.Errorf("key %.24q: want 32 bytes of hex", b)
	}
	if _, err := hex.Decode(out[:], []byte(s)); err != nil {
		return fmt.Errorf("key %.24q: %w", b, err)
	}
	return nil
}

// ParseNodePublic reads "nodekey:<hex>".
func ParseNodePublic(s string) (NodePublic, error) {
	var k NodePublic
	return k, k.UnmarshalText([]byte(s))
}

// ParseDiscoPublic reads "discokey:<hex>".
func ParseDiscoPublic(s string) (DiscoPublic, error) {
	var k DiscoPublic
	return k, k.UnmarshalText([]byte(s))
}

// ParseMachinePublic reads "machinekey:<hex>".
func ParseMachinePublic(s string) (MachinePublic, error) {
	var k MachinePublic
	return k, k.UnmarshalText([]byte(s))
}

// The control server's certificate is self-signed, and clients ship its pin
// instead of trusting a CA (implementation.md §0.1).

const pinPrefix = "sha256:"

// Fingerprint is a certificate's pin: "sha256:" and the hex SHA-256 of its
// SubjectPublicKeyInfo. It pins the key rather than the certificate, so the
// server can renew the certificate with the same key and no client breaks.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return pinPrefix + hex.EncodeToString(sum[:])
}

// PinnedTLSConfig returns a TLS client config that trusts the server only if
// its certificate carries the pinned key. It checks no CA and no name: the
// pin is the whole of the trust, which is what lets a server reached by its
// bare IP address use a certificate nobody signed.
func PinnedTLSConfig(pin string) (*tls.Config, error) {
	want, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(pin)), pinPrefix)
	if !ok || len(want) != 2*sha256.Size {
		return nil, fmt.Errorf("pin %q: want sha256:<64 hex digits>", pin)
	}
	if _, err := hex.DecodeString(want); err != nil {
		return nil, fmt.Errorf("pin %q: %w", pin, err)
	}
	want = pinPrefix + want
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		// The usual chain check would reject a self-signed certificate;
		// VerifyConnection below does the check that matters instead, and
		// crypto/tls runs it whether or not this is set.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("control server sent no certificate")
			}
			got := Fingerprint(cs.PeerCertificates[0])
			if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
				return fmt.Errorf("control server key %s is not the pinned %s", got, want)
			}
			return nil
		},
	}, nil
}
