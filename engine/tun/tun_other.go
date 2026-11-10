//go:build !darwin && !linux && !windows

package tun

import (
	"errors"
	"net/netip"
)

// ErrUnsupported: kernel mode exists on macOS, Linux and Windows.
var ErrUnsupported = errors.New("kernel mode is not available on this system; use -mode userspace")

func Open(Config, func(string, ...any)) (*Kernel, error) { return nil, ErrUnsupported }

type platform struct{}

func (k *Kernel) Close(func(string, ...any)) {}

func (k *Kernel) SetPeers([]netip.Addr, func(string, ...any)) {}

func undo(*Undo, func(string, ...any)) {}

func Plan(Config, []netip.Addr) ([]string, error) { return nil, ErrUnsupported }

func Preflight(block netip.Prefix, _ func(netip.Addr) (int, string, error)) error {
	return CheckBlock(block)
}
