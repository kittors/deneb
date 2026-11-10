// Command deneb-control is Deneb's control server (implementation.md §2.2).
//
// In production it serves HTTPS with a self-signed certificate whose key
// clients pin:
//
//	deneb-control -listen :47443 -data /data -seed /data/seed.json \
//		-public-url https://103.42.31.155:47443
//
// For development it serves plain HTTP on 127.0.0.1:47080 with four test
// accounts (verification code 000000) and POST /dev/approve:
//
//	go run ./cmd/deneb-control -dev
//
// On the server's own machine, two commands talk to the running server
// through its admin socket (a Unix socket in the data directory, not a
// network port):
//
//	deneb-control logins                                  # devices waiting for a login
//	deneb-control approve -code <code or link> -email <a seed test account>
//	deneb-control expire -device <id> -in 72h             # a test: move a login's expiry
//	deneb-control operators [-add|-remove <email>]       # who manages the public relays (the ops page)
//	deneb-control sessions [-revoke <email>]              # browsers logged in; log all of one account's out
//	deneb-control backups                                 # the database's daily copies (<data>/backup)
//	deneb-control devices [-person <email>]               # the devices, and in what state
//	deneb-control device-delete -device <id>              # delete one, whoever's it is
//
// In the container: docker exec deneb-control deneb-control approve ...
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kittors/deneb/control"
	"github.com/kittors/deneb/proto"
)

type relayFlags []control.SeedRelay

func (r *relayFlags) String() string { return fmt.Sprint(len(*r), " relays") }

func (r *relayFlags) Set(s string) error {
	rl, err := control.ParseRelayFlag(s)
	if err != nil {
		return err
	}
	*r = append(*r, rl)
	return nil
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "approve":
			os.Exit(runApprove(os.Args[2:]))
		case "logins":
			os.Exit(runLogins(os.Args[2:]))
		case "expire":
			os.Exit(runExpire(os.Args[2:]))
		case "relays":
			os.Exit(runRelays(os.Args[2:]))
		case "relay-add":
			os.Exit(runRelayAdd(os.Args[2:]))
		case "relay-token":
			os.Exit(runRelayToken(os.Args[2:]))
		case "relay-rate":
			os.Exit(runRelayRate(os.Args[2:]))
		case "relay-kick":
			os.Exit(runRelayKick(os.Args[2:]))
		case "relay-unkick":
			os.Exit(runRelayUnkick(os.Args[2:]))
		case "relay-set":
			os.Exit(runRelaySet(os.Args[2:]))
		case "seats":
			os.Exit(runSeats(os.Args[2:]))
		case "operators":
			os.Exit(runOperators(os.Args[2:]))
		case "sessions":
			os.Exit(runSessions(os.Args[2:]))
		case "backups":
			os.Exit(runBackups(os.Args[2:]))
		case "devices":
			os.Exit(runDevices(os.Args[2:]))
		case "device-delete":
			os.Exit(runDeviceDelete(os.Args[2:]))
		}
	}
	var (
		dev         = flag.Bool("dev", false, "development: plain HTTP on 127.0.0.1:47080, test accounts with code 000000, POST /dev/approve")
		devReset    = flag.Bool("dev-reset", false, "with -dev: empty the data directory first")
		listen      = flag.String("listen", "", `address to listen on (default ":47443", or "127.0.0.1:47080" with -dev)`)
		dataDir     = flag.String("data", "", `data directory (default "/data", or the user cache directory with -dev)`)
		seedPath    = flag.String("seed", "", "seed file: test accounts, their codes, relays (with -dev it replaces the built-in one)")
		publicURL   = flag.String("public-url", "", "how devices and browsers reach this server; login and invite links start with it")
		tlsMode     = flag.String("tls", "auto", "serve HTTPS with the self-signed certificate: on, off, or auto (on unless -dev)")
		tlsHosts    = flag.String("tls-hosts", "", "comma-separated IPs and names the certificate names (default: the public URL's host, 127.0.0.1, localhost)")
		healthcheck = flag.String("healthcheck", "", "only check that this URL answers 200, then exit (for the container's health check)")
		lifetime    = flag.Duration("login-lifetime", control.DefaultLoginLifetime, "how long a device's login lasts if the device does not come back; shorten it to see logins run out")
		seatLimit   = flag.Int("seat-limit", control.DefaultSeatLimit, "people each person's LANs and shares may hold, themselves included; -1 for no limit")
		relayImage  = flag.String("relay-image", control.DefaultRelayImage, "the Docker image the relays' one-line install command runs")
	)
	var relays relayFlags
	flag.Var(&relays, "relay", `a relay as key=value pairs, repeatable; replaces the seed's: "id=hk,name=香港,host=derp.example.com,ipv4=1.2.3.4,derp=443,stun=3478"`)
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.SetPrefix("deneb-control: ")

	if *healthcheck != "" {
		os.Exit(check(*healthcheck))
	}

	if *listen == "" {
		*listen = ":47443"
		if *dev {
			*listen = "127.0.0.1:47080"
		}
	}
	if *dataDir == "" {
		*dataDir = "/data"
		if *dev {
			*dataDir = devDataDir()
		}
	}
	if *devReset {
		if !*dev {
			log.Fatal("-dev-reset only goes with -dev")
		}
		if err := os.RemoveAll(*dataDir); err != nil {
			log.Fatal(err)
		}
	}
	useTLS := *tlsMode == "on" || *tlsMode == "auto" && !*dev
	if *tlsMode != "on" && *tlsMode != "off" && *tlsMode != "auto" {
		log.Fatalf("-tls %q: want on, off or auto", *tlsMode)
	}
	if *publicURL == "" {
		if !*dev {
			log.Fatal("-public-url is required, such as https://103.42.31.155:47443")
		}
		host, port, _ := net.SplitHostPort(*listen)
		if host == "" || host == "0.0.0.0" || host == "::" {
			host = "127.0.0.1"
		}
		scheme := "http"
		if useTLS {
			scheme = "https"
		}
		*publicURL = scheme + "://" + net.JoinHostPort(host, port)
	}
	pu, err := url.Parse(*publicURL)
	if err != nil || pu.Host == "" {
		log.Fatalf("-public-url %q is not a URL", *publicURL)
	}

	seed := control.Seed{}
	if *dev {
		seed = control.DevSeed()
	}
	if *seedPath != "" {
		if seed, err = control.LoadSeed(*seedPath); err != nil {
			log.Fatal(err)
		}
	}
	if len(relays) > 0 {
		seed.Relays = relays
	}

	// The certificate comes first: relay join codes carry its pin.
	var cert atomic.Pointer[tls.Certificate]
	var loadCert func() (string, error)
	pin := ""
	if useTLS {
		hosts := []string{pu.Hostname(), "127.0.0.1", "localhost"}
		if *tlsHosts != "" {
			hosts = append(hosts, strings.Split(*tlsHosts, ",")...)
		}
		loadCert = func() (string, error) {
			c, pin, err := control.LoadOrCreateCert(*dataDir, hosts)
			if err == nil {
				cert.Store(&c)
			}
			return pin, err
		}
		if pin, err = loadCert(); err != nil {
			log.Fatal(err)
		}
		log.Printf("certificate pin %s (also in %s)", pin, filepath.Join(*dataDir, "tls", "pin.txt"))
	}

	srv, err := control.New(control.Config{
		DataDir:       *dataDir,
		PublicURL:     *publicURL,
		Dev:           *dev,
		Seed:          seed,
		LoginLifetime: *lifetime,
		SeatLimit:     *seatLimit,
		Pin:           pin,
		RelayImage:    *relayImage,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	n := srv.Network()
	log.Printf("data in %s; network %s and %s; version %d", *dataDir, n.IPv4, n.IPv6, srv.Version())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go srv.RunBackups(ctx)
	go srv.RunMaintenance(ctx)
	go func() {
		if err := srv.ServeAdmin(ctx, filepath.Join(*dataDir, control.AdminSocketName)); err != nil {
			log.Printf("admin socket: %v", err)
		}
	}()

	hs := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// Streams and long polls watch this context, so a shutdown ends
		// them instead of waiting on them.
		BaseContext: func(net.Listener) context.Context { return ctx },
		ErrorLog:    log.New(os.Stderr, "deneb-control: http: ", log.LstdFlags|log.LUTC),
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}

	if useTLS {
		go func() {
			// Remake the certificate before it runs out; the key, and so
			// the pin, stay the same.
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(24 * time.Hour):
					if _, err := loadCert(); err != nil {
						log.Printf("renew certificate: %v", err)
					}
				}
			}
		}()
		hs.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
				return cert.Load(), nil
			},
		}
	}
	log.Printf("serving %s on %s; public URL %s", map[bool]string{true: "HTTPS", false: "HTTP"}[useTLS], *listen, *publicURL)
	if *dev {
		log.Printf("dev: accounts kittors@deneb.test, jie@deneb.test, mia@deneb.test, leo@deneb.test, code %s; POST /dev/approve", control.DevCode)
	}

	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(sctx)
	}()
	if useTLS {
		// ServeTLS offers HTTP/2, so a device's stream and its requests
		// share one connection. The certificate comes from GetCertificate.
		err = hs.ServeTLS(ln, "", "")
	} else {
		err = hs.Serve(ln)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func devDataDir() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "deneb-control-dev")
	}
	return filepath.Join(os.TempDir(), "deneb-control-dev")
}

// check is the container's health check: the image has no curl.
func check(u string) int {
	c := &http.Client{
		Timeout: 5 * time.Second,
		// It asks the server on the same machine, which the pin is about;
		// there is nothing to verify the certificate against here.
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	resp, err := c.Get(u)
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

// adminFlags reads the flags the admin commands share and returns the
// socket's path.
func adminFlags(name string, args []string, more func(*flag.FlagSet)) (string, *flag.FlagSet) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	data := fs.String("data", "/data", "the server's data directory; the admin socket is in it")
	socket := fs.String("socket", "", "the admin socket (default <data>/"+control.AdminSocketName+")")
	if more != nil {
		more(fs)
	}
	fs.Parse(args)
	if *socket == "" {
		*socket = filepath.Join(*data, control.AdminSocketName)
	}
	return *socket, fs
}

// runLogins lists the devices waiting for someone to log in on them.
func runLogins(args []string) int {
	socket, _ := adminFlags("logins", args, nil)
	ls, err := control.AdminLogins(socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(ls) == 0 {
		fmt.Println("没有等着登录的设备")
		return 0
	}
	for _, l := range ls {
		name := l.Hostname
		if l.DisplayName != "" {
			name = l.DisplayName + "（" + l.Hostname + "）"
		}
		fmt.Printf("%s  %s  %s  来自 %s  %s 发起，%s 前有效\n", l.Code, name, l.OS, l.From,
			l.Created.Format("15:04:05Z"), l.Expires.Format("15:04:05Z"))
	}
	return 0
}

// runApprove logs a seed test account in on a waiting device.
func runApprove(args []string) int {
	var code, email *string
	socket, fs := adminFlags("approve", args, func(fs *flag.FlagSet) {
		code = fs.String("code", "", "the device's login code, or its whole login link")
		email = fs.String("email", "", "a test account from the seed file")
	})
	if *code == "" || *email == "" {
		fs.Usage()
		return 2
	}
	res, err := control.AdminApprove(socket, *code, *email)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("已登录：%s（%s，%s，来自 %s）现在是 %s 的设备 %s，地址 %s\n",
		res.Name, res.Hostname, res.OS, res.From, res.Email, res.Device, res.Address)
	return 0
}

// runExpire moves a device's login expiry, for testing "需要重新登录" and
// "登录过期". A device online renews itself within seconds: stop its engine
// first.
func runExpire(args []string) int {
	var device, in *string
	socket, fs := adminFlags("expire", args, func(fs *flag.FlagSet) {
		device = fs.String("device", "", "the device id, d_…")
		in = fs.String("in", "", `when its login runs out from now: "72h" shows 需要重新登录, "-1m" 登录过期`)
	})
	if *device == "" || *in == "" {
		fs.Usage()
		return 2
	}
	res, err := control.AdminExpire(socket, proto.DeviceID(*device), *in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("%s（%s）的登录 %s 到期，%s 起显示需要重新登录\n", res.Name, res.Device,
		res.ExpiresAt.UTC().Format("2006-01-02 15:04:05Z"), res.RenewFrom.UTC().Format("2006-01-02 15:04:05Z"))
	if res.Online {
		fmt.Println("注意：它现在在线，几秒内会自己续期；要看到需要重新登录，先停掉它的引擎")
	}
	return 0
}

// runRelays lists the relays (M4).
func runRelays(args []string) int {
	socket, _ := adminFlags("relays", args, nil)
	rs, err := control.AdminRelays(socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, r := range rs {
		state := "没登记"
		switch {
		case r.Seeded:
			state = "种子文件里的"
		case r.Online:
			state = "在线，" + r.LastSeen.UTC().Format("15:04:05Z") + " 报到"
		case r.Registered:
			state = "离线"
		}
		owner := "公共"
		if r.Owner != "" {
			owner = r.Owner
		}
		addr := r.HostName
		if r.IPv4.IsValid() {
			addr += " " + r.IPv4.String()
		}
		if r.IPv6.IsValid() {
			addr += " " + r.IPv6.String()
		}
		fmt.Printf("%s  %s  %s  %s  %s  DERP %d  STUN %d  限速 %s  拖进 %d 个局域网  权重 %d", r.ID, r.Name, owner, state, addr, r.DERPPort, r.STUNPort, rateString(r.RateLimit), r.LANs, r.Weight)
		if r.Capacity > 0 {
			fmt.Printf("  容量 %d", r.Capacity)
		}
		if r.Default {
			fmt.Print("  默认")
		}
		if r.Kicks > 0 {
			fmt.Printf("  踢了 %d 个", r.Kicks)
		}
		if l := r.Load; l != nil {
			fmt.Printf("\n    %s：%d 台设备、%d 个人，%s，CPU %.0f%%，内存 %s", l.At.UTC().Format("15:04:05Z"), l.Devices, l.People,
				rateString(l.BytesPerSec), l.CPU*100, sizeString(l.Memory))
			if l.MemoryLimit > 0 {
				fmt.Printf(" / %s", sizeString(l.MemoryLimit))
			}
			if l.Full {
				fmt.Print("，满载")
			}
		}
		fmt.Println()
	}
	return 0
}

// runRelayAdd makes a public relay and prints its registration token.
func runRelayAdd(args []string) int {
	var name, rate *string
	socket, fs := adminFlags("relay-add", args, func(fs *flag.FlagSet) {
		name = fs.String("name", "", "where it is, as people say it: 香港 (default: the region the relay reports)")
		rate = fs.String("rate", "0", `each connection's limit: "2MB/s", "500KB/s"; 0 is none`)
	})
	limit, err := parseRate(*rate)
	if err != nil {
		fs.Usage()
		return 2
	}
	res, err := control.AdminRelayAdd(socket, *name, limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	printToken(res)
	return 0
}

// runRelayToken gives a relay a new registration token, to set it up
// again.
func runRelayToken(args []string) int {
	var relay *string
	socket, fs := adminFlags("relay-token", args, func(fs *flag.FlagSet) {
		relay = fs.String("relay", "", "the relay id, r_…")
	})
	if *relay == "" {
		fs.Usage()
		return 2
	}
	res, err := control.AdminRelayNewToken(socket, proto.RelayID(*relay))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	printToken(res)
	return 0
}

// runRelayRate sets a public relay's rate limit.
func runRelayRate(args []string) int {
	var relay, rate *string
	socket, fs := adminFlags("relay-rate", args, func(fs *flag.FlagSet) {
		relay = fs.String("relay", "", "the relay id, r_…")
		rate = fs.String("rate", "", `each connection's limit: "2MB/s", "500KB/s"; 0 is none`)
	})
	limit, err := parseRate(*rate)
	if *relay == "" || *rate == "" || err != nil {
		fs.Usage()
		return 2
	}
	res, err := control.AdminRelayRate(socket, proto.RelayID(*relay), limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("%s（%s）每条连接限速 %s\n", res.Name, res.ID, rateString(res.RateLimit))
	return 0
}

// printToken shows a join code: the command to run on the relay's server,
// then the bare token (deneb-relay -token), last, for scripts.
func printToken(t *control.AdminRelayToken) {
	what := "新的公共中转站"
	if t.Relay != "" {
		what = "中转站 " + string(t.Relay) + "（" + t.Name + "）重新安装"
	} else if t.Name != "" {
		what += "（" + t.Name + "）"
	}
	fmt.Printf("%s的安装命令，只显示这一次，%s 前有效、只能用一次：\n\n  %s\n\n登记口令（给 deneb-relay -token）：\n%s\n",
		what, t.Expires.Format("2006-01-02 15:04Z"), t.Command, t.Token)
}

// parseRate reads a rate in bytes a second: "2MB/s", "500KB/s", "1500000",
// "0". K, M and G are powers of 1000, as network speeds are.
func parseRate(s string) (int64, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	t = strings.TrimSuffix(strings.TrimSuffix(t, "/S"), "B")
	mult := int64(1)
	for suffix, m := range map[string]int64{"K": 1e3, "M": 1e6, "G": 1e9} {
		if rest, ok := strings.CutSuffix(t, suffix); ok {
			t, mult = rest, m
		}
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("限速不对：%q", s)
	}
	return int64(f * float64(mult)), nil
}

func rateString(b int64) string {
	switch {
	case b == 0:
		return "不限"
	case b >= 1e6:
		return strconv.FormatFloat(float64(b)/1e6, 'f', -1, 64) + "MB/s"
	case b >= 1e3:
		return strconv.FormatFloat(float64(b)/1e3, 'f', -1, 64) + "KB/s"
	}
	return strconv.FormatInt(b, 10) + "B/s"
}

// runRelayKick kicks a person or a device off a relay.
func runRelayKick(args []string) int {
	var relay, email, device *string
	socket, fs := adminFlags("relay-kick", args, func(fs *flag.FlagSet) {
		relay = fs.String("relay", "", "the relay id, r_…")
		email = fs.String("person", "", "the person's email: all their devices")
		device = fs.String("device", "", "one device, d_…")
	})
	if *relay == "" || (*email == "") == (*device == "") {
		fs.Usage()
		return 2
	}
	id, err := control.AdminRelayKick(socket, proto.RelayID(*relay), *email, proto.DeviceID(*device))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("踢掉了（%s）；撤销：deneb-control relay-unkick -kick %s\n", id, id)
	return 0
}

// runRelayUnkick lifts a kick.
func runRelayUnkick(args []string) int {
	var kick *string
	socket, fs := adminFlags("relay-unkick", args, func(fs *flag.FlagSet) {
		kick = fs.String("kick", "", "the kick id, k_…")
	})
	if *kick == "" {
		fs.Usage()
		return 2
	}
	if err := control.AdminRelayUnkick(socket, *kick); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("撤销了，又能用了")
	return 0
}

// runRelaySet sets a relay's weight or capacity.
func runRelaySet(args []string) int {
	var relay *string
	weight, capacity := -1, -1
	socket, fs := adminFlags("relay-set", args, func(fs *flag.FlagSet) {
		relay = fs.String("relay", "", "the relay id, r_…")
		fs.IntVar(&weight, "weight", -1, "0–100: how many devices it gets first, against the others; 0 is a standby")
		fs.IntVar(&capacity, "capacity", -1, "devices at once; at it, the relay counts as full; 0 is no limit")
	})
	if *relay == "" || weight < 0 && capacity < 0 {
		fs.Usage()
		return 2
	}
	var wp, cp *int
	if weight >= 0 {
		wp = &weight
	}
	if capacity >= 0 {
		cp = &capacity
	}
	res, err := control.AdminRelaySet(socket, proto.RelayID(*relay), wp, cp)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("%s（%s）权重 %d，容量 %d（0 是不限）\n", res.Name, res.ID, res.Weight, res.Capacity)
	return 0
}

func sizeString(b int64) string {
	switch {
	case b >= 1<<30:
		return strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64) + "GB"
	case b >= 1<<20:
		return strconv.FormatFloat(float64(b)/(1<<20), 'f', 0, 64) + "MB"
	}
	return strconv.FormatFloat(float64(b)/(1<<10), 'f', 0, 64) + "KB"
}

// runSeats lists everyone's seats, or sets one person's limit for a test
// of "位置满了" on the running server: only that person's, and nothing in
// already is put out.
func runSeats(args []string) int {
	var email *string
	var limit int
	var reset *bool
	socket, fs := adminFlags("seats", args, func(fs *flag.FlagSet) {
		email = fs.String("person", "", "the account's email")
		fs.IntVar(&limit, "limit", 0, "their seats, themselves included; -1 for no limit")
		reset = fs.Bool("reset", false, "back to the deployment's limit")
	})
	if *email == "" {
		ss, err := control.AdminListSeats(socket)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		for _, s := range ss {
			fmt.Println(seatLine(s))
		}
		return 0
	}
	if *reset == (limit != 0) {
		fs.Usage()
		return 2
	}
	s, err := control.AdminSetSeats(socket, *email, limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(seatLine(*s))
	return 0
}

// runOperators lists the deployment's operators, who get the ops page and
// manage the public relays there, or makes someone one (-add) or no longer
// one (-remove), on the running server.
func runOperators(args []string) int {
	var add, remove *string
	socket, fs := adminFlags("operators", args, func(fs *flag.FlagSet) {
		add = fs.String("add", "", "make the account with this email an operator")
		remove = fs.String("remove", "", "the account with this email is not an operator any more")
	})
	var ops []control.AdminOperator
	var err error
	switch {
	case *add != "" && *remove != "":
		fs.Usage()
		return 2
	case *add != "":
		ops, err = control.AdminSetOperator(socket, *add, false)
	case *remove != "":
		ops, err = control.AdminSetOperator(socket, *remove, true)
	default:
		ops, err = control.AdminOperators(socket)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(ops) == 0 {
		fmt.Println("还没有部署管理员")
	}
	for _, o := range ops {
		fmt.Printf("%s  %s\n", o.Email, o.DisplayName)
	}
	return 0
}

// runSessions lists who is logged in in a browser (the Web app), or logs
// every browser of one account out: for when its code got out.
func runSessions(args []string) int {
	var revoke *string
	socket, _ := adminFlags("sessions", args, func(fs *flag.FlagSet) {
		revoke = fs.String("revoke", "", "log every browser of the account with this email out")
	})
	if *revoke != "" {
		n, err := control.AdminRevokeSessions(socket, *revoke)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("%s 的 %d 个浏览器已登出\n", *revoke, n)
		return 0
	}
	ss, err := control.AdminListSessions(socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(ss) == 0 {
		fmt.Println("没有登录着的浏览器")
	}
	for _, s := range ss {
		fmt.Printf("%s  %d 个浏览器，最近一次登录 %s\n", s.Email, s.Count, s.Newest.Format("2006-01-02 15:04Z"))
	}
	return 0
}

// runBackups lists the database's daily copies, newest first: one a day,
// a week of them and two months of weekly ones, without the signing key.
func runBackups(args []string) int {
	socket, _ := adminFlags("backups", args, nil)
	bs, err := control.AdminBackups(socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(bs) == 0 {
		fmt.Println("还没有备份")
	}
	for _, b := range bs {
		fmt.Printf("%s  %d 字节  %s 写的\n", b.Name, b.Size, b.Written.Format("2006-01-02 15:04Z"))
	}
	return 0
}

// runDevices lists the devices, or one person's, with their state.
func runDevices(args []string) int {
	var email *string
	socket, _ := adminFlags("devices", args, func(fs *flag.FlagSet) {
		email = fs.String("person", "", "only this account's devices")
	})
	ds, err := control.AdminDevices(socket, *email)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(ds) == 0 {
		fmt.Println("没有设备")
	}
	for _, d := range ds {
		fmt.Println(deviceLine(d))
	}
	return 0
}

// runDeviceDelete deletes a device whoever's it is, as device.delete does
// for its owner: one online is logged out at once.
func runDeviceDelete(args []string) int {
	var id *string
	socket, fs := adminFlags("device-delete", args, func(fs *flag.FlagSet) {
		id = fs.String("device", "", "the device's id (deneb-control devices lists them)")
	})
	if *id == "" {
		fs.Usage()
		return 2
	}
	d, err := control.AdminDeleteDevice(socket, proto.DeviceID(*id))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("删掉了：" + deviceLine(*d))
	return 0
}

func deviceLine(d control.AdminDevice) string {
	state := map[string]string{"online": "在线", "offline": "离线", "loggedOut": "已登出", "expired": "登录过期"}[d.State]
	seen := ""
	if !d.LastSeen.IsZero() {
		seen = "，最后在线 " + d.LastSeen.Format("2006-01-02 15:04Z")
	}
	return fmt.Sprintf("%s  %s  %s（%s，%s）  %s  %s%s", d.ID, d.Owner, d.DisplayName, d.Hostname, d.OS, d.Address, state, seen)
}

func seatLine(s control.AdminSeats) string {
	limit := "不限"
	if s.Limit > 0 {
		limit = strconv.Itoa(s.Limit)
	}
	whose := "部署的"
	if s.Own {
		whose = "单独设的"
	}
	return fmt.Sprintf("%s  用了 %d 个，上限 %s（%s）", s.Email, s.Used, limit, whose)
}
