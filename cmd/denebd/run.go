package main

import (
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/kittors/deneb/engine"
	"github.com/kittors/deneb/engine/cli"
	"github.com/kittors/deneb/engine/localapi"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/socket"
	"github.com/kittors/deneb/proto"
)

// version is set at build time: -ldflags "-X main.version=..."
var version = "dev"

func runCLI(args []string) error { return cli.Run(args, os.Stdout, os.Stderr) }

// defaultStateDir keeps keys and the last network map: per user for a
// user-space engine, system-wide for a root one.
func defaultStateDir() string { return socket.StateDir() }

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(s string) error { *m = append(*m, s); return nil }

func cmdRun(args []string) error { return runEngine(args, false) }

// checkRunFlags parses run's flags and starts nothing: `service install`
// reports a bad flag then, not at every start of the service.
func checkRunFlags(args []string) error { return runEngine(args, true) }

func runEngine(args []string, check bool) error {
	eh := flag.ExitOnError
	if check {
		eh = flag.ContinueOnError
	}
	fs := flag.NewFlagSet("run", eh)
	stateDir := fs.String("state", defaultStateDir(), "state directory: keys, last network map ($DENEB_STATE_DIR)")
	sock := fs.String("socket", socket.ListenPath(), "local API endpoint ($DENEB_SOCKET)")
	control := fs.String("control", os.Getenv("DENEB_CONTROL"), "control server for the first login, https://host:port ($DENEB_CONTROL; a release build's default is Deneb's own)")
	pin := fs.String("control-pin", os.Getenv("DENEB_CONTROL_PIN"), "control server key pin, sha256:<hex> ($DENEB_CONTROL_PIN)")
	mode := fs.String("mode", "userspace", "userspace, or kernel (root: an interface and routes; macOS /etc/resolver, Linux systemd-resolved)")
	port := fs.Uint("port", 0, "UDP port (0: the last one used, or any)")
	bindIf := fs.String("bind-if", "auto", "pin Internet traffic to this interface: auto (the default route's), none, or a name")
	relayOnly := fs.Bool("relay-only", false, "open no UDP port on the network; peers through relays only (the Windows window's own engine: Windows would ask the person about a program that opens one)")
	socks := fs.String("socks", "127.0.0.1:41080", "user-space mode: SOCKS5 for local programs (\"\" for none)")
	hb := fs.Duration("heartbeat", 0, "direct-path heartbeat (default 1s)")
	trust := fs.Duration("trust", 0, "how long a direct path is trusted without a pong (default 2.5s)")
	idle := fs.Duration("idle", 0, "stop a peer's heartbeat after this long without traffic (default 45s)")
	homeRelay := fs.String("home-relay", "", "stay reachable through this relay id instead of the first of the first LAN (tests)")
	relayIdle := fs.Duration("relay-idle", 0, "close an unused connection to a relay other than home after this long (default 1m)")
	pathByLoss := fs.Bool("path-by-loss", true, "let a lossy or slow direct path give way to the relay, when the relay is clearly cleaner (M2)")
	dupSmall := fs.String("dup-small", "auto", "send small packets twice on a lossy direct path: auto (from 5% loss), on, off")
	hostname := fs.String("hostname", "", "host name to report (default: the system's)")
	display := fs.String("name", "", "what to call this device the first time it logs in, e.g. 构建服务器 (default: the computer name on macOS, else the host name)")
	kind := fs.String("kind", "", "laptop, desktop, mini, server, nas, ...")
	relayCA := fs.String("relay-ca", "", "PEM file of extra CAs trusted for relays (test topology)")
	userView := fs.Bool("userview", envBool("DENEB_USERVIEW", true), "stream the owner's user view, which the window shows; a server without one turns it off ($DENEB_USERVIEW)")
	allowUID := fs.Int("allow-uid", envInt("DENEB_ALLOW_UID", -1), "kernel mode: the desktop user's uid, allowed on the local API ($DENEB_ALLOW_UID)")
	allowUser := fs.String("allow-user", "", "Windows kernel mode: the desktop user, a SID or PC\\name, allowed on the local API")
	waitLock := fs.Duration("wait-lock", 0, "wait this long for another engine on the same state to stop (a kernel-mode engine taking over)")
	var endpoints multi
	fs.Var(&endpoints, "endpoint", "extra ip:port peers may reach this device on (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if check && fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *display == "" {
		*display = defaultDisplayName()
	}
	*control, *pin = controlFor(*control, *pin)

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	var roots *x509.CertPool
	if *relayCA != "" {
		pem, err := os.ReadFile(*relayCA)
		if err != nil {
			return err
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return fmt.Errorf("%s: no certificates", *relayCA)
		}
	}
	var eps []netip.AddrPort
	for _, s := range endpoints {
		ap, err := netip.ParseAddrPort(s)
		if err != nil {
			return fmt.Errorf("-endpoint %q: %w", s, err)
		}
		eps = append(eps, ap)
	}
	m := apitype.Mode(*mode)
	if m != apitype.ModeUserspace && m != apitype.ModeKernel {
		return fmt.Errorf("-mode %q: userspace or kernel", *mode)
	}
	if check {
		return nil
	}
	if m == apitype.ModeUserspace && systemServiceRunning() {
		// The service is this device already (its identity came from this
		// user's state): a second engine of it would fight over it.
		return errors.New("Deneb 的系统服务在跑，这台电脑用它就行：用户态引擎不再起")
	}
	sockArg := *sock
	if m == apitype.ModeKernel && !isFlagSet(fs, "socket") && os.Getenv(socket.EnvPath) == "" {
		sockArg = socket.SystemPath()
	}
	e, err := engine.New(engine.Options{
		StateDir: *stateDir, ControlURL: *control, ControlPin: *pin, Mode: m,
		Port: uint16(*port), BindInterface: *bindIf, RelayOnly: *relayOnly, SOCKS: *socks,
		Heartbeat: *hb, Trust: *trust, Idle: *idle, HomeRelay: proto.RelayID(*homeRelay), RelayIdle: *relayIdle, PathByLoss: *pathByLoss, DupSmall: *dupSmall, Hostname: *hostname, DisplayName: *display, Kind: proto.DeviceKind(*kind),
		RelayRoots: roots, StaticEndpoints: eps, UserView: *userView, Version: version, LockWait: *waitLock,
		SelfUpdate: serviceSelfUpdate(),
	})
	if err != nil {
		return err
	}
	ln, err := socket.Listen(sockArg, socket.ListenOptions{AllowUID: *allowUID, AllowUser: *allowUser})
	if err != nil {
		e.Close()
		return fmt.Errorf("local API: %w", err)
	}
	srv := &http.Server{Handler: localapi.Handler(e, e.TestHandler())}
	go srv.Serve(ln)
	e.Start()
	test := ""
	if engine.TestBuild {
		test = " (test build)"
	}
	log.Printf("denebd %s%s: %s mode, local API on %s, state in %s", version, test, m, sockArg, *stateDir)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-e.StopRequested():
	case <-stopCh:
	}
	log.Printf("stopping")
	srv.Close()
	e.Close()
	log.Printf("stopped")
	if runtime.GOOS != "windows" {
		os.Remove(sockArg)
	}
	return nil
}

// stopCh, closed by stopRun, stops cmdRun like a signal: the Windows
// service manager's Stop.
var (
	stopCh   = make(chan struct{})
	stopOnce sync.Once
)

func stopRun() { stopOnce.Do(func() { close(stopCh) }) }

// splitState takes -state out of run's flags (-state x, -state=x, --state
// x; the last one counts), for the Windows service, whose default is its
// own. A -state without a value stays, for run's flags to report.
func splitState(args []string, def string) (state string, rest []string) {
	return splitFlag(args, "state", def)
}

// splitFlag takes the flag -flag out of args, as splitState does -state.
func splitFlag(args []string, flag, def string) (state string, rest []string) {
	state = def
	for i := 0; i < len(args); i++ {
		name, val, hasVal := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		if !strings.HasPrefix(args[i], "-") || args[i] == "--" || name != flag {
			rest = append(rest, args[i])
			continue
		}
		if !hasVal {
			if i+1 == len(args) {
				rest = append(rest, args[i])
				continue
			}
			i++
			val = args[i]
		}
		state = val
	}
	return state, rest
}

// hasFlag: -name or -name=… is among args.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if n, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "="); strings.HasPrefix(a, "-") && n == name {
			return true
		}
	}
	return false
}

// envBool is the boolean in $name (true, false, 1, 0, ...), or def when it
// is unset or not one.
func envBool(name string, def bool) bool {
	if b, err := strconv.ParseBool(os.Getenv(name)); err == nil {
		return b
	}
	return def
}

// envInt is the integer in $name, or def when it is unset or not one.
func envInt(name string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return n
	}
	return def
}

func isFlagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}
