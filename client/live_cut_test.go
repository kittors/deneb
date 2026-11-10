package client

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/localclient"
	"github.com/kittors/deneb/proto"
)

// A use this computer cuts comes through, for a few seconds, as the
// device that used it and the service as the card names it (scene 5); the
// usage the engine pushes apart from its state changes the "已连接" count.
func TestLiveTakesCutsAndUsage(t *testing.T) {
	now := time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC)
	l := NewLive(localclient.New("/nowhere"))
	l.now = func() time.Time { return now }
	raw, _ := json.Marshal(proto.UserView{Version: 3, Me: "me",
		People: []proto.Person{{ID: "me", Name: "me"}, {ID: "jie", Name: "jie", DisplayName: "阿杰"}},
		LANs: []proto.LANView{{ID: "weekend", Name: "周末联机", Owner: "jie",
			Members: []proto.Member{{Person: "jie", State: proto.MemberJoined}, {Person: "me", State: proto.MemberJoined}},
			Devices: []proto.LANDevice{{Device: "mac", Services: []proto.ServiceState{{Service: proto.Service{Kind: "web", Name: "网页",
				Ports: []proto.PortRange{proto.Port(proto.TCP, 80)}}, On: true}}}, {Device: "pc"}}}},
		Devices: []proto.DeviceView{{ID: "mac", Owner: "me", Name: "mac"}, {ID: "pc", Owner: "jie", Name: "pc", DisplayName: "阿杰的台式机"}},
	})
	l.takeState(&apitype.State{Version: 3, LoggedIn: true, User: "me", SelfDeviceID: "mac", UserView: raw})
	l.takeUsage(nil, []apitype.ServiceUse{{DeviceID: "pc", Proto: "tcp", Port: 3389, Since: now.Add(-time.Minute)}})
	if s := l.Snapshot(); !s.Using["pc"].Equal(now.Add(-time.Minute)) {
		t.Errorf("using %v", s.Using)
	}
	l.takeCut([]apitype.ServiceUse{{DeviceID: "pc", Scopes: []apitype.ScopeID{apitype.ScopeID(proto.LANScope("weekend"))}, Proto: "tcp", Port: 80}})
	s := l.Snapshot()
	if len(s.Cuts) != 1 || s.Cuts[0] != (model.Cut{Device: "pc", Service: "网页", At: now}) {
		t.Fatalf("cuts %+v", s.Cuts)
	}
	now = now.Add(cutKeep + time.Second)
	l.update(func(time.Time) {})
	if s := l.Snapshot(); len(s.Cuts) != 0 {
		t.Errorf("a cut kept past its time: %+v", s.Cuts)
	}
}
