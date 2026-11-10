package localapi

import (
	"testing"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// TestEventQueueKeepsUsageAndCuts: a watcher gets usage (the latest) and
// every cut, besides State and paths. Before 11-08 evening the queue
// dropped both, and no watcher ever saw them.
func TestEventQueueKeepsUsageAndCuts(t *testing.T) {
	q := newEventQueue()
	q.push(apitype.Event{Type: apitype.EventUsage, Using: []apitype.ServiceUse{{DeviceID: "d1", Port: 80}}})
	q.push(apitype.Event{Type: apitype.EventUsage, Using: []apitype.ServiceUse{{DeviceID: "d1", Port: 80, Conns: 2}}})
	q.push(apitype.Event{Type: apitype.EventCut, Cut: []apitype.ServiceUse{{DeviceID: "d2", Port: 22}}})
	q.push(apitype.Event{Type: apitype.EventCut, Cut: []apitype.ServiceUse{{DeviceID: "d3", Port: 22}}})
	q.push(apitype.Event{Type: apitype.EventPaths, Paths: []apitype.PeerPath{{DeviceID: "d1", Kind: apitype.PathDirect}}})
	got := q.drain()
	var usage, cuts, paths int
	for _, ev := range got {
		switch ev.Type {
		case apitype.EventUsage:
			usage++
			if len(ev.Using) != 1 || ev.Using[0].Conns != 2 {
				t.Errorf("usage event %+v, want the latest", ev)
			}
		case apitype.EventCut:
			cuts++
		case apitype.EventPaths:
			paths++
		}
	}
	if usage != 1 || cuts != 2 || paths != 1 {
		t.Fatalf("drained %d usage, %d cut, %d paths events (%+v); want 1, 2, 1", usage, cuts, paths, got)
	}
	if again := q.drain(); len(again) != 0 {
		t.Fatalf("drained twice: %+v", again)
	}
}
