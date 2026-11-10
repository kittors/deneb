package engine

import (
	"net/netip"

	"github.com/kittors/deneb/proto"
)

// testKnobs stage the attacks and failures of the acceptance tests. Only
// the test build (-tags denebtest) has endpoints that set them; in a normal
// build they stay zero and change nothing.
type testKnobs struct {
	// extraAllowed lets this device route more addresses to a peer, and
	// send to them: "route box.jie's address via the Mac" (§6.4 transit).
	extraAllowed map[proto.DeviceID][]netip.Prefix
	filterOff    bool
	// noDNSFlush leaves macOS's DNS caches alone when a name comes back,
	// to measure how long they keep "no such name" (kernel round).
	noDNSFlush bool
	// offline replaces the system's answer to "is there a network?"
	// (checkOnline), for in-process tests.
	offline func(bound string) bool
}
