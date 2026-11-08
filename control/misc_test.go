package control

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"testing"

	"github.com/kittors/deneb/proto"
)

func TestNewNetwork(t *testing.T) {
	cgnat := netip.MustParsePrefix("100.64.0.0/10")
	clash := netip.MustParsePrefix("198.18.0.0/15")
	seen := map[netip.Prefix]bool{}
	for range 300 {
		n, err := newNetwork("", "")
		if err != nil {
			t.Fatal(err)
		}
		a := n.IPv4.Addr().As4()
		if n.IPv4.Bits() != 16 || a[0] != 10 || !slices.Contains(secondOctets, a[1]) || cgnat.Overlaps(n.IPv4) || clash.Overlaps(n.IPv4) {
			t.Fatalf("picked %s", n.IPv4)
		}
		if n.IPv6.Bits() != 48 || n.IPv6.Addr().As16()[0] != 0xfd {
			t.Fatalf("picked %s", n.IPv6)
		}
		if len(n.DNS) != 2 || !n.IPv4.Contains(n.DNS[0]) || !n.IPv6.Contains(n.DNS[1]) || n.DNS[0] != hostAddr(n.IPv4, 0, 53) || n.Domain != proto.Domain {
			t.Fatalf("network %+v", n)
		}
		seen[n.IPv4] = true
	}
	if len(seen) < 10 {
		t.Fatalf("only %d different /16s in 300 picks", len(seen))
	}
	for _, o := range []byte{0, 1, 10, 32, 42, 64, 66, 88, 96, 100, 128, 168, 244} {
		if slices.Contains(secondOctets, o) {
			t.Errorf("10.%d.0.0/16 may be picked", o)
		}
	}

	n, err := newNetwork("10.86.0.0/16", "fd00:1:2::/48")
	if err != nil || n.IPv4.String() != "10.86.0.0/16" || n.IPv6.String() != "fd00:1:2::/48" {
		t.Fatalf("fixed network: %+v, %v", n, err)
	}
	if got := v6For(n.IPv6, netip.MustParseAddr("10.86.1.5")); got.String() != "fd00:1:2::a56:105" {
		t.Fatalf("v6For = %s", got)
	}
	for _, bad := range [][2]string{{"100.64.0.0/16", ""}, {"10.86.0.0/24", ""}, {"192.168.0.0/16", ""}, {"", "2001:db8::/48"}, {"", "fd00::/64"}} {
		if _, err := newNetwork(bad[0], bad[1]); err == nil {
			t.Errorf("newNetwork(%q, %q) took it", bad[0], bad[1])
		}
	}
}

func TestCert(t *testing.T) {
	dir := t.TempDir()
	c1, pin1, err := LoadOrCreateCert(dir, []string{"103.42.31.155", "127.0.0.1", "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(c1.Leaf.IPAddresses, func(ip net.IP) bool { return ip.Equal(net.ParseIP("103.42.31.155")) }) {
		t.Fatalf("certificate names %v", c1.Leaf.IPAddresses)
	}
	if lifetime := c1.Leaf.NotAfter.Sub(c1.Leaf.NotBefore); lifetime > 398*24*3600e9 {
		t.Fatalf("certificate lives %v, more than browsers take", lifetime)
	}
	_, pin2, _ := LoadOrCreateCert(dir, []string{"103.42.31.155"})
	c3, pin3, _ := LoadOrCreateCert(dir, []string{"103.42.31.155", "deneb.example.com"})
	if pin1 != pin2 || pin1 != pin3 {
		t.Fatalf("the pin changed: %s, %s, %s", pin1, pin2, pin3)
	}
	if !slices.Contains(c3.Leaf.DNSNames, "deneb.example.com") || c3.Leaf.SerialNumber.Cmp(c1.Leaf.SerialNumber) == 0 {
		t.Fatal("a new host did not remake the certificate")
	}

	// A client pinning it gets through; that is all the trust there is.
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{c3}}
	ts.StartTLS()
	defer ts.Close()
	cfg, err := proto.PinnedTLSConfig(pin1)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}).Get(ts.URL)
	if err != nil {
		t.Fatalf("pinned client: %v", err)
	}
	resp.Body.Close()
}

func TestParseRelayFlag(t *testing.T) {
	r, err := ParseRelayFlag("id=local,name=本地,host=derper.deneb.test,ipv4=172.30.0.3,derp=443,stun=3478")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "local" || r.Name != "本地" || r.HostName != "derper.deneb.test" || r.IPv4.String() != "172.30.0.3" || r.DERPPort != 443 || r.STUNPort != 3478 || r.Source != proto.RelayServer {
		t.Fatalf("parsed %+v", r)
	}
	for _, bad := range []string{"name=x,host=h", "name=x,derp=443", "name=x,host=h,derp=99999", "name=x,host=h,derp=1,color=red", "junk"} {
		if _, err := ParseRelayFlag(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestInitialAndKind(t *testing.T) {
	for in, want := range map[string]string{"阿杰": "杰", "kittors": "K", "mia": "M", "Élodie": "É", "李小明": "明", "": "?"} {
		if got := initialOf(in); got != want {
			t.Errorf("initialOf(%q) = %q, want %q", in, got, want)
		}
	}
	for _, c := range []struct {
		os   proto.OS
		host string
		want proto.DeviceKind
	}{
		{proto.MacOS, "kittors-MacBook-Pro.local", proto.KindLaptop},
		{proto.MacOS, "Mac-mini", proto.KindMini},
		{proto.MacOS, "studio-iMac", proto.KindDesktop},
		{proto.Windows, "DESKTOP-1", proto.KindDesktop},
		{proto.Linux, "hk-155", proto.KindServer},
		{proto.OtherOS, "x", proto.KindOther},
	} {
		if got := guessKind(c.os, c.host); got != c.want {
			t.Errorf("guessKind(%s, %s) = %s, want %s", c.os, c.host, got, c.want)
		}
	}
}

func TestSeedChanges(t *testing.T) {
	dir := t.TempDir()
	seed := DevSeed()
	open := func() *Server {
		s, err := New(Config{DataDir: dir, PublicURL: "http://x", Seed: seed, Logf: func(string, ...any) {}})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	w := s.state.Load().w
	if len(w.people) != 4 || !slices.Equal(w.defaults, []proto.RelayID{"hk"}) || w.relays["hk"].Owner != w.byEmail["kittors@deneb.test"].ID {
		t.Fatalf("seeded world: %d people, defaults %v, relay %+v", len(w.people), w.defaults, w.relays["hk"])
	}
	s.Close()

	// A second relay first in line, the first one kept but no longer a
	// default, a renamed account: all from the seed, at the next start.
	off := false
	hk := HKSpareRelay()
	hk.Default = &off
	local, _ := ParseRelayFlag("id=local,name=本地,host=derper.test,derp=443")
	seed.Relays = []SeedRelay{local, hk}
	seed.Accounts[1].DisplayName = "杰哥"
	seed.Accounts[1].Initial = ""
	s = open()
	defer s.Close()
	w = s.state.Load().w
	if !slices.Equal(w.defaults, []proto.RelayID{"local"}) || w.relays["hk"] == nil {
		t.Fatalf("defaults %v, relays %v", w.defaults, w.relays)
	}
	if p := w.byEmail["jie@deneb.test"]; p.DisplayName != "杰哥" || p.Initial != "哥" || p.Name != "jie" {
		t.Fatalf("renamed account: %+v", p.Person)
	}
	v := s.Version()
	s.Close()
	s = open()
	if s.Version() != v {
		t.Fatal("the same seed again moved the version")
	}
}
