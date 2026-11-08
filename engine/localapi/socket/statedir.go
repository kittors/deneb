package socket

import (
	"os"
	"path/filepath"
	"runtime"
)

// StateDir is where an engine keeps its keys and last network map:
// $DENEB_STATE_DIR, else per user for a user-space engine and system-wide
// for a root one. The engine log (denebd.log) goes there too.
func StateDir() string {
	if d := os.Getenv("DENEB_STATE_DIR"); d != "" {
		return d
	}
	root := isPrivileged()
	switch runtime.GOOS {
	case "darwin":
		if root {
			return "/Library/Application Support/Deneb/engine"
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Library", "Application Support", "Deneb", "engine")
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "Deneb")
		}
	}
	if root {
		return "/var/lib/deneb"
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "deneb")
}
