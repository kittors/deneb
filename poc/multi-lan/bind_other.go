//go:build !darwin

package main

import (
	"errors"
	"syscall"
)

// The prototype only needs interface pinning on the Mac (to step around the
// local Clash TUN). Linux would use SO_BINDTODEVICE, Windows IP_UNICAST_IF.
func bindControl(string) (func(network, address string, c syscall.RawConn) error, error) {
	return nil, errors.New("-bind-if is only implemented on macOS in this prototype")
}
