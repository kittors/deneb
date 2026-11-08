//go:build !windows && !darwin && !linux

package socket

import (
	"errors"
	"net"
)

func peerUID(*net.UnixConn) (int, error) {
	return -1, errors.New("peer credentials are not implemented on this system")
}
