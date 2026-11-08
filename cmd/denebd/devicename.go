package main

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// defaultDisplayName is what to call this device the first time it logs in
// when -name is not given. On macOS that is the computer name people see in
// Finder and AirDrop ("Kittors的MacBook Air"), not the host name the DNS
// label is made from ("KittorsdeMacBook-Air"). Elsewhere it is empty and
// the control server falls back to the host name.
func defaultDisplayName() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--get", "ComputerName").Output()
	if err != nil {
		return ""
	}
	return cleanName(string(out))
}

// cleanName makes a name the control server accepts, which otherwise turns
// the whole login down: no control characters, at most 40 characters.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 40 {
		s = strings.TrimSpace(string(r[:40]))
	}
	return s
}
