package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/kittors/deneb/proto"
	"tailscale.com/net/stun"
)

// Check is one thing the watchdog looks at.
type Check struct {
	// Name is what the messages call it: "hk-155 的控制服务器".
	Name string `json:"name"`
	// Kind is https (a URL answers 200: the control server's /healthz),
	// derp (a relay answers /derp/probe over TLS), stun (a relay answers a
	// STUN request), backup (the newest backup in Dir is younger than
	// MaxAge) or disk (Dir's file system has MinFree of it left).
	Kind string `json:"kind"`
	URL  string `json:"url,omitempty"`  // https
	Addr string `json:"addr,omitempty"` // derp, stun: host:port
	// Pin is the certificate's key (sha256:…, as proto.Fingerprint
	// writes it) for a server with a certificate no CA signed: the control
	// server, a Deneb relay. Without one the certificate is checked against
	// the system's CAs, for ServerName if it is given (a derper reached by
	// its address).
	Pin        string  `json:"pin,omitempty"`
	ServerName string  `json:"serverName,omitempty"`
	Dir        string  `json:"dir,omitempty"`     // backup, disk
	MaxAge     string  `json:"maxAge,omitempty"`  // backup: default 26h
	MinFree    float64 `json:"minFree,omitempty"` // disk: default 0.15

	tls    *tls.Config
	maxAge time.Duration
}

// checkTimeout bounds one check.
const checkTimeout = 10 * time.Second

// testRoots are the CAs to trust instead of the system's: tests only.
var testRoots *x509.CertPool

// prepare checks the settings and fills in the defaults.
func (c *Check) prepare() error {
	if c.Name == "" {
		return errors.New("no name")
	}
	needAddr := func() error {
		if _, _, err := net.SplitHostPort(c.Addr); err != nil {
			return fmt.Errorf("addr %q: want host:port", c.Addr)
		}
		return nil
	}
	switch c.Kind {
	case "https":
		u, err := url.Parse(c.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("url %q: want https://…", c.URL)
		}
		if c.ServerName == "" {
			c.ServerName = u.Hostname()
		}
	case "derp":
		if err := needAddr(); err != nil {
			return err
		}
		if c.ServerName == "" {
			c.ServerName, _, _ = net.SplitHostPort(c.Addr)
		}
	case "stun":
		return needAddr()
	case "backup", "disk":
		if c.Dir == "" {
			return errors.New("no dir")
		}
		if c.Kind == "backup" {
			c.maxAge = 26 * time.Hour
			if c.MaxAge != "" {
				d, err := time.ParseDuration(c.MaxAge)
				if err != nil || d <= 0 {
					return fmt.Errorf("maxAge %q", c.MaxAge)
				}
				c.maxAge = d
			}
		} else if c.MinFree == 0 {
			c.MinFree = 0.15
		}
		return nil
	default:
		return fmt.Errorf("kind %q: want https, derp, stun, backup or disk", c.Kind)
	}
	if c.Pin != "" {
		cfg, err := proto.PinnedTLSConfig(c.Pin)
		if err != nil {
			return err
		}
		c.tls = cfg
	} else {
		c.tls = &tls.Config{ServerName: c.ServerName, MinVersion: tls.VersionTLS12, RootCAs: testRoots}
	}
	return nil
}

// run makes the check once: nil if all is well.
func (c *Check) run(ctx context.Context) error {
	switch c.Kind {
	case "https":
		return c.get(ctx, c.URL, "")
	case "derp":
		// Ask for the probe by the name the certificate has, at the address.
		return c.get(ctx, "https://"+net.JoinHostPort(c.ServerName, port(c.Addr))+"/derp/probe", c.Addr)
	case "stun":
		return c.stun(ctx)
	case "backup":
		return c.backup()
	case "disk":
		free, total, err := diskFree(c.Dir)
		if err != nil {
			return err
		}
		if total == 0 || float64(free)/float64(total) < c.MinFree {
			return fmt.Errorf("磁盘只剩 %.0f%%", 100*float64(free)/float64(max(total, 1)))
		}
		return nil
	}
	return fmt.Errorf("kind %q", c.Kind)
}

func port(addr string) string {
	_, p, _ := net.SplitHostPort(addr)
	return p
}

// get asks for u and wants a 200; dial, if set, is where to connect.
func (c *Check) get(ctx context.Context, u, dial string) error {
	tr := &http.Transport{TLSClientConfig: c.tls.Clone(), DisableKeepAlives: true, ForceAttemptHTTP2: false}
	if dial != "" {
		d := &net.Dialer{}
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, dial)
		}
	}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return plain(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("回答 %s", resp.Status)
	}
	return nil
}

// stun sends a STUN request, three times at most, and wants its answer.
func (c *Check) stun(ctx context.Context) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", c.Addr)
	if err != nil {
		return plain(err)
	}
	defer conn.Close()
	buf := make([]byte, 1500)
	for range 3 {
		tx := stun.NewTxID()
		if _, err := conn.Write(stun.Request(tx)); err != nil {
			return plain(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
			deadline = dl
		}
		conn.SetReadDeadline(deadline)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				break // timed out: ask again
			}
			if got, _, err := stun.ParseResponse(buf[:n]); err == nil && got == tx {
				return nil
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	return errors.New("STUN 没有回答")
}

// backup wants the newest backup (deneb-YYYYMMDD.db) younger than maxAge.
func (c *Check) backup() error {
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return plain(err)
	}
	var newest time.Time
	var name string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, "deneb-") || !strings.HasSuffix(n, ".db") {
			continue
		}
		if fi, err := e.Info(); err == nil && fi.ModTime().After(newest) {
			newest, name = fi.ModTime(), n
		}
	}
	if name == "" {
		return errors.New("一份备份都没有")
	}
	if age := time.Since(newest); age > c.maxAge {
		return fmt.Errorf("最新的一份 %s 已经 %d 小时了", name, int(age/time.Hour))
	}
	return nil
}

// plain is an error as the messages say it: the common ones in words.
func plain(err error) error {
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout(), errors.Is(err, context.DeadlineExceeded):
		return errors.New("超时，没有回应")
	case errors.Is(err, syscall.ECONNREFUSED):
		return errors.New("连接被拒绝：端口上没有在听")
	case errors.Is(err, os.ErrPermission):
		return errors.New("没有权限读")
	}
	var ce *tls.CertificateVerificationError
	if errors.As(err, &ce) || strings.Contains(err.Error(), "pinned") || strings.Contains(err.Error(), "certificate") {
		return fmt.Errorf("证书对不上：%v", err)
	}
	return err
}
