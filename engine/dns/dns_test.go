package dns

import (
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	"github.com/kittors/deneb/engine/filter"
	"github.com/kittors/deneb/engine/names"
	"github.com/kittors/deneb/proto"
)

func query(t *testing.T, r *Responder, name string, typ dnsmessage.Type) dnsmessage.Message {
	t.Helper()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: 7, RecursionDesired: true})
	b.StartQuestions()
	b.Question(dnsmessage.Question{Name: dnsmessage.MustNewName(name), Type: typ, Class: dnsmessage.ClassINET})
	msg, _ := b.Finish()
	client := netip.MustParseAddr("10.77.0.1")
	reply, ok := r.Answer(UDP4(client, 5353, r.Addr, 53, msg))
	if !ok || reply == nil {
		t.Fatalf("%s: no reply", name)
	}
	h, parsed := filter.Parse(reply)
	if !parsed || h.Src != r.Addr || h.Dst != client || h.SPort != 53 || h.DPort != 5353 {
		t.Fatalf("%s: bad reply header %+v", name, h)
	}
	var m dnsmessage.Message
	if err := m.Unpack(reply[28:]); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAnswer(t *testing.T) {
	r := New(netip.MustParseAddr("10.77.0.53"), func(string, ...any) {})
	r.SetTable(names.NewTable("deneb.internal", []names.Entry{
		{Device: "d4", Name: "nas", OwnerName: "jie", FQDN: "nas.jie.deneb.internal", Addr: netip.MustParseAddr("10.77.0.4")},
		{Device: "d2", Name: "box", OwnerName: "lin", FQDN: "box.lin.deneb.internal", Addr: netip.MustParseAddr("10.77.0.2")},
		{Device: "d3", Name: "box", OwnerName: "jie", FQDN: "box.jie.deneb.internal", Addr: netip.MustParseAddr("10.77.0.3")},
	}))
	m := query(t, r, "nas.jie.deneb.internal.", dnsmessage.TypeA)
	if m.RCode != dnsmessage.RCodeSuccess || len(m.Answers) != 1 || m.Answers[0].Body.(*dnsmessage.AResource).A != [4]byte{10, 77, 0, 4} {
		t.Fatalf("A nas.jie: %+v", m)
	}
	if m := query(t, r, "nas.jie.deneb.internal.", dnsmessage.TypeAAAA); m.RCode != dnsmessage.RCodeSuccess || len(m.Answers) != 0 {
		t.Fatalf("AAAA should be empty NOERROR: %+v", m)
	}
	if m := query(t, r, "box.deneb.internal.", dnsmessage.TypeA); m.RCode != dnsmessage.RCodeNameError {
		t.Fatalf("ambiguous short name should be NXDOMAIN: %v", m.RCode)
	}
	// A negative answer carries an SOA with a 1 s minimum, so the system
	// resolver forgets it within a second (a device put back into a LAN).
	for _, q := range []struct {
		name string
		typ  dnsmessage.Type
	}{{"gone.jie.deneb.internal.", dnsmessage.TypeA}, {"nas.jie.deneb.internal.", dnsmessage.TypeAAAA}} {
		m := query(t, r, q.name, q.typ)
		if len(m.Authorities) != 1 {
			t.Fatalf("%s %v: no SOA in the negative answer: %+v", q.name, q.typ, m)
		}
		soa, ok := m.Authorities[0].Body.(*dnsmessage.SOAResource)
		if !ok || soa.MinTTL != 1 || m.Authorities[0].Header.TTL != 1 || m.Authorities[0].Header.Name.String() != "deneb.internal." {
			t.Fatalf("%s %v: SOA %+v", q.name, q.typ, m.Authorities[0])
		}
	}
	if m := query(t, r, "www.google.com.", dnsmessage.TypeA); m.RCode != dnsmessage.RCodeRefused {
		t.Fatalf("other names must be refused, not forwarded: %v", m.RCode)
	}
	if _, ok := r.Answer(UDP4(netip.MustParseAddr("10.77.0.1"), 5353, netip.MustParseAddr("10.77.0.4"), 53, []byte{1})); ok {
		t.Fatal("packets to other addresses are not the responder's")
	}
}

// TestRevived: a name answered "no such name" that a new table has again is
// reported once, so the engine can flush the system's caches.
func TestRevived(t *testing.T) {
	r := New(netip.MustParseAddr("10.77.0.53"), t.Logf)
	got := make(chan []string, 2)
	r.OnRevived = func(n []string) { got <- n }
	r.SetTable(names.NewTable("", nil))
	if m := query(t, r, "nas.jie.deneb.internal.", dnsmessage.TypeA); m.RCode != dnsmessage.RCodeNameError {
		t.Fatalf("before: %v", m.RCode)
	}
	back := names.NewTable("", []names.Entry{{Device: "d1", Name: "nas", OwnerName: "jie", FQDN: "nas.jie.deneb.internal",
		Addr: netip.MustParseAddr("10.77.0.3"), Scopes: []proto.ScopeID{"lan/weekend"}}})
	r.SetTable(back)
	select {
	case n := <-got:
		if len(n) != 1 || n[0] != "nas.jie.deneb.internal" {
			t.Fatalf("revived %v", n)
		}
	case <-time.After(time.Second):
		t.Fatal("not reported")
	}
	r.SetTable(back)
	select {
	case n := <-got:
		t.Fatalf("reported again: %v", n)
	case <-time.After(100 * time.Millisecond):
	}
}
