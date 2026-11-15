//go:build !unix

package main

import (
	"errors"
	"os"
)

// The relay installs itself on Linux only; elsewhere it builds and runs
// in the foreground, as before.

func tryLock(*os.File) bool { return true }

func redirectStderr(*os.File) {}

func freeSpace(string) (uint64, bool) { return 0, false }

func spawnDetached(string, string, ...string) error { return errors.ErrUnsupported }

func killProcess(int) error { return errors.ErrUnsupported }

func reexec() {}
