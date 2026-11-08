package main

import (
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"runtime"
	"strings"
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

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	stateDir := fs.String("state", defaultStateDir(), "state directory: keys, last network map ($DENEB_STATE_DIR)")
	sock := fs.String("socket", socket.ListenPath(), "local API endpoint ($DENEB_SOCKET)")
	control := fs.String("control", os.Getenv("DENEB_CONTROL"), "control server for the first login, https://host:port ($DENEB_CONTROL)")
	pin := fs.String("control-pin", os.Getenv("DENEB_CONTROL_PIN"), "control server key pin, sha256:<hex> ($DENEB_CONTROL_PIN)")
	mode := fs.String("mode", "userspace", "userspace, or kernel (macOS, root: a utun, one route, /etc/resolver)")
	port := fs.Uint("port", 0, "UDP port (0: the last one used, or any)")
	bindIf := fs.String("bind-if", "auto", "pin Internet traffic to this interface: auto (the default route's), none, or a name")
	socks := fs.String("socks", "127.0.0.1:41080", "user-space mode: SOCKS5 for local programs (\"\" for none)")
	hb := fs.Duration("heartbeat", 0, "direct-path heartbeat (default 1s)")
	trust := fs.Duration("trust", 0, "how long a direct path is trusted without a pong (default 2.5s)")
	idle := fs.Duration("idle", 0, "stop a peer's heartbeat after this long without traffic (default 45s)")
	homeRelay := fs.String("home-relay", "", "stay reachable through this relay id instead of the first of the first LAN (tests)")
	relayIdle := fs.Duration("relay-idle", 0, "close an unused connection to a relay other than home after this long (default 1m)")
	pathByLoss := fs.Bool("path-by-loss", false, "let a lossy or slow direct path give way to the relay (M2; off until proven)")
	hostname := fs.String("hostname", "", "host name to report (default: the system's)")
	display := fs.String("name", "", "what to call this device the first time it logs in, e.g. 构建服务器 (default: the computer name on macOS, else the host name)")
	kind := fs.String("kind", "", "laptop, desktop, mini, server, nas, ...")
	relayCA := fs.String("relay-ca", "", "PEM file of extra CAs trusted for relays (test topology)")
	userView := fs.Bool("userview", runtime.GOOS == "darwin", "stream the owner's user view (the desktop client needs it)")
	allowUID := fs.Int("allow-uid", -1, "kernel mode: the desktop user's uid, allowed on the local API")
	waitLock := fs.Duration("wait-lock", 0, "wait this long for another engine on the same state to stop (a kernel-mode engine taking over)")
	var endpoints multi
	fs.Var(&endpoints, "endpoint", "extra ip:port peers may reach this device on (repeatable)")
	fs.Parse(args)
	if *display == "" {
		*display = defaultDisplayName()
	}

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
	sockArg := *sock
	if m == apitype.ModeKernel && !isFlagSet(fs, "socket") && os.Getenv(socket.EnvPath) == "" {
		sockArg = socket.SystemPath()
	}
	e, err := engine.New(engine.Options{
		StateDir: *stateDir, ControlURL: *control, ControlPin: *pin, Mode: m,
		Port: uint16(*port), BindInterface: *bindIf, SOCKS: *socks,
		Heartbeat: *hb, Trust: *trust, Idle: *idle, HomeRelay: proto.RelayID(*homeRelay), RelayIdle: *relayIdle, PathByLoss: *pathByLoss, Hostname: *hostname, DisplayName: *display, Kind: proto.DeviceKind(*kind),
		RelayRoots: roots, StaticEndpoints: eps, UserView: *userView, Version: version, LockWait: *waitLock,
	})
	if err != nil {
		return err
	}
	ln, err := socket.Listen(sockArg, socket.ListenOptions{AllowUID: *allowUID})
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
	}
	log.Printf("stopping")
	srv.Close()
	e.Close()
	if runtime.GOOS != "windows" {
		os.Remove(sockArg)
	}
	return nil
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
