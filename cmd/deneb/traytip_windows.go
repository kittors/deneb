package main

import (
	"os"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// trayHidden reports whether Windows keeps Deneb's mark in the "^" of the
// notification area, out of sight, as it keeps a new icon until the user
// drags it onto the taskbar. Windows 11 notes it for each program's icon
// under Control Panel\NotifyIconSettings: IsPromoted 1 is on the taskbar,
// 0 or nothing in the "^". Where it notes nothing of Deneb's (Windows 10,
// which shows a new icon), the mark shows.
func trayHidden() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, `Control Panel\NotifyIconSettings`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return false
	}
	defer k.Close()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return false
	}
	found := false
	for _, name := range names {
		sk, err := registry.OpenKey(k, name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		path, _, err := sk.GetStringValue("ExecutablePath")
		if err == nil && samePath(path, exe) {
			found = true
			if v, _, err := sk.GetIntegerValue("IsPromoted"); err == nil && v == 1 {
				sk.Close()
				return false
			}
		}
		sk.Close()
	}
	return found
}

// samePath reports whether path, as NotifyIconSettings notes a program's,
// is exe: in full, or from a known folder named by its GUID
// ("{6D809377-…}\Deneb\Deneb.exe" for one under Program Files).
func samePath(path, exe string) bool {
	if strings.EqualFold(path, exe) {
		return true
	}
	if strings.HasPrefix(path, "{") {
		if i := strings.Index(path, `}\`); i > 0 {
			return strings.HasSuffix(strings.ToLower(exe), strings.ToLower(path[i+1:]))
		}
	}
	return false
}
