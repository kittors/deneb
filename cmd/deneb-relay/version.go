package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

// relayVersion is the release this program is, set when a release is
// built: -ldflags "-X main.relayVersion=0.1.9". It follows Deneb's
// version. A build without it is a development one, which never updates
// itself.
var relayVersion = ""

// updateFeed is the download site's folder of relay releases, set when a
// release is built: -X main.updateFeed=https://<site>/download/relay/. It
// holds relay.json, the latest release's manifest (update.go). The site's
// address is not in the repository (AGENTS.md §3). $DENEB_RELAY_FEED
// overrides it.
var updateFeed = ""

func feed() string {
	if f := os.Getenv("DENEB_RELAY_FEED"); f != "" {
		return f
	}
	return updateFeed
}

// buildSetting is one of the build's settings (GOARM, vcs.revision).
func buildSetting(key string) string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range bi.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}

// version is the relay program's, for the control server's log:
// "deneb-relay 0.1.9", or the commit of a development build.
func version() string {
	if relayVersion != "" {
		return "deneb-relay " + relayVersion
	}
	if rev := buildSetting("vcs.revision"); len(rev) >= 12 {
		return "deneb-relay " + rev[:12]
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		return "deneb-relay " + bi.Main.Version
	}
	return "deneb-relay"
}

// shortVersion is the release, or "dev".
func shortVersion() string {
	if relayVersion != "" {
		return relayVersion
	}
	return "dev"
}

// cmdVersion prints the release alone: an update runs the program it
// downloaded with it, to see it is that release and that it runs here.
func cmdVersion([]string) int {
	fmt.Println(shortVersion())
	return 0
}

// compareVersions compares releases like semantic versioning: 0.1.10 after
// 0.1.9, a pre-release before its release. As MyGo's updater compares
// the window's.
func compareVersions(a, b string) int {
	a, b = strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v")
	a, _, _ = strings.Cut(a, "+")
	b, _, _ = strings.Cut(b, "+")
	ar, apre, _ := strings.Cut(a, "-")
	br, bpre, _ := strings.Cut(b, "-")
	if c := compareFields(ar, br); c != 0 {
		return c
	}
	switch {
	case apre == bpre:
		return 0
	case apre == "":
		return 1
	case bpre == "":
		return -1
	}
	return compareFields(apre, bpre)
}

func compareFields(a, b string) int {
	af, bf := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(af) || i < len(bf); i++ {
		var x, y string
		if i < len(af) {
			x = af[i]
		}
		if i < len(bf) {
			y = bf[i]
		}
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		switch {
		case xerr == nil && yerr == nil:
			if xn != yn {
				if xn < yn {
					return -1
				}
				return 1
			}
		case x != y:
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
