//go:build unix

package main

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// tryLock takes f's exclusive lock, without waiting.
func tryLock(f *os.File) bool {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) == nil
}

// redirectStderr points the process's stderr at f.
func redirectStderr(f *os.File) {
	unix.Dup2(int(f.Fd()), 2)
}

// freeSpace is how many bytes may still be written on the file system
// holding path, by a user that is not root.
func freeSpace(path string) (uint64, bool) {
	var st unix.Statfs_t
	if unix.Statfs(path, &st) != nil {
		return 0, false
	}
	return uint64(st.Bavail) * uint64(st.Bsize), true
}

// spawnDetached starts a program that outlives this one: its own session,
// no terminal, output nowhere (the relay writes its own log), as user
// ("" for this one).
func spawnDetached(user, name string, args ...string) error {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if user != "" {
		a, ok := realHost().lookupUser(user)
		if !ok {
			return os.ErrNotExist
		}
		c.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(a.uid), Gid: uint32(a.gid)}
		c.Dir = "/"
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	c.Stdin, c.Stdout, c.Stderr = null, null, null
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

func killProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// reexec starts the program an update put in place, in this process: the
// same pid, which the service manager goes on watching, and the same
// command line. Every file of this one closes on exec, its listeners and
// its lock too.
func reexec() {
	bin := updatedBin
	if bin == "" {
		return
	}
	if err := syscall.Exec(bin, os.Args, os.Environ()); err != nil {
		os.Stderr.WriteString("deneb-relay: restarting after the update: " + err.Error() + "\n")
	}
}
