package main

import (
	"log"

	"github.com/kittors/deneb/engine"
	"github.com/kittors/deneb/proto"
)

// builtinEntrances are the ways to Deneb's own server, as base64 of
// proto.ControlEntrances' JSON: a release sets it at build time from the
// deployment's (deploy/package.sh, DENEB_CONTROL_ENTRANCES), whose
// addresses past the first stay out of the repository. Unset: builtinControl
// alone (design/plan/control-reachability-20261116.md §9.3).
var builtinEntrances string

// controlEntrances are the ways to Deneb's own server, in a release build;
// a test build has none. A device that saved builtinControl, as releases
// before them did, goes by them too (engine.Options.OwnControls).
func controlEntrances() []proto.ControlEntrance {
	if engine.TestBuild {
		return nil
	}
	if builtinEntrances != "" {
		l, err := proto.ParseControlEntrances(builtinEntrances)
		if err == nil {
			return l.Entrances
		}
		log.Printf("the built-in control entrances do not read (%v): Deneb's own address alone", err)
	}
	return []proto.ControlEntrance{{URL: builtinControl, Pin: builtinControlPin}}
}

// Deneb's own control server, for a release build's first login: what a
// person who just installed the app logs in to, configuring nothing
// (11-10). Someone running their own server names it, with its pin, by
// -control and -control-pin, DENEB_CONTROL and DENEB_CONTROL_PIN, or, on
// Linux, /etc/default/denebd. Test builds have none: the test topology and
// the test machines name theirs.
const (
	builtinControl    = "https://103.42.31.155:47443"
	builtinControlPin = "sha256:a90009bbefa6cbcad0971350099e2bb54c4f9968a8cdf1401ab54c6861cf0dd4"
)

// controlFor is the control server and pin to use, given -control and
// -control-pin: Deneb's own in a release build when none is named, with
// its pin when that server is named without one. Another server's pin is
// never guessed.
func controlFor(url, pin string) (string, string) {
	if engine.TestBuild {
		return url, pin
	}
	if url == "" {
		return builtinControl, builtinControlPin
	}
	if url == builtinControl && pin == "" {
		pin = builtinControlPin
	}
	return url, pin
}
