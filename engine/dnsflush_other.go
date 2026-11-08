//go:build !darwin

package engine

// flushSystemDNSCaches: kernel mode, the only user, is macOS-only for now.
func flushSystemDNSCaches() error { return nil }
