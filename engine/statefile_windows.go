//go:build windows

package engine

import "time"

// lockState: on Windows the engine's named pipe already refuses a second
// engine of the same user before it starts networking, and there is no
// kernel mode yet (M5), so no lock file is needed.
func lockState(dir string, wait time.Duration) (func(), error) { return func() {}, nil }

func chownToDir(path, dir string) {}
