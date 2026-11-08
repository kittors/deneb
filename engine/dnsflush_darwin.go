//go:build darwin

package engine

import "os/exec"

// flushSystemDNSCaches empties macOS's own DNS caches: the directory
// service's (getaddrinfo answers from it too) and mDNSResponder's. Kernel
// mode runs as root, which this needs.
func flushSystemDNSCaches() error {
	if err := exec.Command("dscacheutil", "-flushcache").Run(); err != nil {
		return err
	}
	return exec.Command("killall", "-HUP", "mDNSResponder").Run()
}
