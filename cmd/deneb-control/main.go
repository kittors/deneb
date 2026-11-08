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
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kittors/deneb/control"
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

	srv, err := control.New(control.Config{
		DataDir:       *dataDir,
		PublicURL:     *publicURL,
		Dev:           *dev,
		Seed:          seed,
		LoginLifetime: *lifetime,
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
		hosts := []string{pu.Hostname(), "127.0.0.1", "localhost"}
		if *tlsHosts != "" {
			hosts = append(hosts, strings.Split(*tlsHosts, ",")...)
		}
		var cert atomic.Pointer[tls.Certificate]
		load := func() (string, error) {
			c, pin, err := control.LoadOrCreateCert(*dataDir, hosts)
			if err == nil {
				cert.Store(&c)
			}
			return pin, err
		}
		pin, err := load()
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("certificate pin %s (also in %s)", pin, filepath.Join(*dataDir, "tls", "pin.txt"))
		go func() {
			// Remake the certificate before it runs out; the key, and so
			// the pin, stay the same.
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(24 * time.Hour):
					if _, err := load(); err != nil {
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
