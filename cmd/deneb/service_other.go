//go:build !windows

package main

import (
	"context"
	"errors"
)

// installService: only the Windows service is installed by the window.
func installService(ctx context.Context, exe string) error { return errors.ErrUnsupported }
