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
//	deneb-control alerts [-n 20]                          # the problems now and the last alerts
//	deneb-control logs [-n 200] [-grep <text>]            # the end of the log (its files keep 30 days)
//	deneb-control device-delete -device <id>              # delete one, whoever's it is
//	deneb-control account-delete -email <email> [-lans transfer|delete] [-yes]
//	                                                      # what deleting an account takes; with -yes, delete it
//
// In the container: docker exec deneb-control deneb-control approve ...
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kittors/deneb/control"
	"github.com/kittors/deneb/control/alert"
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
		case "alerts":
			os.Exit(runAlerts(os.Args[2:]))
		case "logs":
			os.Exit(runLogs(os.Args[2:]))
		case "device-delete":
			os.Exit(runDeviceDelete(os.Args[2:]))
		case "account-delete":
			os.Exit(runAccountDelete(os.Args[2:]))
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
		logDir      = flag.String("log-dir", "", `where the log goes: a file a day, kept 30 days (default "<data>/logs"; with -dev, or "off", the standard error only)`)
		lifetime    = flag.Duration("login-lifetime", control.DefaultLoginLifetime, "how long a device's login lasts if the device does not come back; shorten it to see logins run out")
		seatLimit   = flag.Int("seat-limit", control.DefaultSeatLimit, "people each person's LANs and shares may hold, themselves included; -1 for no limit")
		relayImage  = flag.String("relay-image", control.DefaultRelayImage, "the Docker image the relays' one-line install command runs")
		mailMode    = flag.String("mail", "auto", "how login codes go out: smtp, log (with -dev only: into the log), none, or auto (smtp if a host is set, log with -dev, else none)")
		smtpHost    = flag.String("smtp-host", "", "SMTP server for the login codes; also DENEB_SMTP_HOST or <data>/mail.json, where the password goes (never a flag)")
		smtpPort    = flag.Int("smtp-port", 0, "SMTP port (default 465 with -smtp-tls tls, else 587)")
		smtpUser    = flag.String("smtp-username", "", "SMTP account (password: DENEB_SMTP_PASSWORD or <data>/mail.json)")
		smtpFrom    = flag.String("smtp-from", "", `who the codes are from: "Deneb <noreply@example.com>"`)
		smtpTLS     = flag.String("smtp-tls", "", "tls (implicit, port 465) or starttls (port 587)")
		openSignup  = flag.Bool("open-signup", false, "let an email with no account log in: its account is made when its code checks out")
		webURL      = flag.String("web-url", "", "the Web version's address, as https://deneb.example.com: invite links open it, and this server's own invite links redirect there")
		trustProxy  = flag.String("trust-proxy", "", "where deneb-web runs behind nginx, as 198.51.100.4 (comma-separated addresses or prefixes): a browser's address comes from X-Real-IP on requests from there, and from nowhere else")
		alertURL    = flag.String("alert-webhook", "", "where alerts go: a WeCom, Feishu, DingTalk, Telegram or Bark bot's URL, or any JSON webhook; it holds the bot's key, so rather put it in <data>/alert.json or DENEB_ALERT_URL, which ps does not show")
		alertKind   = flag.String("alert-kind", "", "wecom, feishu, dingtalk, telegram, bark or webhook (default: from the URL's host)")
		name        = flag.String("name", "", "who the alerts say they are from (default: the public URL's host)")
	)
	var relays relayFlags
	flag.Var(&relays, "relay", `a relay as key=value pairs, repeatable; replaces the seed's: "id=hk,name=香港,host=derp.example.com,ipv4=1.2.3.4,derp=443,stun=3478"`)
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.LUTC)
	log.SetPrefix("deneb-control: ")

	if *healthcheck != "" {
		os.Exit(check(*healthcheck))
	}
	setMemoryLimit()

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
	// The log goes into files of its own that keep 30 days (PRIVACY.md):
	// Docker's log of the container keeps by size, and at a quiet server
	// that is months.
	logOut := io.Writer(os.Stderr)
	switch {
	case *logDir == "off":
		*logDir = ""
	case *logDir == "" && !*dev:
		*logDir = filepath.Join(*dataDir, "logs")
	}
	if *logDir != "" {
		lf, err := control.OpenLogFiles(*logDir)
		if err != nil {
			log.Fatal(err)
		}
		defer lf.Close()
		log.Printf("the log goes to %s, a file a day, kept %d days; read it with: deneb-control logs", *logDir, control.LogKeepDays)
		log.SetOutput(lf)
		logOut = lf
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

	mailer, err := setUpMail(*mailMode, *dataDir, *dev, control.SMTPConfig{
		Host: *smtpHost, Port: *smtpPort, Username: *smtpUser, From: *smtpFrom, TLS: *smtpTLS,
	})
	if err != nil {
		log.Fatal(err)
	}

	alerts, err := setUpAlerts(*dataDir, *alertURL, *alertKind)
	if err != nil {
		log.Fatal(err)
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
	proxies, err := control.ParseProxies(*trustProxy)
	if err != nil {
		log.Fatal(err)
	}

	srv, err := control.New(control.Config{
		DataDir:       *dataDir,
		PublicURL:     *publicURL,
		WebURL:        *webURL,
		TrustProxies:  proxies,
		Dev:           *dev,
		Seed:          seed,
		LoginLifetime: *lifetime,
		SeatLimit:     *seatLimit,
		Pin:           pin,
		RelayImage:    *relayImage,
		LogDir:        *logDir,
		Mailer:        mailer,
		OpenSignup:    *openSignup,
		Alerts:        alerts,
		Name:          *name,
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
		ErrorLog:    log.New(logOut, "deneb-control: http: ", log.LstdFlags|log.LUTC),
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
						srv.Notify("cert-renew", "证书续不上了", err.Error()+"；证书 397 天有效，还剩 30 天时开始续，每天试一次")
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

// setMemoryLimit keeps the Go heap within four fifths of the container's
// memory limit, unless GOMEMLIMIT says otherwise: thousands of streams make
// a lot of garbage at each change, and the heap may otherwise grow to twice
// what is live before the collector runs, past the limit, where the kernel
// kills the server (the load test, design/plan/m7/control-ops.md §4).
func setMemoryLimit() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	for _, p := range []string{"/sys/fs/cgroup/memory.max", "/sys/fs/cgroup/memory/memory.limit_in_bytes"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
		if err != nil || v <= 0 || v >= 1<<60 { // "max", or v1's "no limit"
			return
		}
		debug.SetMemoryLimit(v / 5 * 4)
		log.Printf("memory: the container may use %d MB; the heap keeps within %d MB", v>>20, v/5*4>>20)
		return
	}
}

// setUpAlerts reads where alerts go: <data>/alert.json, the environment
// (DENEB_ALERT_*) over it, the flags over both. None set, they are only
// logged (and kept for deneb-control alerts).
func setUpAlerts(dataDir, url, kind string) (alert.Sender, error) {
	c, err := alert.LoadConfig(filepath.Join(dataDir, "alert.json"), os.Getenv)
	if err != nil {
		return nil, err
	}
	if url != "" {
		c.URL = url
	}
	if kind != "" {
		c.Kind = kind
	}
	if c.URL == "" {
		log.Printf("alerts: nowhere set to send them; they go into this log and deneb-control alerts")
		return nil, nil
	}
	s, err := alert.NewSender(c)
	if err != nil {
		return nil, err
	}
	log.Printf("alerts: to %s", s)
	return s, nil
}

// runLogs prints the end of the log, from its files.
func runLogs(args []string) int {
	var n *int
	var grep *string
	socket, _ := adminFlags("logs", args, func(fs *flag.FlagSet) {
		n = fs.Int("n", 200, "how many of the last lines")
		grep = fs.String("grep", "", "only the lines with this in them")
	})
	lines, err := control.AdminLogs(socket, *n, *grep)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	return 0
}

// runAlerts shows the problems firing now and the last alerts, newest
// first.
func runAlerts(args []string) int {
	var n *int
	socket, _ := adminFlags("alerts", args, func(fs *flag.FlagSet) {
		n = fs.Int("n", 20, "how many of the last alerts to show")
	})
	a, err := control.AdminListAlerts(socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(a.Active) == 0 {
		fmt.Println("现在没有问题")
	} else {
		fmt.Println("现在的问题：")
		for _, al := range a.Active {
			fmt.Printf("  %s 起  %s  %s\n", alert.Clock(al.At), al.Title, al.Text)
		}
	}
	if len(a.Recent) == 0 {
		fmt.Println("还没有提醒")
		return 0
	}
	fmt.Println("最近的提醒：")
	outcome := map[string]string{alert.Sent: "发出去了", alert.Held: "冷却中没发", alert.Logged: "只记了日志", alert.Failed: "没发出去"}
	kind := map[alert.Kind]string{alert.Firing: "警报", alert.Resolved: "恢复", alert.Info: "消息"}
	for i := len(a.Recent) - 1; i >= 0 && i >= len(a.Recent)-*n; i-- {
		r := a.Recent[i]
		line := fmt.Sprintf("  %s  %s  %s", alert.Clock(r.At), kind[r.Kind], r.Title)
		if r.Text != "" {
			line += "：" + r.Text
		}
		line += "（" + outcome[r.Outcome]
		if r.Error != "" {
			line += "：" + r.Error
		}
		fmt.Println(line + "）")
	}
	return 0
}

// setUpMail picks how the login codes go out. The SMTP settings come from
// <data>/mail.json, the environment over it, and the flags over both; the
// password never from a flag, which ps and docker inspect show.
func setUpMail(mode, dataDir string, dev bool, flags control.SMTPConfig) (control.Mailer, error) {
	c, err := control.LoadSMTPConfig(dataDir, os.Getenv)
	if err != nil {
		return nil, err
	}
	if flags.Host != "" {
		c.Host = flags.Host
	}
	if flags.Port != 0 {
		c.Port = flags.Port
	}
	if flags.Username != "" {
		c.Username = flags.Username
	}
	if flags.From != "" {
		c.From = flags.From
	}
	if flags.TLS != "" {
		c.TLS = flags.TLS
	}
	if mode == "auto" {
		switch {
		case c.Host != "":
			mode = "smtp"
		case dev:
			mode = "log"
		default:
			mode = "none"
		}
	}
	switch mode {
	case "smtp":
		m, err := control.NewSMTPMailer(c)
		if err != nil {
			return nil, err
		}
		log.Printf("mail: login codes go out through %s", m)
		return m, nil
	case "log":
		// Whoever reads the log could log in as anyone.
		if !dev {
			return nil, fmt.Errorf("-mail log only goes with -dev")
		}
		log.Printf("mail: login codes go into this log (development)")
		return control.LogMailer{Logf: log.Printf}, nil
	case "none":
		log.Printf("mail: no mail set up; only the seed's test accounts can log in")
		return nil, nil
	}
	return nil, fmt.Errorf("-mail %q: want smtp, log, none or auto", mode)
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

// runAccountDelete deletes an account for someone who asked the operator
// to. Without -yes it only says what would go.
func runAccountDelete(args []string) int {
	var email, lans *string
	var yes *bool
	socket, fs := adminFlags("account-delete", args, func(fs *flag.FlagSet) {
		email = fs.String("email", "", "the account's email")
		lans = fs.String("lans", "", "the LANs it owns that have other people in them: transfer (each to its member who joined first) or delete (for everyone)")
		yes = fs.Bool("yes", false, "delete it; without -yes, only say what would go")
	})
	if *email == "" {
		fs.Usage()
		return 2
	}
	d, err := control.AdminAccountDelete(socket, control.AdminAccountDeleteRequest{Email: *email, OwnedLANs: *lans, DryRun: !*yes})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	verb := "删掉了"
	if d.DryRun {
		verb = "会删掉"
	}
	fmt.Printf("%s %s：%d 台设备 %v，%d 个局域网 %v，%d 个中转站 %v，%d 次分享，%d 个邀请\n",
		verb, *email, len(d.Devices), d.Devices, len(d.LANs), d.LANs, len(d.Relays), d.Relays, d.Shares, d.Invites)
	for _, t := range d.Transferred {
		fmt.Printf("  局域网 %s 转给 %s\n", t.LAN, t.To)
	}
	if len(d.Left) > 0 {
		fmt.Printf("  退出局域网 %v\n", d.Left)
	}
	if len(d.RelaysInUse) > 0 {
		fmt.Printf("  中转站 %v 别人正在用：他们的设备会改走公共中转站\n", d.RelaysInUse)
	}
	switch {
	case len(d.Blocked) > 0:
		fmt.Printf("还注销不了：它是局域网 %v 的拥有者，里面还有别人。加 -lans transfer（转给最早加入的成员）或 -lans delete（整个删掉）\n", d.Blocked)
		return 1
	case d.DryRun:
		fmt.Println("还没删：确认无误，加 -yes 再跑一次")
	}
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
