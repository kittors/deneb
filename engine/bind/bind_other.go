//go:build !darwin && !linux && !windows

package bind

import (
	"errors"
	"net"
)

func pin(uintptr, *net.Interface, string) error {
	return errors.New("pinning sockets to an interface is not implemented on this system")
}
