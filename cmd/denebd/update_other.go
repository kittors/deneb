//go:build !windows && !darwin

package main

import (
	"context"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// serviceSelfUpdate: the Windows and macOS services replace themselves; a
// Linux daemon is updated by its package.
func serviceSelfUpdate() func(context.Context, string) apitype.UpdateResult { return nil }

func updateStartHook() bool { return false }
