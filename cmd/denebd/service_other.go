//go:build !windows

package main

import (
	"errors"
	"os"
)

func isWindowsService() bool { return false }

func runService([]string) error { return errors.New("not a Windows service") }

func cmdService([]string) error {
	return errors.New("denebd service is Windows only; on Linux the packages bring denebd.service (systemd)")
}

func isAdmin() bool { return os.Geteuid() == 0 }

// systemServiceRunning: only Windows has a service that takes the user's
// identity (`service install -import-state`).
func systemServiceRunning() bool { return false }

// preflightService: only Windows has `service install`.
func preflightService() []string { return nil }
