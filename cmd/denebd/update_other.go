//go:build !windows && !darwin

package main

import (
	"context"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// serviceSelfUpdate: only the Windows service replaces itself (M6); a
// macOS or Linux daemon is updated by its package.
func serviceSelfUpdate() func(context.Context, string) apitype.UpdateResult { return nil }
