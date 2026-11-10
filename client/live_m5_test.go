package client_test

import (
	"testing"

	"github.com/kittors/deneb/client/fakeworld"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/apitype"
)

// macOS keeping the engine off the devices behind this router comes
// through as the notice of the local network permission, without a button
// to the system's settings while it has no switch for Deneb, and goes as
// the engine takes the problem back (DESIGN §6.13).
func TestLiveLocalNetwork(t *testing.T) {
	w, l := live(t, fakeworld.Ready)
	waitFor(t, l, "the world", func(s *model.Snapshot) bool { return len(s.LANs) == 2 })
	w.E.Update(func(s *apitype.State) {
		s.Engine.Problems = append(s.Engine.Problems, apitype.Problem{Code: apitype.ProblemLocalNetwork, Message: "系统不让引擎给本地网络里的设备发包"})
	})
	s := waitFor(t, l, "the notice", func(s *model.Snapshot) bool { return len(s.Notices) > 0 })
	if n := s.Notices[0]; n.Code != apitype.ProblemLocalNetwork || n.Short != "本地网络权限没拿到" || n.Detail == "" || n.Settings != "" {
		t.Errorf("the notice: %+v", n)
	}
	w.E.Update(func(s *apitype.State) { s.Engine.Problems = nil })
	waitFor(t, l, "the notice gone", func(s *model.Snapshot) bool { return len(s.Notices) == 0 })
}
