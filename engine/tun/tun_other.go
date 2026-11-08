//go:build !darwin

package tun

import (
	"errors"
	"net/netip"
)

// ErrUnsupported: kernel mode on Windows and Linux comes in M5.
var ErrUnsupported = errors.New("kernel mode is macOS-only in M1; use -mode userspace")

func Open(Config, func(string, ...any)) (*Kernel, error) { return nil, ErrUnsupported }

func (k *Kernel) Close(func(string, ...any)) {}

func undo(*Undo, func(string, ...any)) {}

func Preflight(block netip.Prefix, _ func(netip.Addr) (int, string, error)) error {
	return CheckBlock(block)
}
