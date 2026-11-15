package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/kittors/deneb/proto"
)

// fakeRouter answers PCP and NAT-PMP on a port of 127.0.0.1 (pmpPort is
// pointed there): pcp false makes it a router that speaks NAT-PMP only.
func fakeRouter(t *testing.T, pcp bool, ext netip.Addr) {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	old := pmpPort
	pmpPort = c.LocalAddr().(*net.UDPAddr).Port
	t.Cleanup(func() { pmpPort = old })
	go func() {
		buf := make([]byte, 1100)
		for {
			n, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			req := buf[:n]
			var res []byte
			switch {
			case req[0] == 2 && !pcp:
				res = []byte{0, req[1] | 0x80, 0, 1, 0, 0, 0, 0} // UNSUPP_VERSION, in NAT-PMP's words
			case req[0] == 2 && n == 60:
				res = make([]byte, 60)
				res[0], res[1] = 2, 0x81
				copy(res[4:8], req[4:8])     // the lifetime asked for
				copy(res[24:42], req[24:42]) // nonce, protocol, internal port
				copy(res[42:44], req[42:44]) // the external port asked for
				a := ext.As16()
				copy(res[44:60], a[:])
			case req[0] == 0 && req[1] == 0:
				res = make([]byte, 12)
				res[1] = 128
				a := ext.As4()
				copy(res[8:12], a[:])
			case req[0] == 0 && (req[1] == 1 || req[1] == 2):
				res = make([]byte, 16)
				res[1] = 128 + req[1]
				copy(res[8:10], req[4:6])   // internal
				copy(res[10:12], req[6:8])  // external: as asked
				copy(res[12:16], req[8:12]) // lifetime
			}
			if res != nil {
				c.WriteToUDP(res, from)
			}
		}
	}()
}

var lan = netip.MustParseAddr("192.168.1.20")

// TestPortMapPCP and TestPortMapNATPMP: a router asked by PCP, and one
// that speaks NAT-PMP only, forward both ports.
func TestPortMapPCP(t *testing.T) {
	fakeRouter(t, true, netip.MustParseAddr("203.0.113.50"))
	m, err := mapPorts(context.Background(), netip.MustParseAddr("127.0.0.1"), lan, 47445, 47478)
	if err != nil || m.method != "pcp" || m.ext.String() != "203.0.113.50" || m.tcp != 47445 || m.udp != 47478 || m.life != mapLease {
		t.Fatalf("%+v, %v", m, err)
	}
	if r := m.report(); r.Method != "pcp" || r.DERPPort != 47445 || r.External != m.ext {
		t.Errorf("report %+v", r)
	}
}

func TestPortMapNATPMP(t *testing.T) {
	fakeRouter(t, false, netip.MustParseAddr("203.0.113.51"))
	m, err := mapPorts(context.Background(), netip.MustParseAddr("127.0.0.1"), lan, 47445, 47478)
	if err != nil || m.method != "natpmp" || m.ext.String() != "203.0.113.51" || m.tcp != 47445 || m.udp != 47478 {
		t.Fatalf("%+v, %v", m, err)
	}
}

// TestPortMapUPnP: a UPnP router, found by its description, maps TCP on
// the next port when the first is taken, and UDP for good when it takes
// no leases that run out.
func TestPortMapUPnP(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/desc.xml":
			fmt.Fprint(w, `<?xml version="1.0"?><root><device><deviceType>urn:schemas-upnp-org:device:InternetGatewayDevice:1</deviceType>
<deviceList><device><deviceList><device><serviceList><service>
<serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType><controlURL>/ctl/ipc</controlURL>
</service></serviceList></device></deviceList></device></deviceList></device></root>`)
		case "/ctl/ipc":
			b, _ := io.ReadAll(r.Body)
			body := string(b)
			action := r.Header.Get("SOAPAction")
			mu.Lock()
			calls = append(calls, action[strings.Index(action, "#")+1:len(action)-1]+" "+between(body, "NewProtocol")+" "+between(body, "NewExternalPort")+" "+between(body, "NewLeaseDuration"))
			mu.Unlock()
			fault := func(code int) {
				w.WriteHeader(500)
				fmt.Fprintf(w, `<s:Envelope><s:Body><s:Fault><detail><UPnPError><errorCode>%d</errorCode></UPnPError></detail></s:Fault></s:Body></s:Envelope>`, code)
			}
			switch {
			case strings.Contains(action, "AddPortMapping") && between(body, "NewExternalPort") == "47445" && between(body, "NewProtocol") == "TCP":
				fault(718) // taken
			case strings.Contains(action, "AddPortMapping") && between(body, "NewProtocol") == "UDP" && between(body, "NewLeaseDuration") != "0":
				fault(725) // only permanent leases
			case strings.Contains(action, "GetExternalIPAddress"):
				fmt.Fprint(w, `<s:Envelope><s:Body><u:GetExternalIPAddressResponse><NewExternalIPAddress>203.0.113.60</NewExternalIPAddress></u:GetExternalIPAddressResponse></s:Body></s:Envelope>`)
			default:
				fmt.Fprint(w, `<s:Envelope><s:Body/></s:Envelope>`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	g, err := igdAt(context.Background(), srv.URL+"/desc.xml")
	if err != nil || g.control != srv.URL+"/ctl/ipc" {
		t.Fatalf("%+v, %v", g, err)
	}
	m, err := g.mapBoth(context.Background(), lan, 47445, 47478)
	if err != nil || m.method != "upnp" || m.tcp != 47446 || m.udp != 47478 || m.ext.String() != "203.0.113.60" {
		t.Fatalf("%+v, %v (calls %q)", m, err, calls)
	}
	want := []string{"AddPortMapping TCP 47445 3600", "AddPortMapping TCP 47446 3600", "AddPortMapping UDP 47478 3600", "AddPortMapping UDP 47478 0", "GetExternalIPAddress   "}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls %q, want %q", calls, want)
	}
	m.unmap(context.Background(), netip.Addr{}, lan, 47445, 47478)
	if last := calls[len(calls)-1]; last != "DeletePortMapping UDP 47478 " {
		t.Errorf("unmapped with %q", calls[len(calls)-2:])
	}
}

func between(s, tag string) string {
	_, rest, ok := strings.Cut(s, "<"+tag+">")
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(rest, "</"+tag+">")
	return v
}

// TestPortMapReported: what the mapping got goes into the next sync, the
// router's address and ports in place of the relay's own; a failure is
// reported with why, and the relay's own ports stay.
func TestPortMapReported(t *testing.T) {
	r := &relay{logf: t.Logf, syncNow: make(chan struct{}, 1), reported: proto.RelayInfo{DERPPort: 47445, STUNPort: 47478, Pin: "sha256:x"}}
	pm := &portMapper{logf: t.Logf, tcpLocal: 47445, udpLocal: 47478,
		lan: func() (netip.Addr, netip.Addr, bool) { return lan, netip.MustParseAddr("192.168.1.1"), true },
		mapPorts: func(context.Context, netip.Addr, netip.Addr, uint16, uint16) (*portMap, error) {
			return &portMap{method: "upnp", ext: netip.MustParseAddr("203.0.113.60"), tcp: 50000, udp: 50001, life: mapLease}, nil
		}}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // once, and no renewal
	r.mapPortsFor(ctx, pm, dir)
	if r.info == nil || r.info.IPv4.String() != "203.0.113.60" || r.info.DERPPort != 50000 || r.info.STUNPort != 50001 || r.info.PortMap.Method != "upnp" || r.info.Pin != "sha256:x" {
		t.Fatalf("info %+v", r.info)
	}
	if rep := readPortMap(dir); rep == nil || rep.DERPPort != 50000 {
		t.Errorf("portmap.json %+v", rep)
	}
	pm.mapPorts = func(context.Context, netip.Addr, netip.Addr, uint16, uint16) (*portMap, error) {
		return nil, fmt.Errorf("路由器没有回应")
	}
	r.mapPortsFor(ctx, pm, dir)
	if r.info.DERPPort != 47445 || r.info.PortMap.Error != "路由器没有回应" || r.info.PortMap.Method != "" {
		t.Errorf("after a failure: %+v %+v", r.info, r.info.PortMap)
	}
}
