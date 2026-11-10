//go:build !linux && !darwin

package control

import "errors"

func diskFree(string) (free, total uint64, err error) {
	return 0, 0, errors.New("disk space: not on this system")
}
