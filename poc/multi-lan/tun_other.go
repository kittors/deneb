//go:build !darwin

package main

import (
	"errors"
	"net/netip"

	"golang.zx2c4.com/wireguard/tun"
)

// Kernel-TUN mode is only wired up for the Mac experiment; the remote test
// nodes always run in user space.
func preflightKernelTUN(netip.Prefix) error {
	return errors.New("-tun is only implemented on macOS in this prototype")
}

func setupKernelTUN(netip.Addr, netip.Prefix, int) (tun.Device, func(), error) {
	return nil, nil, errors.New("-tun is only implemented on macOS in this prototype")
}
