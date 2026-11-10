//go:build linux || darwin

package control

import "syscall"

// diskFree is the space left to an unprivileged user on the file system
// that holds path, and its size.
func diskFree(path string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
}
