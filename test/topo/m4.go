package main

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var (
	relayIDRE    = regexp.MustCompile(`"relay":"([^"]+)"`)
	relayTokenRE = regexp.MustCompile(`"relayToken":"([^"]+)"`)
)

// scopeRelay is the relay node n keeps for the LAN named lan (State's
// scopes[].relay, M4).
func scopeRelay(n, lan string) (string, error) {
	s, err := state(n)
	if err != nil {
		return "", err
	}
	for _, sc := range s.Scopes {
		if sc.Name == lan {
			return string(sc.Relay), nil
		}
	}
	return "", fmt.Errorf("%s is not in %s", n, lan)
}

// checkOwnRelay is M4's "我的服务器": kittors registers a relay of his own
// (cmd/deneb-relay: admission by the control server, a self-signed
// certificate the engines trust by its pin) and drags it into 工作室. The
// Mac and build keep it for 工作室; with UDP between them blocked they
// reach each other through it. When it stops, 工作室 falls back to the
// public relay ("改走公共中转站"); when it is back, to it again.
func checkOwnRelay() error {
	out, err := deneb("mac", "op", "relay.create", `{"name":"我的服务器"}`)
	if err != nil {
		return fmt.Errorf("relay.create: %v %s", err, out)
	}
	idm, tokm := relayIDRE.FindStringSubmatch(out), relayTokenRE.FindStringSubmatch(out)
	if idm == nil || tokm == nil {
		return fmt.Errorf("relay.create gave no relay or token: %s", out)
	}
	id := idm[1]
	defer func() {
		docker("rm", "-f", "dnb-relay1")
		deneb("mac", "op", "relay.delete", fmt.Sprintf(`{"relay":%q}`, id))
	}()
	if _, err := docker("run", "-d", "--name", "dnb-relay1", "--hostname", "relay1", "--network", "dnb-net", "--ip", "198.51.100.5",
		"-e", "DENEB_RELAY_TOKEN="+tokm[1], image, "relay",
		"-control", "https://"+ctlIP+":"+ctlPort, "-control-pin", controlPin, "-data", "/var/lib/deneb-relay",
		"-listen", ":47445", "-stun", ":47478", "-ipv4", "198.51.100.5", "-sync", "5s"); err != nil {
		return fmt.Errorf("starting the relay: %w", err)
	}
	s, err := state("mac")
	if err != nil {
		return err
	}
	lan := ""
	for _, sc := range s.Scopes {
		if sc.Name == "工作室" {
			lan = string(sc.ID)
		}
	}
	if lan == "" {
		return errors.New("the Mac is not in 工作室")
	}
	lanID := lan[len("lan/"):]
	if out, err := waitFor(20*time.Second, func() (string, error) {
		return deneb("mac", "op", "relay.add", fmt.Sprintf(`{"lan":%q,"relay":%q}`, lanID, id))
	}); err != nil {
		return fmt.Errorf("relay.add: %v %s", err, out)
	}
	t0 := time.Now()
	for _, n := range []string{"mac", "build"} {
		if _, err := waitFor(30*time.Second, func() (string, error) {
			r, err := scopeRelay(n, "工作室")
			if err == nil && r != id {
				err = fmt.Errorf("%s keeps %q for 工作室, want %s", n, r, id)
			}
			return "", err
		}); err != nil {
			return err
		}
	}
	onOwn := time.Since(t0)
	for _, c := range [][2]string{{"mac", "build"}, {"build", "mac"}} {
		deneb(c[0], "debug", "block", c[1])
		defer deneb(c[0], "debug", "unblock", c[1])
	}
	// Traffic, or the idle line would keep showing direct. build's report
	// of its relays reaches the Mac's map a moment later (Peer.Relays):
	// then the Mac sends through the own relay too.
	if _, err := waitFor(30*time.Second, func() (string, error) {
		ping("mac", "build", 1)
		p, err := debugPath("mac", "build")
		if err == nil && (p.Kind != "relay" || p.Relay != id) {
			err = fmt.Errorf("the Mac reaches build %s through %q, want the relay %s", p.Kind, p.Relay, id)
		}
		return "", err
	}); err != nil {
		return err
	}
	if err := pingOK("mac", "build", 5*time.Second); err != nil {
		return fmt.Errorf("through the own relay: %w", err)
	}
	// It stops: 工作室 falls back to the public relay, and still works.
	if _, err := docker("stop", "dnb-relay1"); err != nil {
		return err
	}
	t1 := time.Now()
	if _, err := waitFor(30*time.Second, func() (string, error) {
		r, err := scopeRelay("mac", "工作室")
		if err == nil && (r == id || r == "") {
			err = fmt.Errorf("still %q", r)
		}
		return r, err
	}); err != nil {
		return fmt.Errorf("工作室 did not fall back when the own relay stopped: %w", err)
	}
	fellBack := time.Since(t1)
	if err := pingOK("mac", "build", 15*time.Second); err != nil {
		return fmt.Errorf("after falling back: %w", err)
	}
	// It is back: so is 工作室.
	if _, err := docker("start", "dnb-relay1"); err != nil {
		return err
	}
	t2 := time.Now()
	if _, err := waitFor(90*time.Second, func() (string, error) {
		r, err := scopeRelay("mac", "工作室")
		if err == nil && r != id {
			err = fmt.Errorf("still %q", r)
		}
		return r, err
	}); err != nil {
		return fmt.Errorf("工作室 did not come back to the own relay: %w", err)
	}
	back := time.Since(t2)
	fmt.Printf("     own-relay: 工作室 on kittors's relay %s after it was dragged in; build reached through it with UDP blocked; relay stopped: fell back in %s; started again: back in %s\n",
		onOwn.Round(100*time.Millisecond), fellBack.Round(100*time.Millisecond), back.Round(100*time.Millisecond))
	return nil
}
