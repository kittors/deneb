package proto

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestKeysRoundTrip(t *testing.T) {
	priv := NewMachineKey()
	pub := priv.Public()
	b, err := json.Marshal(struct {
		M MachinePublic
		N NodePublic
		D DiscoPublic
	}{pub, NodePublic{1, 2, 3}, DiscoPublic{4}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"machinekey:`) || !strings.Contains(string(b), `"nodekey:010203`) {
		t.Fatalf("keys marshal as %s", b)
	}
	var back struct {
		M MachinePublic
		N NodePublic
		D DiscoPublic
	}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.M != pub || back.N != (NodePublic{1, 2, 3}) || back.D != (DiscoPublic{4}) {
		t.Fatalf("round trip changed the keys: %+v", back)
	}
	if _, err := ParseNodePublic("discokey:" + strings.Repeat("00", 32)); err == nil {
		t.Fatal("a disco key parsed as a node key")
	}
	if _, err := ParseNodePublic("nodekey:00"); err == nil {
		t.Fatal("a short key parsed")
	}

	text, _ := priv.MarshalText()
	var priv2 MachinePrivate
	if err := priv2.UnmarshalText(text); err != nil || priv2 != priv {
		t.Fatalf("private key round trip: %v", err)
	}
	if strings.Contains(priv.String(), string(text[len("privmachinekey:"):])) {
		t.Fatal("String printed the private key")
	}
}

func TestSignChallenge(t *testing.T) {
	priv := NewMachineKey()
	challenge := []byte("some challenge")
	sig := priv.SignChallenge(challenge)
	if !priv.Public().VerifyChallenge(challenge, sig) {
		t.Fatal("signature does not verify")
	}
	if priv.Public().VerifyChallenge([]byte("another"), sig) {
		t.Fatal("signature verifies for another challenge")
	}
	if NewMachineKey().Public().VerifyChallenge(challenge, sig) {
		t.Fatal("signature verifies for another key")
	}
	if priv.Public().VerifyChallenge(challenge, sig[:10]) {
		t.Fatal("a short signature verifies")
	}
}

func TestPinnedTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	pin := Fingerprint(srv.Certificate())

	get := func(pin string) error {
		cfg, err := PinnedTLSConfig(pin)
		if err != nil {
			return err
		}
		c := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
		resp, err := c.Get(srv.URL)
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
	if err := get(pin); err != nil {
		t.Fatalf("pinned server refused: %v", err)
	}
	if err := get(strings.ToUpper(pin[:7]) + pin[7:]); err != nil {
		t.Fatalf("pin with upper-case prefix refused: %v", err)
	}
	wrong := "sha256:" + strings.Repeat("ab", 32)
	if err := get(wrong); err == nil || !strings.Contains(err.Error(), "not the pinned") {
		t.Fatalf("a server with another key passed: %v", err)
	}
	for _, bad := range []string{"", "sha256:12", "md5:" + strings.Repeat("ab", 32), "sha256:" + strings.Repeat("zz", 32)} {
		if _, err := PinnedTLSConfig(bad); err == nil {
			t.Errorf("pin %q accepted", bad)
		}
	}
}

func TestPortRange(t *testing.T) {
	for _, s := range []string{"tcp/80", "udp/27015-27030", "tcp/65535"} {
		p, err := ParsePortRange(s)
		if err != nil || p.String() != s {
			t.Errorf("%q: got %v, %v", s, p, err)
		}
	}
	for _, s := range []string{"tcp/0", "icmp/1", "tcp/90-80", "tcp", "tcp/x", "tcp/70000"} {
		if _, err := ParsePortRange(s); err == nil {
			t.Errorf("%q parsed", s)
		}
	}
	svc := Service{Kind: ServiceScreen, Ports: []PortRange{Port(UDP, 3389), Port(TCP, 3389), Port(TCP, 3389)}}
	if got := svc.Key(); got != "tcp/3389,udp/3389" {
		t.Errorf("key %q", got)
	}
	if !svc.Allows(UDP, 3389) || svc.Allows(TCP, 3390) {
		t.Error("Allows is wrong")
	}
	if svc.DisplayName() != "远程桌面" {
		t.Errorf("display name %q", svc.DisplayName())
	}
	b, _ := json.Marshal(svc)
	if string(b) != `{"kind":"screen","ports":["udp/3389","tcp/3389","tcp/3389"]}` {
		t.Errorf("service marshals as %s", b)
	}
	if err := (Service{Kind: ServiceWeb}).Validate(); err == nil {
		t.Error("a service without ports is valid")
	}
}

func TestParseInvite(t *testing.T) {
	const tok = "ABCDEFGH23456789"
	for _, s := range []string{
		tok,
		" " + tok + "\n",
		"https://103.42.31.155:47443/i/" + tok,
		"http://127.0.0.1:47080/i/" + tok,
		"deneb://join/" + tok,
		JoinLink(tok),
		InviteURL("https://x:1/", tok),
	} {
		got, err := ParseInvite(s)
		if err != nil || got != tok {
			t.Errorf("%q: got %q, %v", s, got, err)
		}
	}
	for _, s := range []string{"", "short", "https://x/login?c=" + tok, "deneb://open/" + tok, "ftp://x/i/" + tok, tok + "!"} {
		if got, err := ParseInvite(s); err == nil {
			t.Errorf("%q parsed as %q", s, got)
		}
	}
}

func TestMakeLabel(t *testing.T) {
	for in, want := range map[string]string{
		"kittors-MacBook-Pro.local": "kittors-macbook-pro",
		"DESKTOP-7H2K9":             "desktop-7h2k9",
		"box.lin.example.com":       "box",
		"Jie's NAS":                 "jies-nas",
		"阿杰的电脑":                     "x",
		"  --hi__there-- ":          "hi-there",
		"mia.chen":                  "mia",
		"测试box2":                    "box2",
	} {
		if got := MakeLabel(in, "x"); got != want {
			t.Errorf("MakeLabel(%q) = %q, want %q", in, got, want)
		}
	}
	if got := MakeLabel(strings.Repeat("a", 80), "x"); len(got) != 63 {
		t.Errorf("long label has %d letters", len(got))
	}
	if !ValidLabel("box-2") || ValidLabel("Box") || ValidLabel("") || ValidLabel("-a") {
		t.Error("ValidLabel is wrong")
	}
	if FQDN("nas", "jie") != "nas.jie.deneb.internal" {
		t.Error("FQDN")
	}
}

func TestScopeID(t *testing.T) {
	s := LANScope("l_abc")
	if s != "lan/l_abc" {
		t.Fatalf("scope %q", s)
	}
	if id, ok := s.LAN(); !ok || id != "l_abc" {
		t.Fatalf("LAN() = %q, %v", id, ok)
	}
	if _, ok := ScopeID("share/x").LAN(); ok {
		t.Fatal("a share is a LAN")
	}
	if _, ok := ScopeID("lan/").LAN(); ok {
		t.Fatal("an empty LAN id is a LAN")
	}
}

func TestNetMapJSON(t *testing.T) {
	nm := NetMap{
		Version: 7,
		Network: Network{
			IPv4:   netip.MustParsePrefix("10.67.0.0/16"),
			IPv6:   netip.MustParsePrefix("fd12:3456:789a::/48"),
			DNS:    []netip.Addr{netip.MustParseAddr("10.67.0.53")},
			Domain: Domain,
		},
		Self: Node{ID: "d_1", Addrs: []netip.Prefix{netip.MustParsePrefix("10.67.1.1/32")}},
		Peers: []Peer{{
			Node:   Node{ID: "d_2", Endpoints: []netip.AddrPort{netip.MustParseAddrPort("1.2.3.4:41641")}},
			Scopes: []ScopeID{LANScope("l_1")},
		}},
		Relays: []Relay{{ID: "r_hk", Name: "香港", HostName: "derp.example", IPv4: netip.MustParseAddr("198.51.100.4"), DERPPort: 33443, STUNPort: 3478}},
	}
	b, err := json.Marshal(nm)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"ipv4":"10.67.0.0/16"`, `"endpoints":["1.2.3.4:41641"]`, `"scopes":["lan/l_1"]`, `"ipv4":"198.51.100.4"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("netmap JSON lacks %s: %s", want, b)
		}
	}
	if strings.Contains(string(b), `"ipv6":""`) {
		t.Errorf("an empty address is written out: %s", b)
	}
	var back NetMap
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Peers[0].Endpoints[0] != nm.Peers[0].Endpoints[0] || back.Network.IPv4 != nm.Network.IPv4 || back.Network.IPv6 != nm.Network.IPv6 || back.Relays[0].IPv4 != nm.Relays[0].IPv4 {
		t.Fatalf("round trip changed the map")
	}
}
