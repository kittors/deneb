package localclient

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// detach starts the engine without a console and outside the client's
// process group, so Ctrl-C or closing the client does not stop it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
}
