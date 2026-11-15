package probe

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/net/stunserver"
)

// relayCert is a self-signed certificate, as a relay makes one, and its pin.
func relayCert(t *testing.T) (tls.Certificate, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "deneb-relay"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, proto.Fingerprint(leaf)
}

// listenTLS serves TLS with cert on a port of this machine until the test
// ends: a handshake is all it does.
func listenTLS(t *testing.T, cert tls.Certificate) netip.AddrPort {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				c.(*tls.Conn).Handshake()
				c.Close()
			}()
		}
	}()
	return netip.MustParseAddrPort(ln.Addr().String())
}

// TestProbe: a relay that answers TLS with its certificate and STUN is ok;
// another certificate, something not TLS, nothing listening, and silence
// each say so.
func TestProbe(t *testing.T) {
	ctx := context.Background()
	cert, pin := relayCert(t)
	_, otherPin := relayCert(t)
	tcp := listenTLS(t, cert)
	stunCtx, stop := context.WithCancel(ctx)
	defer stop()
	s := stunserver.New(stunCtx)
	if err := s.Listen("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	go s.Serve()
	udp := netip.MustParseAddrPort(s.LocalAddr().String())

	p := Relay(ctx, proto.ProbeTarget{Family: "ipv4", Addr: tcp.Addr(), DERPPort: tcp.Port(), STUNPort: udp.Port(), Pin: pin})
	if p.TCP != proto.RelayProbeOK || p.UDP != proto.RelayProbeOK || p.Family != "ipv4" || p.Addr != tcp.Addr() {
		t.Fatalf("a relay that works: %+v", p)
	}
	if got := TLS(ctx, tcp, otherPin, "relay.example.com"); got != proto.RelayProbePin {
		t.Fatalf("another certificate: %s", got)
	}

	// Something that answers, not with TLS: it closes at once.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("SSH-2.0-OpenSSH\r\n"))
			c.Close()
		}
	}()
	notTLS := netip.MustParseAddrPort(ln.Addr().String())
	if got := TLS(ctx, notTLS, pin, ""); got != proto.RelayProbeTLS {
		t.Fatalf("not TLS: %s", got)
	}
	ln.Close()
	if got := TLS(ctx, notTLS, pin, ""); got != proto.RelayProbeRefused {
		t.Fatalf("nothing listening: %s", got)
	}

	// Silence: a port that takes the connection and says nothing.
	quiet, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer quiet.Close()
	go func() {
		for {
			c, err := quiet.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	short, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if got := TLS(short, netip.MustParseAddrPort(quiet.Addr().String()), pin, ""); got != proto.RelayProbeTimeout {
		t.Fatalf("silence: %s", got)
	}

	// A STUN port with nothing on it: refused (an ICMP port unreachable) on
	// this machine, or no answer.
	pc, _ := net.ListenPacket("udp", "127.0.0.1:0")
	closed := netip.MustParseAddrPort(pc.LocalAddr().String())
	pc.Close()
	short2, cancel2 := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel2()
	if got := STUN(short2, closed); got != proto.RelayProbeRefused && got != proto.RelayProbeTimeout {
		t.Fatalf("no STUN: %s", got)
	}
	if p := Relay(ctx, proto.ProbeTarget{Family: "ipv4", Addr: tcp.Addr(), DERPPort: tcp.Port(), Pin: pin}); p.UDP != proto.RelayProbeNone {
		t.Fatalf("no STUN port: %+v", p)
	}
}

// TestPublic: only addresses on the Internet are public.
func TestPublic(t *testing.T) {
	for a, want := range map[string]bool{
		"203.0.113.7": true, "8.8.8.8": true, "2001:db8::1": true, "::ffff:8.8.8.8": true,
		"10.1.2.3": false, "192.168.1.5": false, "172.16.0.1": false, "100.64.1.1": false, "127.0.0.1": false,
		"169.254.1.1": false, "::1": false, "fe80::1": false, "fd00::1": false, "0.0.0.0": false, "224.0.0.1": false,
	} {
		if got := Public(netip.MustParseAddr(a)); got != want {
			t.Errorf("%s: %v", a, got)
		}
	}
}
