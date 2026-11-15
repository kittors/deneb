package main

import (
	"errors"
	"strings"
	"testing"
)

// TestLocalDrivePath: on Windows the service reads only from this
// computer's own drives: a share, a device path, a path relative to a drive
// or with . or .. or a stream in it is refused before anything is opened.
func TestLocalDrivePath(t *testing.T) {
	for _, p := range []string{
		`C:\Users\k\AppData\Local\Programs\Deneb\bin\denebd.exe`,
		`d:\Deneb\wintun.dll`,
	} {
		if err := localDrivePath(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	for _, p := range []string{
		`\\example.com\share\Deneb\denebd.exe`,
		`//example.com/share/Deneb/denebd.exe`,
		`\\?\UNC\example.com\share\denebd.exe`,
		`\\?\C:\Deneb\denebd.exe`,
		`\\.\C:\Deneb\denebd.exe`,
		`\\.\pipe\denebd.exe`,
		`\??\C:\Deneb\denebd.exe`,
		`C:Deneb\denebd.exe`,
		`\Deneb\denebd.exe`,
		`C:/Deneb/denebd.exe`,
		`C:\Deneb/denebd.exe`,
		`C:\Deneb\..\Windows\denebd.exe`,
		`C:\Deneb\.\denebd.exe`,
		`C:\Deneb\\denebd.exe`,
		`C:\Deneb\denebd.exe:stream`,
		`C:\Deneb\denebd.exe\`,
		"C:\\Deneb\\den\x01ebd.exe",
		`1:\Deneb\denebd.exe`,
		`C:\`,
		``,
	} {
		if err := localDrivePath(p); !errors.Is(err, errNotPlain) {
			t.Errorf("%q: %v, want it refused", p, err)
		}
	}
}

// TestReadUpTo: a file that grows past the limit while it is read is
// refused, not cut short.
func TestReadUpTo(t *testing.T) {
	if b, err := readUpTo(strings.NewReader("deneb"), 5, "f"); err != nil || string(b) != "deneb" {
		t.Errorf("at the limit: %q %v", b, err)
	}
	if _, err := readUpTo(strings.NewReader("deneb!"), 5, "f"); !errors.Is(err, errNotPlain) {
		t.Errorf("past the limit: %v, want it refused", err)
	}
}
