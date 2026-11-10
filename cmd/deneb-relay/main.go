// Command deneb-relay is Deneb's own relay (implementation.md §2.6.3, M4):
// Tailscale's DERP server and a STUN server, plus what a Deneb relay
// needs. It registers with the control server under its owner's token,
// asks it whether each client may come in (a public relay: anyone logged
// in; someone's own: the devices of the LANs and shares it serves),
// closes the ones that may no longer, keeps public relays to their rate
// limit, and reports what each client sent and received, never who to
// whom.
//
// The first start needs the registration token from relay.create (the
// client's "我的服务器") or deneb-control relay-add (a public relay):
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
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/derp/derpserver"
	"tailscale.com/net/stunserver"
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
}

func main() {
	var (
		cfg                config
		ipv4, ipv6         string
		derpPort, stunPort uint
		healthcheck        bool
	)
	flag.StringVar(&cfg.control, "control", "", "the control server: https://203.0.113.1:47443")
	flag.StringVar(&cfg.controlPin, "control-pin", "", "the control server's certificate pin, sha256:…")
	flag.StringVar(&cfg.token, "token", os.Getenv("DENEB_RELAY_TOKEN"), "registration token, for the first start (or $DENEB_RELAY_TOKEN)")
	join := flag.String("join", os.Getenv("DENEB_RELAY_JOIN"), "join code from the install command (or $DENEB_RELAY_JOIN): the control server, its pin and the token in one")
	flag.StringVar(&cfg.region, "region", "", `where the relay is, for its name: "香港"; "auto" asks the cloud's metadata, then $TZ; "" only $TZ; "off" nothing`)
	flag.StringVar(&cfg.data, "data", "/data", "data directory: secret, certificate, keys")
	flag.StringVar(&cfg.listen, "listen", ":47445", "DERP over HTTPS: the TCP address to listen on")
	flag.StringVar(&cfg.stun, "stun", ":47478", `STUN: the UDP address to listen on; "" for none`)
	flag.StringVar(&cfg.host, "hostname", "", "TLS name devices use (default: its address)")
	flag.StringVar(&ipv4, "ipv4", "", "the IPv4 address devices reach it at (default: what the control server sees)")
	flag.StringVar(&ipv6, "ipv6", "", "the IPv6 address devices reach it at")
	flag.UintVar(&derpPort, "derp-port", 0, "the TCP port devices reach DERP at, if not -listen's (a port mapping)")
	flag.UintVar(&stunPort, "stun-port", 0, "the UDP port devices reach STUN at, if not -stun's")
	flag.DurationVar(&cfg.syncEvery, "sync", time.Minute, "how often to sync with the control server")
	flag.BoolVar(&healthcheck, "healthcheck", false, "check that the relay with this -data answers, and exit: for Docker's HEALTHCHECK")
	flag.Parse()
	log.SetPrefix("deneb-relay: ")

	if healthcheck {
		os.Exit(check(cfg.data))
	}
	var err error
	if ipv4 != "" {
		if cfg.ipv4, err = netip.ParseAddr(ipv4); err != nil || !cfg.ipv4.Is4() {
			log.Fatalf("-ipv4 %q is not an IPv4 address", ipv4)
		}
	}
	if ipv6 != "" {
		if cfg.ipv6, err = netip.ParseAddr(ipv6); err != nil || !cfg.ipv6.Is6() {
			log.Fatalf("-ipv6 %q is not an IPv6 address", ipv6)
		}
	}
	cfg.derpPort, cfg.stunPort = uint16(derpPort), uint16(stunPort)
	if *join != "" {
		if err := cfg.useJoin(*join); err != nil {
			log.Fatal(err)
		}
	}
	if cfg.control == "" {
		flag.Usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg, log.Printf, nil); err != nil {
		log.Fatal(err)
	}
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

// version is the relay program's, for the control server's log.
func version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "deneb-relay"
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 12 {
			return "deneb-relay " + s.Value[:12]
		}
	}
	return "deneb-relay " + bi.Main.Version
}

// listening is told the relay's addresses once it serves; for tests.
type listening func(derp net.Addr, stun net.Addr, r *relay)

// run runs the relay until ctx ends, or until the control server no longer
// knows it.
func run(ctx context.Context, cfg config, logf func(string, ...any), ready listening) error {
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
	var stun *stunserver.STUNServer
	if cfg.stun != "" {
		stun = stunserver.New(ctx)
		if err := stun.Listen(cfg.stun); err != nil {
			return fmt.Errorf("STUN: %w", err)
		}
	}
	info := proto.RelayInfo{HostName: cfg.host, IPv4: cfg.ipv4, IPv6: cfg.ipv6, DERPPort: cfg.derpPort, STUNPort: cfg.stunPort, Version: version()}
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
		return fmt.Errorf("%v；在客户端里给它重新生成登记口令，用 -token 再启动", err)
	} else if err != nil {
		logf("sync: %v; serving, and trying again every %s", err, cfg.syncEvery)
	}
	logf("relay %s (%s, %s) on %s, STUN %v, pin %s", st.Relay, st.Name, st.Source, ln.Addr(), cfg.stun, pin)

	srv := &http.Server{
		Handler:           r.handler(),
		TLSConfig:         tlsConfig(cert),
		ReadHeaderTimeout: 10 * time.Second,
		// DERP upgrades HTTP/1.1; an empty map turns HTTP/2 off.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
		ErrorLog:     log.New(logWriter(logf), "http: ", 0),
	}
	errc := make(chan error, 3)
	go func() { errc <- srv.ServeTLS(ln, "", "") }()
	if stun != nil {
		go func() { errc <- stun.Serve() }()
	}
	go func() { errc <- r.syncLoop(ctx, cfg.syncEvery) }()
	go r.watch(ctx)
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
	st := &state{Relay: res.Relay, Name: res.Name, Source: res.Source, Secret: res.Secret, Control: ctl.base}
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
