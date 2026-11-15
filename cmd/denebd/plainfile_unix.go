//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// readPlain reads path whole, at most max bytes: a regular file with no
// other name, not reached through a link at its end (O_NOFOLLOW), opened
// without waiting for a FIFO's writer and without becoming the service's
// terminal (plainfile.go).
func readPlain(path string, max int64) ([]byte, error) {
	name := filepath.Base(path)
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err == syscall.ELOOP {
		return nil, fmt.Errorf("%w：%s 是个链接", errNotPlain, name)
	}
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		syscall.Close(fd)
		return nil, &os.PathError{Op: "fstat", Path: path, Err: err}
	}
	bad := ""
	switch {
	case st.Mode&syscall.S_IFMT != syscall.S_IFREG:
		bad = "不是普通文件"
	case uint64(st.Nlink) != 1:
		// Another name for a file is one anybody can make, of a file they
		// may not read themselves.
		bad = "还有别的名字（硬链接）"
	case st.Size > max:
		bad = "太大"
	}
	if bad != "" {
		syscall.Close(fd)
		return nil, fmt.Errorf("%w：%s %s", errNotPlain, name, bad)
	}
	// A regular file: read it blocking, as any other.
	if err := syscall.SetNonblock(fd, false); err != nil {
		syscall.Close(fd)
		return nil, &os.PathError{Op: "fcntl", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	return readUpTo(f, max, name)
}
