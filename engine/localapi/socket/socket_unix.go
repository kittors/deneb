//go:build !windows

package socket

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

// systemPath is where a root engine listens. The desktop app looks here
// first, so a kernel-mode daemon wins over a leftover user-space one.
func systemPath() string { return "/var/run/deneb/denebd.sock" }

// userPath is where a user-space engine started by the desktop app (or by
// hand) listens. It sits in the user's own directory, so the file system
// already keeps other users out; the peer check below is a second lock.
func userPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), fmt.Sprintf("denebd-%d.sock", os.Getuid()))
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Deneb", "denebd.sock")
	}
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "deneb", "denebd.sock")
	}
	return filepath.Join(home, ".local", "share", "deneb", "denebd.sock")
}

func isPrivileged() bool { return os.Geteuid() == 0 }

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func listen(path string, opts ListenOptions) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// A socket file outlives a crashed process. Remove it only if nothing
	// answers on it, so a second engine cannot steal a live one's endpoint.
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		return nil, fmt.Errorf("another engine is already listening on %s", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	mode := os.FileMode(0o600)
	if opts.AllowUID >= 0 && opts.AllowUID != os.Geteuid() {
		// The other user must be able to open the file; the peer check
		// still keeps everyone else out.
		if err := os.Chown(path, opts.AllowUID, -1); err != nil {
			ln.Close()
			return nil, fmt.Errorf("chown %s: %w", path, err)
		}
	}
	if err := os.Chmod(path, mode); err != nil {
		ln.Close()
		return nil, err
	}
	return &peerCheckListener{Listener: ln, allow: opts.AllowUID}, nil
}

func dial(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", path)
}

// peerCheckListener drops connections from users other than the engine's
// own, root and AllowUID. File permissions should already stop them; this
// holds even if someone loosens the directory.
type peerCheckListener struct {
	net.Listener
	allow int
}

func (l *peerCheckListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uc, ok := c.(*net.UnixConn)
		if !ok {
			return c, nil
		}
		uid, err := peerUID(uc)
		if err != nil {
			log.Printf("[localapi] reject connection: cannot read peer credentials: %v", err)
			c.Close()
			continue
		}
		if uid == 0 || uid == os.Geteuid() || (l.allow >= 0 && uid == l.allow) {
			return c, nil
		}
		log.Printf("[localapi] reject connection from uid %d", uid)
		c.Close()
	}
}

func rawControl(c *net.UnixConn, f func(fd uintptr)) error {
	raw, err := c.SyscallConn()
	if err != nil {
		return err
	}
	return raw.Control(f)
}
