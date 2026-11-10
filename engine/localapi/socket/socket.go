// Package socket finds, listens on and dials the engine's local API endpoint:
// a Unix socket on macOS and Linux, a named pipe on Windows. Only the system
// user who runs Deneb (and the administrator) may connect (plan §2.6.2).
package socket

import (
	"context"
	"errors"
	"net"
	"os"
)

// ErrNotOurs: the endpoint is there but is not this user's to talk to (on
// Windows, a pipe someone else owns where this user's engine would be).
var ErrNotOurs = errors.New("the engine's endpoint here belongs to someone else")

// EnvPath overrides every default. The client's tests and the dev window
// use it so that a fake engine never touches the real path.
const EnvPath = "DENEB_SOCKET"

// Path is where a client should connect: $DENEB_SOCKET if set; otherwise the
// system daemon's endpoint (kernel mode) if one is listening; otherwise this
// user's own (user-space mode).
func Path() string {
	if p := os.Getenv(EnvPath); p != "" {
		return p
	}
	if p := SystemPath(); p != "" && exists(p) {
		return p
	}
	return UserPath()
}

// SystemPath is the system daemon's endpoint, where a kernel-mode engine
// (root) listens.
func SystemPath() string {
	if pathOverride.system != "" {
		return pathOverride.system
	}
	return systemPath()
}

// UserPath is where a user-space engine of this user listens.
func UserPath() string {
	if pathOverride.user != "" {
		return pathOverride.user
	}
	return userPath()
}

var pathOverride struct{ system, user string }

// SetPathsForTest points SystemPath and UserPath at other files until
// restore is called. Tests only; not safe for concurrent use.
func SetPathsForTest(system, user string) (restore func()) {
	old := pathOverride
	pathOverride.system, pathOverride.user = system, user
	return func() { pathOverride = old }
}

// Exists reports whether an endpoint file is there (a Unix socket file; on
// Windows, a named pipe that is currently served).
func Exists(p string) bool { return exists(p) }

// ListenPath is where an engine should listen when no path is given.
func ListenPath() string {
	if p := os.Getenv(EnvPath); p != "" {
		return p
	}
	if isPrivileged() {
		return SystemPath()
	}
	return UserPath()
}

// ListenOptions say who besides the engine's own user may connect.
type ListenOptions struct {
	// AllowUID lets one more Unix user in: a kernel-mode engine runs as
	// root on behalf of the person logged in to the desktop. -1 for none.
	AllowUID int
	// AllowUser is the same on Windows, where the Deneb service runs as
	// SYSTEM: a SID ("S-1-5-21-…") or an account name ("PC\jie"). Empty
	// for none.
	AllowUser string
}

// Listen creates the endpoint, replacing a stale socket file left by a
// crashed engine, and rejects connections from other users.
func Listen(path string, opts ListenOptions) (net.Listener, error) {
	return listen(path, opts)
}

// Dial connects to the endpoint.
func Dial(ctx context.Context, path string) (net.Conn, error) {
	return dial(ctx, path)
}
