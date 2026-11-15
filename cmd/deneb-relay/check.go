package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// The checks install and status make (design/plan/m7/relay-install.md
// §5.1): each says what it found in one line, and what to do about it.

// clockSkew is how far this machine's clock is from the control
// server's, from the Date of its answer: positive when this one is ahead.
func clockSkew(ctx context.Context, ctl *controlClient) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", ctl.base+"/", nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	resp, err := ctl.hc.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	server, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		return 0, fmt.Errorf("Deneb 服务器的回答里没有时间")
	}
	// The Date is whole seconds, taken somewhere within the round trip.
	mid := start.Add(time.Since(start) / 2)
	return mid.Sub(server).Round(time.Second), nil
}

// Clock skews: past skewWarn it is said, past skewBad it is a failure (a
// clock hours off is one without a battery, which TLS will soon trip on).
const (
	skewWarn = 2 * time.Minute
	skewBad  = 10 * time.Minute
)

// skewWords says a skew in words: "快 2 小时 3 分".
func skewWords(d time.Duration) string {
	way := "快"
	if d < 0 {
		way, d = "慢", -d
	}
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%s %d 天", way, int(d/(24*time.Hour)))
	case d >= time.Hour:
		return fmt.Sprintf("%s %d 小时 %d 分", way, int(d/time.Hour), int(d%time.Hour/time.Minute))
	case d >= time.Minute:
		return fmt.Sprintf("%s %d 分", way, int(d/time.Minute))
	}
	return fmt.Sprintf("%s %d 秒", way, int(d/time.Second))
}

// diskNeed is the room the relay needs where its program goes: the
// program, an update beside it and the one it replaced, and its data and
// logs.
func diskNeed(binSize int64) uint64 {
	return uint64(3*binSize) + 40<<20
}

// existingDir is p, or its nearest directory that exists: where the free
// space of a directory not made yet is measured.
func existingDir(p string) string {
	for {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

func mb(n uint64) string {
	if n >= 10<<30 {
		return fmt.Sprintf("%.0f GB", float64(n)/(1<<30))
	}
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%d MB", n>>20)
}

// tcpFree and udpFree: nothing listens on the port, on any address.
func tcpFree(port int) bool {
	l, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

func udpFree(port int) bool {
	c, err := net.ListenPacket("udp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// pickPorts is the TCP and UDP port the relay listens on: the ones asked
// for if both are free, else the next pair up that is (design §5.2).
func pickPorts(tcp, udp int, tcpOK, udpOK func(int) bool) (int, int, bool) {
	for i := 0; i < 50; i++ {
		t, u := tcp+i, udp+i
		if t > 65535 || u > 65535 {
			break
		}
		if tcpOK(t) && udpOK(u) {
			return t, u, true
		}
	}
	return 0, 0, false
}

// portOf is the port of a listen address, ":47445".
func portOf(addr string, def int) int {
	if addr == "" {
		return def
	}
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return def
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return def
	}
	return n
}

// proxyInUse is the proxy the environment names for HTTPS
// ($HTTPS_PROXY), without its password; "" for none.
func proxyInUse() string {
	u, err := http.ProxyFromEnvironment(&http.Request{URL: &url.URL{Scheme: "https", Host: "deneb.app"}})
	if err != nil || u == nil {
		return ""
	}
	return u.Redacted()
}
