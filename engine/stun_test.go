package engine

import (
	"net"
	"net/netip"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/kittors/deneb/engine/controlclient"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/proto"
	"tailscale.com/net/stun"
)

// TestSTUNServersFromTheMap: the engine asks the network map's STUN
// servers by the addresses the map gives, besides the relays, which here
// have no STUN at all, as where the relays' address is blocked. Their
// answer is the public address status shows, with who saw it, and goes
// into what the engine reports, so peers punch to it.
func TestSTUNServersFromTheMap(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	seen := netip.MustParseAddrPort("203.0.113.9:41641")
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if tx, err := stun.ParseBindingRequest(buf[:n]); err == nil {
				pc.WriteTo(stun.Response(tx, seen), from)
			}
		}
	}()
	at := pc.LocalAddr().(*net.UDPAddr).AddrPort()

	e, err := New(Options{StateDir: t.TempDir(), Mode: apitype.ModeUserspace, BindInterface: "none", Logf: quiet, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Start()
	nm := selfMap(e, 3)
	// The name is for the logs: only the address is used.
	nm.STUN = []proto.STUNServer{{HostName: "stun.example.com", Port: at.Port(), IPv4: at.Addr()}}
	e.applyFrame(controlclient.Frame{Version: 3, NetMap: nm})

	want := apitype.PublicAddr{Addr: seen.String(), Server: "stun.example.com:" + strconv.Itoa(int(at.Port()))}
	deadline := time.Now().Add(3 * time.Second)
	for {
		st := e.State()
		if slices.Contains(st.Engine.Public, want) && slices.Contains(st.Engine.Endpoints, seen.String()) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("status: public %+v, endpoints %v; want %+v", st.Engine.Public, st.Engine.Endpoints, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !slices.Contains(e.ms.Endpoints(), seen) {
		t.Fatalf("what the engine reports: %v", e.ms.Endpoints())
	}

	// A map without them: forgotten.
	nm = selfMap(e, 4)
	e.applyFrame(controlclient.Frame{Version: 4, NetMap: nm})
	if st := e.State(); len(st.Engine.Public) != 0 || slices.Contains(st.Engine.Endpoints, seen.String()) {
		t.Fatalf("after the map dropped them: public %+v, endpoints %v", st.Engine.Public, st.Engine.Endpoints)
	}
}
