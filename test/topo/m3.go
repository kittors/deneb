package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var shareIDRE = regexp.MustCompile(`"share":"([^"]+)"`)

// checkShare is M3's share: kittors shares build (its tick service) with
// jie. jie's devices, which share no LAN with build, reach it through the
// share and only that way; build reaches none of them (their side has
// NoInbound; an M1 receiver does not know it and still answers a ping).
// Revoking takes it away at once.
func checkShare() error {
	issuer := "mac" // any of kittors's devices may share build
	if isM1(issuer) {
		issuer = "build"
	}
	if isM1(issuer) {
		return errors.New("both of kittors's devices run M1")
	}
	sb, err := state("build")
	if err != nil {
		return err
	}
	sd, err := state("desktop")
	if err != nil {
		return err
	}
	// How soon the share is usable: desktop has no LAN with build, so
	// build is new to it (the map, WireGuard, a handshake, a path). A TCP
	// connect to the shared port every 100 ms from just before the share.
	probeOut := make(chan string, 1)
	go func() {
		out, _ := deneb("desktop", "debug", "probe", "build", "tcp", "7000", "100ms", "12")
		probeOut <- out
	}()
	time.Sleep(500 * time.Millisecond)
	shared := time.Now()
	out, err := deneb(issuer, "op", "share.create", fmt.Sprintf(`{"device":%q,"person":%q,"services":["tcp/7000"]}`, sb.SelfDeviceID, sd.User))
	if err != nil {
		return fmt.Errorf("share.create: %v %s", err, out)
	}
	usable := firstOK(<-probeOut, shared)
	m := shareIDRE.FindStringSubmatch(out)
	if m == nil {
		return fmt.Errorf("share.create gave no share id: %s", out)
	}
	revoked := false
	defer func() {
		if !revoked {
			deneb(issuer, "op", "share.revoke", fmt.Sprintf(`{"share":%q}`, m[1]))
		}
	}()
	for _, n := range []string{"desktop", "nas"} {
		var r tickResult
		if _, err := waitFor(6*time.Second, func() (string, error) {
			var err error
			r, err = tick(n, "build", 2)
			if err == nil && (r.Broken || r.Lines < 10) {
				err = fmt.Errorf("%+v", r)
			}
			return "", err
		}); err != nil {
			return fmt.Errorf("%s cannot use build's tick service through the share: %v", n, err)
		}
	}
	var closed []string
	for _, n := range []string{"desktop", "nas"} {
		if isM1(n) {
			continue // an M1 receiver goes by the empty Open only: ping still comes in
		}
		if err := pingFails("build", n); err != nil {
			return fmt.Errorf("through the share, the other way: %w", err)
		}
		closed = append(closed, n)
	}
	if s, err := state("desktop"); err == nil && !isM1("desktop") {
		ok := false
		for _, sc := range s.Scopes {
			if strings.HasPrefix(string(sc.ID), "share/") && sc.Kind == "share" && sc.NoInbound {
				ok = true
			}
		}
		if !ok {
			return fmt.Errorf("desktop's scopes do not show the share as only-out: %+v", s.Scopes)
		}
	}
	// A connection open across the revoke ends at once, with resets from
	// the side that cut it, as an open SSH session should close rather
	// than freeze (Web line, 11-09).
	open := tickAsync("desktop", "build", 8)
	time.Sleep(2 * time.Second)
	revoked = true
	if out, err := deneb(issuer, "op", "share.revoke", fmt.Sprintf(`{"share":%q}`, m[1])); err != nil {
		return fmt.Errorf("share.revoke: %v %s", err, out)
	}
	t0 := time.Now()
	or, oerr := open()
	openEnded := time.Since(t0)
	if !isM1("desktop") && (oerr != nil || !or.Broken || openEnded > 2*time.Second) {
		return fmt.Errorf("the connection open across the revoke was not reset at once: %+v %v, ended %s after the revoke", or, oerr, openEnded.Round(100*time.Millisecond))
	}
	if _, err := waitFor(5*time.Second, func() (string, error) {
		if _, err := ping("desktop", "build", 1); err == nil {
			return "", errors.New("desktop still reaches build")
		}
		return "", nil
	}); err != nil {
		return fmt.Errorf("after the revoke: %w", err)
	}
	fmt.Printf("     share: desktop reached build's shared port %s after share.create; jie's desktop and nas use build's tick through the share; build cannot ping %v; revoked: the open connection ended %s after (%s), new ones gone after %s\n",
		usable, closed, openEnded.Round(100*time.Millisecond), or.Error, time.Since(t0).Round(100*time.Millisecond))
	return nil
}

// firstOK is how long after t0 the first successful line of a debug probe
// came ("HH:MM:SS.mmm ok …", UTC as the engines log), or "never".
func firstOK(out string, t0 time.Time) string {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || f[1] != "ok" {
			continue
		}
		at, err := time.Parse("15:04:05.000", f[0])
		if err != nil {
			continue
		}
		u := t0.UTC()
		at = time.Date(u.Year(), u.Month(), u.Day(), at.Hour(), at.Minute(), at.Second(), at.Nanosecond(), time.UTC)
		return at.Sub(u).Round(100 * time.Millisecond).String()
	}
	return "never (in 12 s)"
}
