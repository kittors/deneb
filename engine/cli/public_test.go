package cli

import (
	"testing"

	"github.com/kittors/deneb/engine/localapi/apitype"
)

// deneb status says where the STUN answers saw this device from, and who
// saw it: the relay by its name, a STUN server by its host (its port only
// when it is not STUN's own).
func TestPublicLine(t *testing.T) {
	for _, c := range []struct {
		ps   []apitype.PublicAddr
		want string
	}{
		{nil, "还没查到"},
		{[]apitype.PublicAddr{{Addr: "203.0.113.7:41641", Server: "stun.cloudflare.com:3478"}},
			"203.0.113.7:41641（stun.cloudflare.com 看到的）"},
		{[]apitype.PublicAddr{
			{Addr: "203.0.113.7:41641", Relay: "r_1", RelayName: "香港"},
			{Addr: "203.0.113.7:41641", Server: "stun.cloudflare.com:3478"},
		}, "203.0.113.7:41641（中转站“香港”、stun.cloudflare.com 看到的）"},
		{[]apitype.PublicAddr{
			{Addr: "203.0.113.7:41641", Server: "stun.cloudflare.com:3478"},
			{Addr: "203.0.113.7:50000", Relay: "r_2"},
			{Addr: "203.0.113.7:41642", Server: "stun.example.com:19302"},
		}, "203.0.113.7:41641（stun.cloudflare.com 看到的）；203.0.113.7:50000（中转站“r_2”看到的）；203.0.113.7:41642（stun.example.com:19302 看到的）"},
	} {
		if got := publicLine(c.ps); got != c.want {
			t.Errorf("publicLine(%+v) = %q, want %q", c.ps, got, c.want)
		}
	}
}
