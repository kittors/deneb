package names

import (
	"errors"
	"net/netip"
	"testing"
)

func table() *Table {
	return NewTable("deneb.internal", []Entry{
		{Device: "d1", Name: "mac", OwnerName: "kittors", FQDN: "mac.kittors.deneb.internal", Addr: netip.MustParseAddr("10.77.0.1")},
		{Device: "d2", Name: "box", OwnerName: "lin", FQDN: "box.lin.deneb.internal", Addr: netip.MustParseAddr("10.77.0.2")},
		{Device: "d3", Name: "box", OwnerName: "jie", FQDN: "box.jie.deneb.internal", Addr: netip.MustParseAddr("10.77.0.3")},
		{Device: "d4", Name: "nas", OwnerName: "jie", FQDN: "nas.jie.deneb.internal", Addr: netip.MustParseAddr("10.77.0.4")},
	})
}

func TestResolve(t *testing.T) {
	tb := table()
	for q, want := range map[string]string{
		"nas": "d4", "NAS.": "d4", "nas.jie": "d4", "nas.jie.deneb.internal": "d4", "nas.jie.deneb.internal.": "d4",
		"box.lin": "d2", "box.jie.deneb.internal": "d3", "10.77.0.2": "d2", "d3": "d3",
	} {
		e, err := tb.Resolve(q)
		if err != nil || string(e.Device) != want {
			t.Errorf("Resolve(%q) = %v, %v; want %s", q, e.Device, err, want)
		}
	}
	var amb *AmbiguousError
	if _, err := tb.Resolve("box"); !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Errorf("box should be ambiguous, got %v", err)
	}
	var nf *NotFoundError
	for _, q := range []string{"box.mia", "printer", "10.77.0.9", "a.b.c", "example.com", ""} {
		if _, err := tb.Resolve(q); !errors.As(err, &nf) {
			t.Errorf("Resolve(%q) should be not found, got %v", q, err)
		}
	}
	if !tb.IsDenebName("x.y.deneb.internal.") || tb.IsDenebName("deneb.internal.evil.com") {
		t.Error("IsDenebName")
	}
}
