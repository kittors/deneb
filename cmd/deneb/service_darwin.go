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
	return installServiceAsking(ctx, exe, "Deneb 要安装系统网卡。")
}

// updateService installs the service again over the one there, where it
// cannot take the shipped engine in itself (an unsigned, development build;
// a service from before it could): the same, but the system's window says
// it is an update (design/plan/m7/engine-self-update.md §4).
func updateService(ctx context.Context, exe string) error {
	return installServiceAsking(ctx, exe, "Deneb 要更新系统网卡。")
}

// installServiceAsking runs `denebd service install` as an administrator.
// The system's own window asks for the password, with prompt as its first
// line (the line after, about the password, is the system's).
func installServiceAsking(ctx context.Context, exe, prompt string) error {
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
	script := "do shell script " + strconv.Quote(strings.Join(parts, " ")) + " with prompt " + strconv.Quote(prompt) + " with administrator privileges"
	b, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("安装 macOS 系统网卡：%w：%s", err, strings.TrimSpace(string(b)))
	}
	return nil
}
