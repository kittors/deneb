//go:build !linux

package main

// trayHost: the menu bar and the notification area are always there.
func trayHost() bool { return true }
