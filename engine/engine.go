// Package engine is denebd: one engine serves every scope the device is in
// (plan §2.3, multi-lan-tech §4). It holds the device's keys, the stream to
// the control server, and the three tables the control server sends (peers,
// what is open to each scope, relays), and it swaps them whole on every new
// network map. Joining, pausing or leaving a scope changes only the tables:
// the socket, the relay connections, the keys and the interface stay.
//
// Packets go: WireGuard (one key, one port) ⇄ filter (scopes kept apart) ⇄
// the local stack: gVisor in user-space mode, a utun in kernel mode.
package engine

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"golang.zx2c4.com/wireguard/device"
	"tailscale.com/types/key"

	"tailscale.com/net/netmon"

	"tailscale.com/util/eventbus"

	"github.com/kittors/deneb/engine/bind"
	"github.com/kittors/deneb/engine/controlclient"
	"github.com/kittors/deneb/engine/dns"
	"github.com/kittors/deneb/engine/filter"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/magicsock"
	"github.com/kittors/deneb/engine/names"
	"github.com/kittors/deneb/engine/netstack"
	"github.com/kittors/deneb/engine/tun"
	"github.com/kittors/deneb/proto"
)

// Options configure an engine.
type Options struct {
	StateDir string
	// ControlURL and ControlPin are used for the first login; afterwards
	// the ones saved in the state directory win, so a device stays with the
	// server it was logged in to.
	ControlURL string
	ControlPin string
	Mode       apitype.Mode
	Port       uint16 // UDP port; 0 means the saved one, or any
	// BindInterface is "auto" (the default route's interface), "none", or
	// an interface name.
	BindInterface   string
	SOCKS           string // user-space mode: SOCKS5 for local apps, "" for none
	Heartbeat       time.Duration
	Trust           time.Duration
	Hostname        string
	DisplayName     string
	Kind            proto.DeviceKind
	RelayRoots      *x509.CertPool
	StaticEndpoints []netip.AddrPort
	// UserView: stream the owner's user view. The desktop client needs it;
	// a headless node fetches it only when the command line asks (§2.2).
	UserView bool
	MTU      int
	Version  string
	Logf     func(format string, args ...any)
	// Idle: a peer without traffic this long gets no more heartbeat until
	// traffic comes back (M2; default 45 s).
	Idle time.Duration
	// HomeRelay, if the network map has it, is where this device stays
	// reachable instead of the first relay of its first LAN (tests: peers
	// homed on different relays). RelayIdle closes other relays' unused
	// connections (default 60 s).
	HomeRelay proto.RelayID
	RelayIdle time.Duration
	// PathByLoss lets a lossy or slow direct path give way to the relay
	// (M2). Off by default until proven on real networks.
	PathByLoss bool
	// LockWait is how long to wait for another engine on the same state
	// directory to stop (0: refuse at once). A kernel-mode engine taking over
	// from the user-space one waits; everything else refuses.
	LockWait time.Duration
}

// ErrStateBusy means another engine runs on the same state directory.
var ErrStateBusy = errors.New("另一个 Deneb 引擎正在用这份状态")

// Engine is safe for concurrent use.
type Engine struct {
	opts Options
	logf func(format string, args ...any)

	unlock    func() // releases the state directory
	closeOnce sync.Once
	stopCh    chan struct{} // closed when a stop is asked for (test build)
	stopOnce  sync.Once

	nodePriv  key.NodePrivate
	discoPriv [32]byte
	machine   proto.MachinePrivate

	ms     *magicsock.Conn
	bindIf string // the physical interface Internet traffic is pinned to

	netMu         sync.Mutex // one reconnect at a time
	lastNetChange time.Time
	netmon        *netmon.Monitor
	bus           *eventbus.Bus
	streamKick    func()     // ends the current control stream
	lastUsage     string     // usageKey of the last usage published
	dnsFlushMu    sync.Mutex // one flush of the system's DNS caches at a time
	f             *filter.Filter
	names         atomic.Pointer[names.Table]
	pinger        *pinger

	ctx    context.Context
	cancel context.CancelFunc

	mu sync.Mutex
	st persisted

	// The data path, made when the first network map gives the device its
	// address.
	self    netip.Addr
	stack   *netstack.Stack
	kern    *tun.Kernel
	tunw    *filter.TUN
	dev     *device.Device
	dnsr    *dns.Responder
	wgPeers map[proto.NodePublic]wgPeer

	nm        *proto.NetMap
	version   uint64
	versionCh chan struct{} // closed and replaced at every new version
	userView  json.RawMessage

	cc         *controlclient.Client
	ccRunning  bool
	ccGen      int // which control loop is the current one
	ccCancel   context.CancelFunc
	loginURL   string
	controlUp  bool
	problems   map[string]apitype.Problem
	socksOn    bool
	portmaps   map[string]*portmap
	homeRelay  proto.RelayID
	lastReport string

	subs      map[int]func(apitype.Event)
	nextSub   int
	lastPaths map[proto.DeviceID]apitype.PeerPath
	pathWake  chan struct{}
	reportCh  chan struct{}

	test testKnobs // set only through the test build's endpoints
}

type wgPeer struct {
	addr  netip.Addr
	extra []netip.Prefix
}

// persisted is state.json in the state directory.
type persisted struct {
	Machine    proto.MachinePrivate `json:"machineKey"`
	Node       key.NodePrivate      `json:"nodeKey"`
	Disco      string               `json:"discoKey"`
	ControlURL string               `json:"controlURL,omitempty"`
	ControlPin string               `json:"controlPin,omitempty"`
	LoggedIn   bool                 `json:"loggedIn"`
	Device     proto.DeviceID       `json:"device,omitempty"`
	Person     proto.PersonID       `json:"person,omitempty"`
	Port       uint16               `json:"port,omitempty"`
	Paused     []proto.ScopeID      `json:"paused,omitempty"`
}

// New loads (or makes) the device's keys, opens the UDP socket and, if the
// device was logged in before, puts the last network map back in force, so
// connections come back before the control server answers.
func New(opts Options) (*Engine, error) {
	if opts.Mode == "" {
		opts.Mode = apitype.ModeUserspace
	}
	if opts.MTU == 0 {
		opts.MTU = 1280
	}
	if opts.Heartbeat == 0 {
		opts.Heartbeat = time.Second // plan §2.3: M1 uses 1 s and 2.5 s
	}
	if opts.Trust == 0 {
		opts.Trust = 2500 * time.Millisecond
	}
	if opts.BindInterface == "" {
		opts.BindInterface = "auto"
	}
	if opts.Hostname == "" {
		opts.Hostname, _ = os.Hostname()
	}
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}
	if err := os.MkdirAll(opts.StateDir, 0o700); err != nil {
		return nil, err
	}
	unlock, err := lockState(opts.StateDir, opts.LockWait)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		opts:      opts,
		logf:      logf,
		unlock:    unlock,
		stopCh:    make(chan struct{}),
		versionCh: make(chan struct{}),
		wgPeers:   map[proto.NodePublic]wgPeer{},
		problems:  map[string]apitype.Problem{},
		portmaps:  map[string]*portmap{},
		subs:      map[int]func(apitype.Event){},
		lastPaths: map[proto.DeviceID]apitype.PeerPath{},
		pathWake:  make(chan struct{}, 1),
		reportCh:  make(chan struct{}, 1),
	}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	if err := e.loadState(); err != nil {
		unlock()
		return nil, err
	}
	e.names.Store(names.NewTable("", nil))
	e.pinger = newPinger()

	if opts.Mode == apitype.ModeKernel {
		// Undo whatever a crashed run left before making anything new.
		tun.CleanupStale(opts.StateDir, logf)
	}
	ifname := ""
	switch opts.BindInterface {
	case "none":
	case "auto":
		n, err := bind.DefaultInterface()
		if err != nil {
			e.problem("bind", "没找到物理网卡，连接走系统路由；开着代理软件时直连可能会绕道代理。"+err.Error())
		}
		ifname = n
	default:
		ifname = opts.BindInterface
	}
	port := opts.Port
	if port == 0 {
		port = e.st.Port // keep the port across restarts: NAT mappings and firewall rules stay valid
	}
	ms, err := magicsock.New(e.nodePriv, e.discoPriv, magicsock.Options{
		Port:            port,
		BindInterface:   ifname,
		Heartbeat:       opts.Heartbeat,
		Trust:           opts.Trust,
		Idle:            opts.Idle,
		RelayIdle:       opts.RelayIdle,
		PathByLoss:      opts.PathByLoss,
		StaticEndpoints: opts.StaticEndpoints,
		RelayRoots:      opts.RelayRoots,
		Logf:            logf,
		OnEndpoints:     e.requestReport,
		OnPath:          func(proto.DeviceID) { e.wakePaths() },
		OnPeerRestart:   func(k proto.NodePublic) { go e.rekey(k) },
		OnTimeJump: func(d time.Duration) {
			go e.netChanged(fmt.Sprintf("woke after %s", d.Round(time.Second)))
		},
	})
	if err != nil {
		return nil, err
	}
	e.ms = ms
	e.bindIf, _ = ms.BoundInterface()
	if _, berr := ms.BoundInterface(); berr != nil {
		e.problem("bind", "没能把连接绑到物理网卡，改走系统路由："+berr.Error())
	}
	if e.st.Port != ms.Port() {
		e.st.Port = ms.Port()
		e.saveState()
	}
	e.f = filter.New(logf)

	if e.st.LoggedIn {
		if nm, err := e.loadNetmap(); err == nil {
			e.mu.Lock()
			e.applyNetmapLocked(nm, nm.Version)
			e.mu.Unlock()
			logf("[engine] restored the last network map (version %d) while the control server answers", nm.Version)
		}
	}
	return e, nil
}

// Start runs the engine's loops. It returns at once.
func (e *Engine) Start() {
	e.ms.Start()
	go e.watchNetwork()
	go e.gcLoop()
	go e.pathLoop()
	go e.reportLoop()
	e.mu.Lock()
	// With a control server configured, the control loop always runs: a
	// logged-in device streams its map; one that is not keeps a current
	// login link ready (State.loginURL, `deneb login`) and notices at once
	// when someone finishes logging in in a browser, even with a link it
	// printed before a restart. Links last 15 minutes and are renewed.
	if e.controlURLLocked() != "" {
		e.startControlLocked()
	}
	e.mu.Unlock()
}

// Close stops everything and, in kernel mode, removes the interface, the
// route and the resolver file.
func (e *Engine) Close() {
	e.closeOnce.Do(func() {
		e.cancel()
		e.mu.Lock()
		if e.ccCancel != nil {
			e.ccCancel()
		}
		for _, pm := range e.portmaps {
			pm.ln.Close()
		}
		e.teardownDataPathLocked()
		e.ms.Close()
		mon, bus := e.netmon, e.bus
		e.mu.Unlock()
		if mon != nil {
			mon.Close()
			bus.Close()
		}
		e.unlock()
	})
}

// StopRequested is closed when someone asks the engine to stop through the
// local API (the test build's /v1/test/shutdown): the program running the
// engine then stops it as it would on SIGTERM. A kernel-mode engine runs as
// root; this lets its own user stop it, which undoes the interface.
func (e *Engine) StopRequested() <-chan struct{} { return e.stopCh }

func (e *Engine) requestStop() { e.stopOnce.Do(func() { close(e.stopCh) }) }

func (e *Engine) controlURLLocked() string {
	if e.st.ControlURL != "" {
		return e.st.ControlURL
	}
	return e.opts.ControlURL
}

func (e *Engine) controlPinLocked() string {
	if e.st.ControlURL != "" {
		return e.st.ControlPin
	}
	return e.opts.ControlPin
}

func (e *Engine) statePath() string  { return filepath.Join(e.opts.StateDir, "state.json") }
func (e *Engine) netmapPath() string { return filepath.Join(e.opts.StateDir, "netmap.json") }

func (e *Engine) loadState() error {
	b, err := os.ReadFile(e.statePath())
	switch {
	case errors.Is(err, os.ErrNotExist):
		e.st.Machine = proto.NewMachineKey()
		e.st.Node = key.NewNode()
		var d [32]byte
		rand.Read(d[:])
		e.st.Disco = hex.EncodeToString(d[:])
		if err := e.saveState(); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(b, &e.st); err != nil {
			return fmt.Errorf("%s: %w", e.statePath(), err)
		}
	}
	d, err := hex.DecodeString(e.st.Disco)
	if err != nil || len(d) != 32 {
		return fmt.Errorf("%s: bad disco key", e.statePath())
	}
	copy(e.discoPriv[:], d)
	e.nodePriv = e.st.Node
	e.machine = e.st.Machine
	return nil
}

// saveState writes state.json atomically; it holds private keys, so 0600.
func (e *Engine) saveState() error {
	b, err := json.MarshalIndent(e.st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(e.statePath(), b)
}

func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	// A kernel-mode engine runs as root on the user's state: keep the files
	// the user's, or the user-space engine could not read them afterwards.
	chownToDir(tmp, filepath.Dir(path))
	return os.Rename(tmp, path)
}

func (e *Engine) loadNetmap() (*proto.NetMap, error) {
	b, err := os.ReadFile(e.netmapPath())
	if err != nil {
		return nil, err
	}
	var nm proto.NetMap
	return &nm, json.Unmarshal(b, &nm)
}

func (e *Engine) problem(code, msg string) {
	e.mu.Lock()
	e.problems[code] = apitype.Problem{Code: code, Message: msg}
	e.mu.Unlock()
	e.logf("[engine] problem %s: %s", code, msg)
}

func (e *Engine) clearProblem(code string) {
	e.mu.Lock()
	delete(e.problems, code)
	e.mu.Unlock()
}

func (e *Engine) gcLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-t.C:
			e.f.GC()
		}
	}
}

func firstV4(ps []netip.Prefix) netip.Addr {
	for _, p := range ps {
		if p.Addr().Is4() {
			return p.Addr()
		}
	}
	return netip.Addr{}
}

func firstV4Addr(as []netip.Addr) netip.Addr {
	for _, a := range as {
		if a.Is4() {
			return a
		}
	}
	return netip.Addr{}
}
