//go:build !linux

package main

import "net/netip"

func ipv6Flags() map[netip.Addr]uint32 { return nil }

func defaultRouteIface() string { return "" }

func defaultGateway() (netip.Addr, string) { return netip.Addr{}, "" }

func portOwner(string, int) string { return "" }
