// Command deneb-relay is Deneb's own relay (implementation.md §2.6.3, M4):
// Tailscale's DERP server and a STUN server, plus what a Deneb relay
// needs. It registers with the control server under its owner's token,
// asks it whether each client may come in (a public relay: anyone logged
// in; someone's own: the devices of the LANs and shares it serves),
// closes the ones that may no longer, keeps public relays to their rate
// limit, and reports what each client sent and received, never who to
// whom.
//
// Someone's own relay installs itself (design/plan/m7/relay-install.md):
//
//	deneb-relay install --join <code>   checks, installs as a service, registers, has it probed from outside
//	deneb-relay status                  checks again, here and from outside
//	deneb-relay update                  the latest release, its signature checked
//	deneb-relay uninstall               stops, removes, tells the control server
//
// The service runs `deneb-relay run -data <dir>`, which reads config.json
// there. With no command, the flags alone say everything, as before
// (Deneb's own relays in Docker; a public relay):
//
//	deneb-relay -control https://203.0.113.1:47443 -control-pin sha256:… -token deneb-relay-…
//
// It keeps its secret, certificate and keys in -data; later starts need
// no token.
package main

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/derp/derpserver"
)

// config is what the flags say.
type config struct {
	control, controlPin, token, data string
	listen, stun                     string
	host                             string
	ipv4, ipv6                       netip.Addr
	derpPort, stunPort               uint16
	syncEvery                        time.Duration
	region                           string // -region: a place, "off", or "" to look
	// findAddrs: the addresses not given are looked for on the interfaces,
	// and looked at again every minute (an installed relay; Deneb's own
	// in Docker say theirs).
	findAddrs bool
	// autoUpdate: it follows Deneb's releases (an installed relay).
	autoUpdate bool
	// portMap: with no public IPv4 on its interfaces, it asks the router
	// to forward its ports (an installed relay, unless told not to).
	portMap bool
	// What it tells the control server about the machine
	// (proto.RelayInfo): how it runs, the host firewall install found, and
	// how many times it started within the hour.
	install      string
	firewall     string
	firewallOpen bool
	restarts     int
}

// commands are deneb-relay's commands; with none, it runs the relay.
var commands = map[string]func([]string) int{
	"run":       cmdRun,
	"install":   cmdInstall,
	"uninstall": cmdUninstall,
	"status":    cmdStatus,
	"update":    cmdUpdate,
	"version":   cmdVersion,
	"--version": cmdVersion,
	"-version":  cmdVersion,
}

func main() {
	log.SetPrefix("deneb-relay: ")
	args := os.Args[1:]
	if len(args) > 0 {
		if cmd, ok := commands[args[0]]; ok {
			os.Exit(cmd(args[1:]))
		}
	}
	os.Exit(cmdRun(args))
}

// cmdRun runs the relay until it is told to stop: in a service, from its
// data directory's config.json; in Docker, from its flags.
func cmdRun(args []string) int {
	var (
		cfg                config
		ipv4, ipv6         string
		derpPort, stunPort uint
		healthcheck        bool
		logPath            string
		ifStopped          bool
	)
	fs := flag.NewFlagSet("deneb-relay", flag.ExitOnError)
	fs.StringVar(&cfg.control, "control", "", "the control server: https://203.0.113.1:47443")
	fs.StringVar(&cfg.controlPin, "control-pin", "", "the control server's certificate pin, sha256:…")
	fs.StringVar(&cfg.token, "token", os.Getenv("DENEB_RELAY_TOKEN"), "registration token, for the first start (or $DENEB_RELAY_TOKEN)")
	join := fs.String("join", os.Getenv("DENEB_RELAY_JOIN"), "join code from the install command (or $DENEB_RELAY_JOIN): the control server, its pin and the token in one")
	fs.StringVar(&cfg.region, "region", "", `where the relay is, for its name: "香港"; "auto" asks the cloud's metadata, then $TZ; "" only $TZ; "off" nothing`)
	fs.StringVar(&cfg.data, "data", "/data", "data directory: secret, certificate, keys, config.json")
	fs.StringVar(&cfg.listen, "listen", ":47445", "DERP over HTTPS: the TCP address to listen on")
	fs.StringVar(&cfg.stun, "stun", ":47478", `STUN: the UDP address to listen on; "" for none`)
	fs.StringVar(&cfg.host, "hostname", "", "TLS name devices use (default: its address)")
	fs.StringVar(&ipv4, "ipv4", "", "the IPv4 address devices reach it at (default: a public one on its interfaces, else what the control server sees)")
	fs.StringVar(&ipv6, "ipv6", "", "the IPv6 address devices reach it at (default: a public one on its interfaces)")
	fs.UintVar(&derpPort, "derp-port", 0, "the TCP port devices reach DERP at, if not -listen's (a port mapping)")
	fs.UintVar(&stunPort, "stun-port", 0, "the UDP port devices reach STUN at, if not -stun's")
	fs.DurationVar(&cfg.syncEvery, "sync", time.Minute, "how often to sync with the control server")
	fs.BoolVar(&healthcheck, "healthcheck", false, "check that the relay with this -data answers, and exit: for Docker's HEALTHCHECK")
	fs.StringVar(&logPath, "log", "", "write the log to this file, kept under 30 MB (where no journal takes it)")
	fs.BoolVar(&ifStopped, "if-stopped", false, "do nothing if a relay already runs on -data: cron starts it so every minute")
	fs.Parse(args)

	if healthcheck {
		return check(cfg.data)
	}
	// config.json, which install writes, fills in what no flag said.
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	s, err := loadSettings(cfg.data)
	if err != nil {
		log.Print(err)
		return 1
	}
	if s != nil {
		apply := func(name string, to *string, v string) {
			if !set[name] && v != "" {
				*to = v
			}
		}
		apply("control", &cfg.control, s.Control)
		apply("control-pin", &cfg.controlPin, s.ControlPin)
		apply("listen", &cfg.listen, s.Listen)
		apply("stun", &cfg.stun, s.STUN)
		apply("hostname", &cfg.host, s.HostName)
		apply("region", &cfg.region, s.Region)
		apply("ipv4", &ipv4, s.IPv4)
		apply("ipv6", &ipv6, s.IPv6)
		if !set["derp-port"] && s.DERPPort != 0 {
			derpPort = uint(s.DERPPort)
		}
		if !set["stun-port"] && s.STUNPort != 0 {
			stunPort = uint(s.STUNPort)
		}
	}
	if logPath != "" {
		l, err := openLog(logPath)
		if err != nil {
			log.Print(err)
			return 1
		}
		log.SetOutput(l)
	}
	if ipv4 != "" {
		if cfg.ipv4, err = netip.ParseAddr(ipv4); err != nil || !cfg.ipv4.Is4() {
			log.Printf("-ipv4 %q is not an IPv4 address", ipv4)
			return 2
		}
	}
	if ipv6 != "" {
		if cfg.ipv6, err = netip.ParseAddr(ipv6); err != nil || !cfg.ipv6.Is6() {
			log.Printf("-ipv6 %q is not an IPv6 address", ipv6)
			return 2
		}
	}
	cfg.derpPort, cfg.stunPort = uint16(derpPort), uint16(stunPort)
	if *join != "" {
		if err := cfg.useJoin(*join); err != nil {
			log.Print(err)
			return 1
		}
	}
	if cfg.control == "" {
		fs.Usage()
		return 2
	}
	if err := os.MkdirAll(cfg.data, 0o700); err != nil {
		log.Print(err)
		return 1
	}
	release, ok, err := holdLock(cfg.data)
	if err != nil {
		log.Print(err)
		return 1
	}
	if !ok {
		if ifStopped {
			return 0
		}
		log.Printf("another relay is running on %s", cfg.data)
		return 1
	}
	defer release()
	installedRelay := s != nil && s.Install != nil
	cfg.findAddrs = installedRelay
	cfg.autoUpdate = installedRelay && !s.NoUpdate
	cfg.portMap = installedRelay && !s.NoPortMap
	cfg.restarts = noteStart(cfg.data, time.Now())
	if installedRelay {
		cfg.install = s.Install.installKind()
		cfg.firewall, cfg.firewallOpen = s.Install.FirewallKind, s.Install.FirewallOpen
	} else if _, err := os.Stat("/.dockerenv"); err == nil {
		cfg.install = proto.RelayInstallDocker
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = run(ctx, cfg, log.Printf, nil)
	switch {
	case errors.Is(err, errUpdated):
		release()
		reexec()
		return 1
	case errors.Is(err, errRevoked) && installedRelay:
		// Its owner no longer uses it (Deneb's "不用这个中转站"): it stops
		// serving and stays so. Exiting would only have the service manager
		// start it again and again.
		log.Printf("%v; no longer serving. To remove it: deneb-relay uninstall", err)
		<-ctx.Done()
		return 0
	case err != nil:
		log.Print(err)
		return 1
	}
	return 0
}

// useJoin takes the control server, its pin and the token from a join
// code, where the flags did not say.
func (cfg *config) useJoin(code string) error {
	control, pin, token, err := proto.ParseRelayJoinCode(code)
	if err != nil {
		return err
	}
	cfg.control = cmp.Or(cfg.control, control)
	cfg.controlPin = cmp.Or(cfg.controlPin, pin)
	cfg.token = cmp.Or(cfg.token, token)
	return nil
}

// listening is told the relay's addresses once it serves; for tests.
type listening func(derp net.Addr, stun net.Addr, r *relay)

// run runs the relay until ctx ends, or until the control server no longer
// knows it. When it returns, all it opened is closed: its ports are free,
// its router's forwarding taken back, and nothing of it still runs, even
// when its process stays (cmdRun: one Deneb no longer uses waits for a
// signal).
func run(ctx context.Context, cfg config, logf func(string, ...any), ready listening) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	if err := os.MkdirAll(cfg.data, 0o700); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	// Where -healthcheck finds it, whatever -listen said.
	if err := writeFile(cfg.data, listenFile, []byte(ln.Addr().String())); err != nil {
		return err
	}
	// STUN's port is free once run returns (stunServer.close).
	var stun *stunServer
	if cfg.stun != "" {
		if stun, err = listenSTUN(cfg.stun, logf); err != nil {
			return fmt.Errorf("STUN: %w", err)
		}
		defer stun.close()
	}
	info := proto.RelayInfo{HostName: cfg.host, IPv4: cfg.ipv4, IPv6: cfg.ipv6, DERPPort: cfg.derpPort, STUNPort: cfg.stunPort, Version: version(),
		Install: cfg.install, Firewall: cfg.firewall, FirewallOpen: cfg.firewallOpen, Restarts: cfg.restarts}
	if cfg.findAddrs {
		v4, v6, local := findAddrs()
		if !cfg.ipv4.IsValid() {
			info.IPv4 = v4
		}
		if !cfg.ipv6.IsValid() {
			info.IPv6 = v6
		}
		info.Local = local
	}
	if info.Region = detectRegion(ctx, cfg.region); info.Region != "" {
		logf("region: %s", info.Region)
	}
	if info.DERPPort == 0 {
		info.DERPPort = uint16(ln.Addr().(*net.TCPAddr).Port)
	}
	if info.STUNPort == 0 && stun != nil {
		info.STUNPort = uint16(stun.LocalAddr().(*net.UDPAddr).Port)
	}
	certName := cfg.host
	if certName == "" {
		certName = "deneb-relay"
		if cfg.ipv4.IsValid() {
			certName = cfg.ipv4.String()
		}
	}
	cert, pin, err := loadCert(cfg.data, certName)
	if err != nil {
		return fmt.Errorf("certificate: %w", err)
	}
	info.Pin = pin
	priv, err := loadDERPKey(cfg.data)
	if err != nil {
		return fmt.Errorf("DERP key: %w", err)
	}

	// It answers from here on, before the control server hears from it: a
	// check from outside, made as the server learns it is registered or
	// online again, finds it up (a check is a TLS handshake and a STUN
	// request). A request that comes this early waits until it is set up.
	up := make(chan struct{})
	var handler http.Handler
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			select {
			case <-up:
				handler.ServeHTTP(w, req)
			case <-req.Context().Done():
			case <-time.After(10 * time.Second):
				http.Error(w, "starting", http.StatusServiceUnavailable)
			}
		}),
		TLSConfig:         tlsConfig(cert),
		ReadHeaderTimeout: 10 * time.Second,
		// DERP upgrades HTTP/1.1; an empty map turns HTTP/2 off.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
		ErrorLog:     log.New(logWriter(logf), "http: ", 0),
	}
	defer srv.Close()
	errc := make(chan error, 3)
	go func() { errc <- srv.ServeTLS(ln, "", "") }()

	ctl, err := newControlClient(cfg.control, cfg.controlPin)
	if err != nil {
		return err
	}

	st, err := loadState(cfg.data)
	if err != nil {
		return err
	}
	if st != nil && st.Control != ctl.base {
		logf("registered with %s, now told %s: keeping the registration", st.Control, ctl.base)
	}
	if st == nil {
		if st, err = register(ctx, ctl, cfg, info, logf); err != nil {
			return err
		}
	}
	ctl.setSecret(st.Secret)
	d := derpserver.New(priv, logf)
	defer d.Close()
	r := newRelay(ctl, *st, info, d, logf)
	// The first sync says where the relay is now, and whether the server
	// still knows it. One set up again with a new token takes it.
	if err := r.sync(ctx); errors.Is(err, errRevoked) && cfg.token != "" {
		logf("%v; registering again with the token", err)
		if st, err = register(ctx, ctl, cfg, info, logf); err != nil {
			return err
		}
		ctl.setSecret(st.Secret)
		r.mu.Lock()
		r.st = *st
		r.mu.Unlock()
		d.SetVerifyClientURLFailOpen(st.Source == proto.RelayPublic)
		if err := r.sync(ctx); err != nil {
			return err
		}
	} else if errors.Is(err, errRevoked) {
		return fmt.Errorf("%w；在客户端里给它重新生成登记口令，用 -token 再启动", err)
	} else if err != nil {
		logf("sync: %v; serving, and trying again every %s", err, cfg.syncEvery)
	}
	logf("relay %s (%s, %s) on %s, STUN %v, pin %s", st.Relay, st.Name, st.Source, ln.Addr(), cfg.stun, pin)

	handler = r.handler()
	close(up)
	// Its own goroutines end before run returns, first of all that it
	// closes: a relay Deneb no longer uses stops asking the control server,
	// takes its router's forwarding back (keep) and stops watching its
	// addresses, while its process waits for a signal.
	var bg sync.WaitGroup
	defer func() {
		stop()
		bg.Wait()
	}()
	bg.Go(func() { errc <- r.syncLoop(ctx, cfg.syncEvery) })
	bg.Go(func() { r.watch(ctx) })
	find := findAddrs
	// Behind a router (no public IPv4 here, none given, no forwarding set
	// by hand): ask it to forward the ports, in the background, so that a
	// cloud's machine with no router to ask does not start late.
	if cfg.portMap && !info.IPv4.IsValid() && !cfg.ipv4.IsValid() && cfg.derpPort == 0 && info.STUNPort != 0 {
		pm := &portMapper{logf: logf, tcpLocal: info.DERPPort, udpLocal: info.STUNPort, mapPorts: mapPorts}
		bg.Go(func() { r.mapPortsFor(ctx, pm, cfg.data) })
		find = func() (netip.Addr, netip.Addr, []netip.Addr) {
			v4, v6, local := findAddrs()
			if ip, _, _, ok := pm.external(); ok && !v4.IsValid() {
				v4 = ip
			}
			return v4, v6, local
		}
	} else {
		writePortMap(cfg.data, nil)
	}
	if cfg.findAddrs {
		bg.Go(func() { r.watchAddrs(ctx, time.Minute, cfg.ipv4.IsValid(), cfg.ipv6.IsValid(), find) })
	}
	if cfg.autoUpdate {
		bg.Go(func() {
			if autoUpdate(ctx, logf) {
				errc <- errUpdated
			}
		})
	}
	if ready != nil {
		var sa net.Addr
		if stun != nil {
			sa = stun.LocalAddr()
		}
		ready(ln.Addr(), sa, r)
	}
	select {
	case <-ctx.Done():
		err = nil
	case err = <-errc:
	}
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shut)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// register spends the token: the control server learns where the relay
// is, and the relay gets its secret, kept in the data directory.
func register(ctx context.Context, ctl *controlClient, cfg config, info proto.RelayInfo, logf func(string, ...any)) (*state, error) {
	if cfg.token == "" {
		return nil, errors.New("第一次启动要给登记口令：-token，或者环境变量 DENEB_RELAY_TOKEN（在客户端里新建中转站时给的那串）")
	}
	var res proto.RelayRegisterResult
	if err := ctl.call(ctx, proto.PathRelayRegister, proto.RelayRegisterRequest{Token: cfg.token, RelayInfo: info}, &res); err != nil {
		return nil, fmt.Errorf("登记：%w", err)
	}
	st := &state{Relay: res.Relay, Name: res.Name, Source: res.Source, Secret: res.Secret, Control: ctl.base, existing: res.Existing}
	if err := saveState(cfg.data, st); err != nil {
		return nil, err
	}
	logf("registered as %s (%s, %s)", st.Relay, st.Name, st.Source)
	return st, nil
}

// check asks the relay whose data directory is dir for its probe page.
func check(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, listenFile))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_, port, err := net.SplitHostPort(strings.TrimSpace(string(b)))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if _, err := strconv.Atoi(port); err != nil {
		fmt.Fprintln(os.Stderr, "bad port:", port)
		return 1
	}
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		// Its own certificate, on this machine: the pin is for devices.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	resp, err := c.Get("https://127.0.0.1:" + port + "/derp/probe")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, resp.Status)
		return 1
	}
	return 0
}

// logWriter sends the HTTP server's own complaints (TLS handshakes from
// scanners, mostly) to the log.
type logWriter func(string, ...any)

func (l logWriter) Write(b []byte) (int, error) {
	l("%s", b)
	return len(b), nil
}
