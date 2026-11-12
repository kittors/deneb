//go:build !darwin && !linux

package main

// rollLog: on Windows the service rolls its own log as it starts
// (openServiceLog). The log of an engine the window starts there, which
// runs until the adapter is installed, is not rolled yet.
func rollLog(string) {}
