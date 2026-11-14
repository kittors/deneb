package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var ownEngineArgs []string

// macOS supplies utun. The packaged engine installs its root-owned launchd
// service after the system authenticates the user; no password enters Deneb.
func installService(ctx context.Context, exe string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	state := filepath.Join(home, "Library", "Application Support", "Deneb", "engine")
	args := []string{exe, "service", "install", "-allow-uid", strconv.Itoa(os.Getuid()), "-import-state", state}
	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	script := "do shell script " + strconv.Quote(strings.Join(parts, " ")) + " with administrator privileges"
	b, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("安装 macOS 系统网卡：%w：%s", err, strings.TrimSpace(string(b)))
	}
	return nil
}
