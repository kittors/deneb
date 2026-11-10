package main

import (
	"fmt"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// checkServers: two servers on public addresses, build and nas (dnb-net, no
// NAT), go direct between them. With UDP from nas to build dropped on the
// way, as from hk-spare to hk-155 on 11-10 (the relay's STUN and a bare
// datagram lost too, TCP and ICMP through), they stay on the relay, and the
// counters tell which way is lost: nas hears build's pings, build hears
// nothing from nas. Once UDP goes through again, they go direct by
// themselves.
func checkServers() error {
	if out, err := deneb("build", "lan", "add", "周末联机"); err != nil {
		return fmt.Errorf("build lan add 周末联机: %v %s", err, out)
	}
	defer deneb("build", "lan", "remove", "周末联机")
	var buildIP string
	for _, n := range nodes {
		if n.name == "build" {
			buildIP = n.ip
		}
	}
	busy := func() { deneb("build", "ping", "nas", "-c", "2") } // keeps the line awake
	both := func(kind apitype.PathKind, within time.Duration) error {
		_, err := waitFor(within, func() (string, error) {
			busy()
			for _, c := range [][2]string{{"build", "nas"}, {"nas", "build"}} {
				p, err := pathKind(c[0], c[1])
				if err != nil {
					return "", err
				}
				if (p.Kind == apitype.PathDirect) != (kind == apitype.PathDirect) {
					return "", fmt.Errorf("%s -> %s is %s, want %s", c[0], c[1], p.Kind, kind)
				}
			}
			return "", nil
		})
		return err
	}
	start := time.Now()
	if err := both(apitype.PathDirect, 20*time.Second); err != nil {
		return fmt.Errorf("two servers on public addresses: %w", err)
	}
	firstDirect := time.Since(start)

	rule := []string{"OUTPUT", "-p", "udp", "-d", buildIP, "-j", "DROP"}
	if out, err := docker(append([]string{"exec", "dnb-nas", "iptables", "-I"}, rule...)...); err != nil {
		return fmt.Errorf("drop UDP from nas to build: %v %s", err, out)
	}
	blocked := true
	unblock := func() {
		if blocked {
			docker(append([]string{"exec", "dnb-nas", "iptables", "-D"}, rule...)...)
			blocked = false
		}
	}
	defer unblock()
	if err := both(apitype.PathRelay, 30*time.Second); err != nil {
		return fmt.Errorf("with UDP from nas to build dropped: %w", err)
	}
	b0, err1 := debugPath("build", "nas")
	n0, err2 := debugPath("nas", "build")
	for range 3 {
		busy()
	}
	b1, err3 := debugPath("build", "nas")
	n1, err4 := debugPath("nas", "build")
	for _, err := range []error{err1, err2, err3, err4} {
		if err != nil {
			return err
		}
	}
	if n1.PingsRx <= n0.PingsRx {
		return fmt.Errorf("nas heard none of build's pings (%d, then %d)", n0.PingsRx, n1.PingsRx)
	}
	if b1.PingsRx != b0.PingsRx || b1.PongsRx != b0.PongsRx {
		return fmt.Errorf("build heard nas with its UDP dropped: pings %d -> %d, answers %d -> %d", b0.PingsRx, b1.PingsRx, b0.PongsRx, b1.PongsRx)
	}

	unblock()
	back := time.Now()
	if err := both(apitype.PathDirect, 30*time.Second); err != nil {
		return fmt.Errorf("after UDP from nas came back: %w", err)
	}
	fmt.Printf("     servers: direct between build and nas in %s; with UDP from nas dropped, the relay, nas hearing %d of build's pings and build none of nas; direct again %s after it came back\n",
		firstDirect.Round(100*time.Millisecond), n1.PingsRx-n0.PingsRx, time.Since(back).Round(100*time.Millisecond))
	return nil
}
