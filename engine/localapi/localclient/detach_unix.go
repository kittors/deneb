//go:build !windows

package localclient

import (
	"os/exec"
	"syscall"
)

// detach puts the engine in its own session: no terminal, no signals from
// the client's process group.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
