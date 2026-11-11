package client

import (
	"time"

	"github.com/kittors/deneb/client/model"
)

// M6 in the demo (DESIGN §7.3, §6.14): a person new to Deneb, who has no
// LAN yet; the diagnostics; and the other proxies' notices.

// m6Event plays one of M6's outside events on s, or returns s:
//   - "m6": the server and the engine as M6 has them: diagnostics.
//   - "m6-fresh": signed in for the first time: no LAN, nothing given to me,
//     no network adapter.
//   - "notice-surge", "notice-v2rayn": Surge's, or v2rayN's, tunnel takes
//     Deneb's connections (§6.14, as Clash's of §6.8).
//   - "user-mode": this computer's engine has no network adapter, as after
//     Deneb was installed again over it, or the first time's step put off
//     (§7.3, M6's second batch); "kernel": it has.
func (d *Demo) m6Event(s *model.Snapshot, event string, now time.Time) *model.Snapshot {
	n := *s
	switch event {
	case "user-mode", "kernel":
		n.Kernel = event == "kernel"
	case "m6":
		n.Features = maps(s.Features)
		n.Features[model.FeatureDiagnostics] = true
	case "m6-fresh":
		// Deneb just installed: no adapter yet either.
		n.Kernel = false
		n.LANs, n.ShareLANs, n.Shared, n.Invitations, n.Notices = nil, nil, nil, nil, nil
		n.Seats.Used, n.Seats.People = 1, []model.PersonID{s.Me}
	case "notice-surge":
		n.Notices = []model.Notice{{Code: "tun-capture", Short: "Surge 可能挡住连接", Message: "Surge 正在接管网络，局域网里的设备可能连不上",
			Detail: "在 Surge 的「规则」里加上这一条，局域网里的连接就不经过它：", Rule: "IP-CIDR,10.67.0.0/16,DIRECT"}}
	case "notice-v2rayn":
		n.Notices = []model.Notice{{Code: "tun-capture", Short: "v2rayN 可能挡住连接", Message: "v2rayN 正在接管网络，局域网里的设备可能连不上",
			Detail: "在 v2rayN 的「路由设置」里，把这一段加进「直连」：", Rule: "10.67.0.0/16"}}
	default:
		return s
	}
	return &n
}
