package main

import (
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanName(t *testing.T) {
	long := strings.Repeat("长", 45)
	for _, c := range []struct{ in, want string }{
		{"Kittors的MacBook Air\n", "Kittors的MacBook Air"},
		{"  阿杰的\tNAS \x00", "阿杰的NAS"},
		{"bad\xffbyte", "badbyte"},
		{long, strings.Repeat("长", 40)},
		{"\n", ""},
	} {
		got := cleanName(c.in)
		if got != c.want {
			t.Errorf("cleanName(%q) = %q, want %q", c.in, got, c.want)
		}
		if utf8.RuneCountInString(got) > 40 {
			t.Errorf("cleanName(%q) is longer than 40", c.in)
		}
	}
}

func TestDefaultDisplayName(t *testing.T) {
	n := defaultDisplayName()
	if runtime.GOOS != "darwin" {
		if n != "" {
			t.Errorf("default name %q outside macOS", n)
		}
		return
	}
	if n == "" {
		t.Error("no computer name on macOS")
	}
}
