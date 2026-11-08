// Command deneb-poc is a throwaway prototype for one question: can a single
// device be online in two isolated LANs at the same time, using one WireGuard
// identity, one UDP port and one relay connection, while members of one LAN
// cannot use that device to reach the other LAN?
//
// It is deliberately small and runs entirely in user space (gVisor netstack),
// so it creates no network interfaces, routes, DNS settings or firewall rules
// on any machine it runs on.
package main

import (
	"fmt"
	"log"
	"os"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "gen":
		cmdGen(os.Args[2:])
	case "node":
		cmdNode(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  deneb-poc gen  -topology topology.json -out configs/
  deneb-poc node -config configs/mac.me.json [-api 127.0.0.1:47777] [-socks 127.0.0.1:41080] [-bind-if en0]`)
	os.Exit(2)
}
