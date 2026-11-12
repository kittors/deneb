//go:build !windows

package logfile

import "os"

// Open opens path to append to, creating it for this user only.
func Open(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}
