//go:build !windows

package main

// trayHidden: only Windows keeps the mark out of sight, in its "^".
func trayHidden() bool { return false }
