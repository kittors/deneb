package main

import (
	"slices"
	"testing"

	"github.com/kittors/deneb/engine"
)

func TestSplitState(t *testing.T) {
	for _, c := range []struct {
		args  []string
		state string
		rest  []string
	}{
		{nil, "def", nil},
		{[]string{"-control", "https://c:1", "-port", "41722"}, "def", []string{"-control", "https://c:1", "-port", "41722"}},
		{[]string{"-state", `C:\s`, "-socks="}, `C:\s`, []string{"-socks="}},
		{[]string{"-socks=", "--state=D:\\x", "-name", "台式机"}, `D:\x`, []string{"-socks=", "-name", "台式机"}},
		{[]string{"-state", "a", "-state", "b"}, "b", nil},
		{[]string{"-port", "1", "-state"}, "def", []string{"-port", "1", "-state"}},
		{[]string{"-statefile", "x"}, "def", []string{"-statefile", "x"}},
	} {
		state, rest := splitState(c.args, "def")
		if state != c.state || !slices.Equal(rest, c.rest) {
			t.Errorf("splitState(%q) = %q, %q; want %q, %q", c.args, state, rest, c.state, c.rest)
		}
	}
}

func TestCheckRunFlags(t *testing.T) {
	if err := checkRunFlags([]string{"-mode", "kernel", "-state", t.TempDir(), "-control", "https://c:1", "-port", "41722", "-socks=", "-userview=false"}); err != nil {
		t.Fatalf("good flags: %v", err)
	}
	for _, bad := range [][]string{
		{"-contrl", "https://c:1"},
		{"-mode", "tun"},
		{"-port", "x"},
		{"-socks=", "stray"},
	} {
		if err := checkRunFlags(bad); err == nil {
			t.Errorf("checkRunFlags(%q): no error", bad)
		}
	}
}

func TestHasFlag(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{[]string{"-allow-user", "S-1-5-21-1"}, true},
		{[]string{"-port", "1", "--allow-user=PC\\jie"}, true},
		{[]string{"-allow-users", "x"}, false},
		{[]string{"-port", "1", "--", "-allow-user", "x"}, false},
		{nil, false},
	} {
		if got := hasFlag(c.args, "allow-user"); got != c.want {
			t.Errorf("hasFlag(%q) = %v", c.args, got)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		cmp  int
		ok   bool
	}{
		{"0.6.0", "0.6.0", 0, true},
		{"v0.6.1", "0.6.0", 1, true},
		{"0.10.0", "0.9.9", 1, true},
		{"1.0.0", "1.0.0-beta.2", 1, true},
		{"1.0.0-beta.2", "1.0.0-beta.10", -1, true},
		{"1.0.0-beta", "1.0.0-1", 1, true},
		{"1.0.0-rc.1", "1.0.0-beta.9", 1, true},
		{"1.0.0+abc", "1.0.0+def", 0, true},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1, true},
		{"m5-11092326-win", "0.6.0", 0, false},
		{"0.6", "0.6.0", 0, false},
		{"0.06.0", "0.6.0", 0, false},
		{"dev", "dev", 0, false},
	} {
		cmp, ok := compareVersions(c.a, c.b)
		if cmp != c.cmp || ok != c.ok {
			t.Errorf("compareVersions(%q, %q) = %d, %v; want %d, %v", c.a, c.b, cmp, ok, c.cmp, c.ok)
		}
	}
}

// TestControlFor: a release build logs in to Deneb's own server unless told
// otherwise, with its pin; another server's pin is never filled in.
func TestControlFor(t *testing.T) {
	if engine.TestBuild {
		t.Skip("a test build has no built-in server (controlFor passes the flags through)")
	}
	for _, c := range []struct{ url, pin, wantURL, wantPin string }{
		{"", "", builtinControl, builtinControlPin},
		{builtinControl, "", builtinControl, builtinControlPin},
		{"https://my.example:47443", "", "https://my.example:47443", ""},
		{"https://my.example:47443", "sha256:ab", "https://my.example:47443", "sha256:ab"},
		{"", "sha256:ab", builtinControl, builtinControlPin},
	} {
		u, p := controlFor(c.url, c.pin)
		if u != c.wantURL || p != c.wantPin {
			t.Errorf("controlFor(%q, %q) = %q, %q", c.url, c.pin, u, p)
		}
	}
}

func TestEnvBool(t *testing.T) {
	t.Setenv("DENEB_TEST_BOOL", "false")
	if envBool("DENEB_TEST_BOOL", true) {
		t.Error("false read as true")
	}
	t.Setenv("DENEB_TEST_BOOL", "maybe")
	if !envBool("DENEB_TEST_BOOL", true) {
		t.Error("an unreadable value did not keep the default")
	}
}
