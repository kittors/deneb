package main

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// trayHost reports whether a panel here shows AppIndicators: whether
// something owns org.kde.StatusNotifierWatcher on the session bus, as
// GNOME's AppIndicator extension and KDE's and XFCE's panels do. GNOME as
// it comes has none, and an indicator there shows nowhere. It asks with
// gdbus, GLib's, which a GTK desktop has; without it, it says there is one.
func trayHost() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gdbus", "call", "--session", "--dest", "org.freedesktop.DBus",
		"--object-path", "/org/freedesktop/DBus", "--method", "org.freedesktop.DBus.NameHasOwner",
		"org.kde.StatusNotifierWatcher").Output()
	if err != nil {
		return true
	}
	return !strings.Contains(string(out), "false")
}
