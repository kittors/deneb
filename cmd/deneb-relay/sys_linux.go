package main

import (
	"bufio"
	"encoding/hex"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ipv6Flags are the flags of this machine's IPv6 addresses, from
// /proc/net/if_inet6: "20010db8…0001 02 40 00 80 eth0" is the address,
// interface index, prefix length, scope and flags (hex), and name.
func ipv6Flags() map[netip.Addr]uint32 {
	out := map[netip.Addr]uint32{}
	f, err := os.Open("/proc/net/if_inet6")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) < 6 || len(fl[0]) != 32 {
			continue
		}
		b, err := hex.DecodeString(fl[0])
		if err != nil {
			continue
		}
		v, err := strconv.ParseUint(fl[4], 16, 32)
		if err != nil {
			continue
		}
		out[netip.AddrFrom16([16]byte(b))] = uint32(v)
	}
	return out
}

// defaultRouteIface is the interface of the IPv4 default route.
func defaultRouteIface() string {
	_, iface := defaultGateway()
	return iface
}

// defaultGateway is the IPv4 default route's gateway and interface, from
// /proc/net/route: the line whose destination is 00000000, its gateway in
// hex, the bytes in the machine's order (little-endian on what Deneb
// runs on).
func defaultGateway() (netip.Addr, string) {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return netip.Addr{}, ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fl := strings.Fields(sc.Text())
		if len(fl) < 3 || fl[1] != "00000000" || fl[2] == "00000000" {
			continue
		}
		b, err := hex.DecodeString(fl[2])
		if err != nil || len(b) != 4 {
			continue
		}
		return netip.AddrFrom4([4]byte{b[3], b[2], b[1], b[0]}), fl[0]
	}
	return netip.Addr{}, ""
}

// portOwner names the program listening on port (proto "tcp" or "udp"),
// for "47445 被 nginx 占了": the socket's inode from /proc/net, then the
// process holding it. "" when it cannot tell (another user's process,
// without root).
func portOwner(proto string, port int) string {
	inodes := map[string]bool{}
	for _, file := range []string{proto, proto + "6"} {
		f, err := os.Open("/proc/net/" + file)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Scan() // the header
		for sc.Scan() {
			fl := strings.Fields(sc.Text())
			if len(fl) < 10 {
				continue
			}
			_, hexPort, ok := strings.Cut(fl[1], ":")
			p, err := strconv.ParseUint(hexPort, 16, 16)
			if !ok || err != nil || int(p) != port {
				continue
			}
			// TCP: listening (0A). UDP: bound, not connected (07).
			if proto == "tcp" && fl[3] != "0A" || proto == "udp" && fl[3] != "07" {
				continue
			}
			inodes["socket:["+fl[9]+"]"] = true
		}
		f.Close()
	}
	if len(inodes) == 0 {
		return ""
	}
	procs, _ := os.ReadDir("/proc")
	for _, p := range procs {
		if _, err := strconv.Atoi(p.Name()); err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", p.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			if link, err := os.Readlink(filepath.Join("/proc", p.Name(), "fd", fd.Name())); err == nil && inodes[link] {
				comm, _ := os.ReadFile(filepath.Join("/proc", p.Name(), "comm"))
				return strings.TrimSpace(string(comm))
			}
		}
	}
	return ""
}
