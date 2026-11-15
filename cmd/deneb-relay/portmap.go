package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/proto"
)

// A machine at home sits behind its router, which lets nothing in unless
// told to (design/plan/m7/relay-install.md §5.2). The relay asks it to
// forward its two ports, by PCP, NAT-PMP or UPnP, whichever the router
// speaks, and keeps the mappings alive while it runs.
//
// tailscale.com/net/portmapper, which the design named, maps UDP only
// (WireGuard's port); devices reach a relay over TCP. These are the three
// protocols' few messages, for TCP and UDP both, with no new dependency.

// pmpPort is where a router answers PCP and NAT-PMP; a test's fake
// router listens elsewhere.
var pmpPort = 5351

// portMap is the relay's mappings, as the router made them.
type portMap struct {
	method string     // "pcp", "natpmp", "upnp"
	ext    netip.Addr // the router's address on the Internet
	tcp    uint16     // the router's ports, forwarded to the relay's
	udp    uint16
	life   time.Duration
	upnp   *igd // for UPnP: where to renew and remove them
}

// report is the mappings as the control server takes them.
func (m *portMap) report() *proto.RelayPortMap {
	return &proto.RelayPortMap{Method: m.method, External: m.ext, DERPPort: m.tcp, STUNPort: m.udp}
}

// mapLease is how long a mapping is asked for; it is renewed at half.
const mapLease = time.Hour

// mapPorts asks the router at gw to forward its ports tcp and udp to the
// same ports of self, this machine's address on its LAN: PCP, then
// NAT-PMP (one round trip each, to the router), then UPnP. The error says
// why none worked.
func mapPorts(ctx context.Context, gw, self netip.Addr, tcp, udp uint16) (*portMap, error) {
	var errs []string
	if m, err := pmpMapBoth(ctx, gw, self, tcp, udp, pcpMap, "pcp"); err == nil {
		return m, nil
	} else {
		errs = append(errs, "PCP: "+err.Error())
	}
	if m, err := pmpMapBoth(ctx, gw, self, tcp, udp, natpmpMap, "natpmp"); err == nil {
		return m, nil
	} else {
		errs = append(errs, "NAT-PMP: "+err.Error())
	}
	g, err := discoverIGD(ctx, gw)
	if err == nil {
		var m *portMap
		if m, err = g.mapBoth(ctx, self, tcp, udp); err == nil {
			return m, nil
		}
	}
	errs = append(errs, "UPnP: "+err.Error())
	return nil, errors.New(strings.Join(errs, "；"))
}

// mapFunc maps one port by PCP or NAT-PMP.
type mapFunc func(ctx context.Context, gw, self netip.Addr, tcp bool, internal, suggested uint16, life time.Duration) (ext uint16, extIP netip.Addr, granted time.Duration, err error)

func pmpMapBoth(ctx context.Context, gw, self netip.Addr, tcp, udp uint16, f mapFunc, method string) (*portMap, error) {
	t, ip, life, err := f(ctx, gw, self, true, tcp, tcp, mapLease)
	if err != nil {
		return nil, err
	}
	u, _, _, err := f(ctx, gw, self, false, udp, udp, mapLease)
	if err != nil {
		return nil, err
	}
	return &portMap{method: method, ext: ip, tcp: t, udp: u, life: life}, nil
}

// unmap takes the mappings back (a relay that stops).
func (m *portMap) unmap(ctx context.Context, gw, self netip.Addr, tcpLocal, udpLocal uint16) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	switch m.method {
	case "pcp":
		pcpMap(ctx, gw, self, true, tcpLocal, m.tcp, 0)
		pcpMap(ctx, gw, self, false, udpLocal, m.udp, 0)
	case "natpmp":
		natpmpMap(ctx, gw, self, true, tcpLocal, 0, 0)
		natpmpMap(ctx, gw, self, false, udpLocal, 0, 0)
	case "upnp":
		if m.upnp != nil {
			m.upnp.remove(ctx, "TCP", m.tcp)
			m.upnp.remove(ctx, "UDP", m.udp)
		}
	}
}

// exchange sends req to the router's PCP/NAT-PMP port and returns the
// first answer that passes ok, resending as RFC 6886 says, a few times.
func exchange(ctx context.Context, gw netip.Addr, req []byte, ok func([]byte) bool) ([]byte, error) {
	c, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(gw, uint16(pmpPort))))
	if err != nil {
		return nil, err
	}
	defer c.Close()
	buf := make([]byte, 1100)
	wait := 250 * time.Millisecond
	for try := 0; try < 3; try++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if _, err := c.Write(req); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(wait)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		c.SetReadDeadline(deadline)
		for {
			n, err := c.Read(buf)
			if err != nil {
				break // the wait ran out: send again
			}
			if ok(buf[:n]) {
				return buf[:n], nil
			}
		}
		wait *= 2
	}
	return nil, errors.New("路由器没有回应")
}

// natpmpMap maps a port by NAT-PMP (RFC 6886); life 0 takes it back.
func natpmpMap(ctx context.Context, gw, _ netip.Addr, tcp bool, internal, suggested uint16, life time.Duration) (uint16, netip.Addr, time.Duration, error) {
	// The router's address on the Internet first: opcode 0.
	res, err := exchange(ctx, gw, []byte{0, 0}, func(b []byte) bool { return len(b) >= 12 && b[0] == 0 && b[1] == 128 })
	if err != nil {
		return 0, netip.Addr{}, 0, err
	}
	if rc := binary.BigEndian.Uint16(res[2:4]); rc != 0 {
		return 0, netip.Addr{}, 0, fmt.Errorf("路由器拒绝了（%d）", rc)
	}
	extIP := netip.AddrFrom4([4]byte(res[8:12]))
	op := byte(1) // UDP
	if tcp {
		op = 2
	}
	req := make([]byte, 12)
	req[1] = op
	binary.BigEndian.PutUint16(req[4:], internal)
	binary.BigEndian.PutUint16(req[6:], suggested)
	binary.BigEndian.PutUint32(req[8:], uint32(life/time.Second))
	res, err = exchange(ctx, gw, req, func(b []byte) bool {
		return len(b) >= 16 && b[0] == 0 && b[1] == 128+op && binary.BigEndian.Uint16(b[8:10]) == internal
	})
	if err != nil {
		return 0, netip.Addr{}, 0, err
	}
	if rc := binary.BigEndian.Uint16(res[2:4]); rc != 0 {
		return 0, netip.Addr{}, 0, fmt.Errorf("路由器拒绝了（%d）", rc)
	}
	return binary.BigEndian.Uint16(res[10:12]), extIP, time.Duration(binary.BigEndian.Uint32(res[12:16])) * time.Second, nil
}

// pcpMap maps a port by PCP's MAP (RFC 6887); life 0 takes it back.
func pcpMap(ctx context.Context, gw, self netip.Addr, tcp bool, internal, suggested uint16, life time.Duration) (uint16, netip.Addr, time.Duration, error) {
	req := make([]byte, 60)
	req[0], req[1] = 2, 1 // version 2, MAP request
	binary.BigEndian.PutUint32(req[4:], uint32(life/time.Second))
	client := netip.AddrFrom16(self.As16()).As16() // IPv4-mapped
	copy(req[8:24], client[:])
	nonce := req[24:36]
	rand.Read(nonce)
	req[36] = 17
	if tcp {
		req[36] = 6
	}
	binary.BigEndian.PutUint16(req[40:], internal)
	binary.BigEndian.PutUint16(req[42:], suggested)
	any4 := netip.AddrFrom16(netip.IPv4Unspecified().As16()).As16() // no external address asked for
	copy(req[44:60], any4[:])
	res, err := exchange(ctx, gw, req, func(b []byte) bool {
		// A router that speaks NAT-PMP only answers version 0, "unsupported".
		return len(b) >= 4 && b[0] == 0 || len(b) >= 60 && b[0] == 2 && b[1] == 0x81 && bytes.Equal(b[24:36], nonce)
	})
	if err != nil {
		return 0, netip.Addr{}, 0, err
	}
	if res[0] == 0 {
		return 0, netip.Addr{}, 0, errors.New("路由器只会 NAT-PMP")
	}
	if rc := res[3]; rc != 0 {
		return 0, netip.Addr{}, 0, fmt.Errorf("路由器拒绝了（%d）", rc)
	}
	ext := netip.AddrFrom16([16]byte(res[44:60])).Unmap()
	return binary.BigEndian.Uint16(res[42:44]), ext, time.Duration(binary.BigEndian.Uint32(res[4:8])) * time.Second, nil
}

// igd is a router's UPnP Internet Gateway Device: the service that maps
// ports, and where to ask it.
type igd struct {
	control string // the service's control URL
	service string // its type: urn:schemas-upnp-org:service:WANIPConnection:1
	hc      *http.Client
}

var ssdpAddr = netip.MustParseAddrPort("239.255.255.250:1900")

// discoverIGD finds the router's gateway device by SSDP, the router at gw
// first.
func discoverIGD(ctx context.Context, gw netip.Addr) (*igd, error) {
	c, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	for _, st := range []string{"urn:schemas-upnp-org:device:InternetGatewayDevice:2", "urn:schemas-upnp-org:device:InternetGatewayDevice:1"} {
		msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: " + st + "\r\n\r\n"
		c.WriteToUDPAddrPort([]byte(msg), ssdpAddr)
	}
	deadline := time.Now().Add(2500 * time.Millisecond)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	c.SetReadDeadline(deadline)
	var locations []string
	buf := make([]byte, 4096)
	for {
		n, from, err := c.ReadFromUDPAddrPort(buf)
		if err != nil {
			break
		}
		resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(buf[:n])), nil)
		if err != nil {
			continue
		}
		loc := resp.Header.Get("Location")
		resp.Body.Close()
		if loc == "" {
			continue
		}
		if from.Addr().Unmap() == gw {
			locations = append([]string{loc}, locations...)
		} else {
			locations = append(locations, loc)
		}
	}
	if len(locations) == 0 {
		return nil, errors.New("路由器没有打开 UPnP")
	}
	var lastErr error
	for _, loc := range locations {
		g, err := igdAt(ctx, loc)
		if err == nil {
			return g, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// igdAt reads the device description at location and finds its
// port-mapping service.
func igdAt(ctx context.Context, location string) (*igd, error) {
	hc := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	req, err := http.NewRequestWithContext(ctx, "GET", location, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var desc struct {
		URLBase string    `xml:"URLBase"`
		Device  igdDevice `xml:"device"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&desc); err != nil {
		return nil, fmt.Errorf("路由器的 UPnP 描述看不懂：%w", err)
	}
	base, err := url.Parse(location)
	if err != nil {
		return nil, err
	}
	if desc.URLBase != "" {
		if b, err := url.Parse(desc.URLBase); err == nil {
			base = b
		}
	}
	var find func(d igdDevice) *igd
	find = func(d igdDevice) *igd {
		for _, s := range d.Services {
			if strings.Contains(s.Type, ":WANIPConnection:") || strings.Contains(s.Type, ":WANPPPConnection:") {
				if u, err := base.Parse(s.ControlURL); err == nil {
					return &igd{control: u.String(), service: s.Type, hc: hc}
				}
			}
		}
		for _, sub := range d.Devices {
			if g := find(sub); g != nil {
				return g
			}
		}
		return nil
	}
	if g := find(desc.Device); g != nil {
		return g, nil
	}
	return nil, errors.New("路由器的 UPnP 不能映射端口")
}

type igdDevice struct {
	Services []struct {
		Type       string `xml:"serviceType"`
		ControlURL string `xml:"controlURL"`
	} `xml:"serviceList>service"`
	Devices []igdDevice `xml:"deviceList>device"`
}

// upnpFault is a UPnP error (UPnPError's errorCode).
type upnpFault int

func (f upnpFault) Error() string { return "UPnP 错误 " + strconv.Itoa(int(f)) }

// call makes a SOAP call of the service, and returns the answer's values.
func (g *igd) call(ctx context.Context, action string, args [][2]string) (map[string]string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:%s xmlns:u="%s">`, action, g.service)
	for _, a := range args {
		fmt.Fprintf(&b, "<%s>", a[0])
		xml.EscapeText(&b, []byte(a[1]))
		fmt.Fprintf(&b, "</%s>", a[0])
	}
	fmt.Fprintf(&b, "</u:%s></s:Body></s:Envelope>", action)
	req, err := http.NewRequestWithContext(ctx, "POST", g.control, strings.NewReader(b.String()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+g.service+"#"+action+`"`)
	resp, err := g.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	vals := map[string]string{}
	d := xml.NewDecoder(bytes.NewReader(body))
	var cur string
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			cur = t.Name.Local
		case xml.CharData:
			if cur != "" {
				vals[cur] += strings.TrimSpace(string(t))
			}
		case xml.EndElement:
			cur = ""
		}
	}
	if resp.StatusCode != http.StatusOK {
		if code, err := strconv.Atoi(vals["errorCode"]); err == nil {
			return nil, upnpFault(code)
		}
		return nil, fmt.Errorf("UPnP：%s", resp.Status)
	}
	return vals, nil
}

// add maps external of the router to self:internal; the lease in
// seconds, 0 for one that does not run out.
func (g *igd) add(ctx context.Context, protocol string, external, internal uint16, self netip.Addr, lease int) error {
	_, err := g.call(ctx, "AddPortMapping", [][2]string{
		{"NewRemoteHost", ""}, {"NewExternalPort", strconv.Itoa(int(external))}, {"NewProtocol", protocol},
		{"NewInternalPort", strconv.Itoa(int(internal))}, {"NewInternalClient", self.String()}, {"NewEnabled", "1"},
		{"NewPortMappingDescription", "deneb-relay"}, {"NewLeaseDuration", strconv.Itoa(lease)},
	})
	return err
}

func (g *igd) remove(ctx context.Context, protocol string, external uint16) error {
	_, err := g.call(ctx, "DeletePortMapping", [][2]string{{"NewRemoteHost", ""}, {"NewExternalPort", strconv.Itoa(int(external))}, {"NewProtocol", protocol}})
	return err
}

// mapOne maps a port, the same one outside if the router lets it, else
// one of the next few; a router that takes only leases that never run
// out gets one of those.
func (g *igd) mapOne(ctx context.Context, protocol string, internal uint16, self netip.Addr) (uint16, int, error) {
	lease := int(mapLease / time.Second)
	for ext := internal; ext < internal+10; ext++ {
		err := g.add(ctx, protocol, ext, internal, self, lease)
		if f, ok := err.(upnpFault); ok && f == 725 { // OnlyPermanentLeasesSupported
			lease = 0
			err = g.add(ctx, protocol, ext, internal, self, lease)
		}
		if f, ok := err.(upnpFault); ok && f == 718 { // ConflictInMappingEntry: taken by another
			continue
		}
		return ext, lease, err
	}
	return 0, 0, errors.New("路由器上的端口都被占了")
}

func (g *igd) mapBoth(ctx context.Context, self netip.Addr, tcp, udp uint16) (*portMap, error) {
	t, lease, err := g.mapOne(ctx, "TCP", tcp, self)
	if err != nil {
		return nil, err
	}
	u, _, err := g.mapOne(ctx, "UDP", udp, self)
	if err != nil {
		g.remove(ctx, "TCP", t)
		return nil, err
	}
	m := &portMap{method: "upnp", tcp: t, udp: u, life: time.Duration(lease) * time.Second, upnp: g}
	if vals, err := g.call(ctx, "GetExternalIPAddress", nil); err == nil {
		m.ext, _ = netip.ParseAddr(vals["NewExternalIPAddress"])
	}
	return m, nil
}

// lanAddr is this machine's private IPv4 address on the interface of the
// default route, and the router there: where mappings are asked for.
func lanAddr() (self, gw netip.Addr, ok bool) {
	gw, iface := defaultGateway()
	if !gw.IsValid() {
		return
	}
	for _, a := range ifaceAddrs() {
		ip := a.addr.Unmap()
		if a.iface == iface && ip.Is4() && ip.IsPrivate() {
			return ip, gw, true
		}
	}
	return
}

// portMapper keeps the relay's mappings: renewed at half their life, and
// taken back when the relay stops.
type portMapper struct {
	logf     func(string, ...any)
	tcpLocal uint16
	udpLocal uint16
	mapPorts func(ctx context.Context, gw, self netip.Addr, tcp, udp uint16) (*portMap, error)
	lan      func() (self, gw netip.Addr, ok bool) // lanAddr, unless a test's

	mu       sync.Mutex
	cur      *portMap
	self, gw netip.Addr
	lastErr  string
}

// try asks for the mappings once, and returns what to report.
func (p *portMapper) try(ctx context.Context) *proto.RelayPortMap {
	lan := p.lan
	if lan == nil {
		lan = lanAddr
	}
	self, gw, ok := lan()
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	m, err := p.mapPorts(ctx, gw, self, p.tcpLocal, p.udpLocal)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.self, p.gw = self, gw
	if err != nil {
		if err.Error() != p.lastErr {
			p.logf("port mapping: %v", err)
			p.lastErr = err.Error()
		}
		p.cur = nil
		return &proto.RelayPortMap{Error: err.Error()}
	}
	if p.cur == nil || *p.cur != *m {
		p.logf("port mapping (%s): %s, TCP %d → %d, UDP %d → %d, for %s", m.method, m.ext, m.tcp, p.tcpLocal, m.udp, p.udpLocal, m.life)
	}
	p.cur, p.lastErr = m, ""
	return m.report()
}

// external is the router's public address and ports, if mapped.
func (p *portMapper) external() (netip.Addr, uint16, uint16, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cur == nil || !publicAddr(p.cur.ext) {
		return netip.Addr{}, 0, 0, false
	}
	return p.cur.ext, p.cur.tcp, p.cur.udp, true
}

// portMapFile is the last report, in the data directory: install and
// status say what came of it.
const portMapFile = "portmap.json"

func writePortMap(dir string, rep *proto.RelayPortMap) {
	if rep == nil {
		os.Remove(filepath.Join(dir, portMapFile))
		return
	}
	b, _ := json.Marshal(rep)
	writeFile(dir, portMapFile, b)
}

func readPortMap(dir string) *proto.RelayPortMap {
	b, err := os.ReadFile(filepath.Join(dir, portMapFile))
	if err != nil {
		return nil
	}
	var rep proto.RelayPortMap
	if json.Unmarshal(b, &rep) != nil {
		return nil
	}
	return &rep
}

// mapPortsFor runs the relay's port mapping until ctx ends: once now,
// then kept, each change in the info its next sync carries.
func (r *relay) mapPortsFor(ctx context.Context, pm *portMapper, dir string) {
	apply := func(rep *proto.RelayPortMap) {
		writePortMap(dir, rep)
		r.mu.Lock()
		next := r.reported
		next.PortMap = rep
		next.IPv4, next.DERPPort, next.STUNPort = netip.Addr{}, pm.tcpLocal, pm.udpLocal
		if ip, t, u, ok := pm.external(); ok {
			next.IPv4, next.DERPPort, next.STUNPort = ip, t, u
		}
		old := r.reported
		changed := next.IPv4 != old.IPv4 || next.DERPPort != old.DERPPort || next.STUNPort != old.STUNPort ||
			(next.PortMap == nil) != (old.PortMap == nil) || next.PortMap != nil && *next.PortMap != *old.PortMap
		if changed {
			r.reported = next
			info := next
			r.info = &info
		}
		r.mu.Unlock()
		if changed {
			r.nudge()
		}
	}
	apply(pm.try(ctx))
	pm.keep(ctx, apply)
}

// keep renews the mappings until ctx ends, then takes them back. changed
// gets each new report.
func (p *portMapper) keep(ctx context.Context, changed func(*proto.RelayPortMap)) {
	for {
		p.mu.Lock()
		every := 5 * time.Minute // a failure is tried again
		if p.cur != nil && p.cur.life > 0 {
			every = p.cur.life / 2
		} else if p.cur != nil {
			every = 30 * time.Minute
		}
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			p.mu.Lock()
			m, self, gw := p.cur, p.self, p.gw
			p.mu.Unlock()
			if m != nil {
				m.unmap(context.Background(), gw, self, p.tcpLocal, p.udpLocal)
			}
			return
		case <-time.After(every):
		}
		changed(p.try(ctx))
	}
}
