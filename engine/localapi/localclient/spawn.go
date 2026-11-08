package localclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kittors/deneb/engine/localapi/socket"
)

// SpawnOptions say how to start a user-space engine.
type SpawnOptions struct {
	// Binary is denebd's path. Empty: denebd next to this executable,
	// then denebd on PATH.
	Binary string
	// Args default to ["run"].
	Args []string
	// Env is added to this process's environment, e.g. DENEB_CONTROL=...
	Env []string
	// LogPath defaults to denebd.log in the engine's state directory.
	LogPath string
	// Wait is how long to wait for the new engine to answer (default 5 s).
	Wait time.Duration
}

// EnsureRunning connects to the engine, starting one if nothing answers
// (plan §2.4: in user-space mode the client starts the engine). The engine
// is started detached, so closing or crashing the client never takes the
// connections down. It listens where this client connects.
func (c *Client) EnsureRunning(ctx context.Context, o SpawnOptions) error {
	if _, err := c.Version(ctx); err == nil {
		return nil
	} else if !errors.Is(err, ErrNotRunning) {
		return err
	}
	bin, err := findEngine(o.Binary)
	if err != nil {
		return err
	}
	args := o.Args
	if len(args) == 0 {
		args = []string{"run"}
	}
	logPath := o.LogPath
	if logPath == "" {
		logPath = filepath.Join(socket.StateDir(), "denebd.log")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(append(os.Environ(), o.Env...), socket.EnvPath+"="+c.spawnPath())
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}
	go cmd.Wait() // reap it if it exits while we run
	wait := o.Wait
	if wait <= 0 {
		wait = 5 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		if _, err := c.Version(ctx); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("started %s but it does not answer on %s; see %s", bin, c.spawnPath(), logPath)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func findEngine(bin string) (string, error) {
	if bin != "" {
		return bin, nil
	}
	name := "denebd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("cannot find %s next to this program or on PATH", name)
	}
	return p, nil
}
