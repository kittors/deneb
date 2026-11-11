package tun

import (
	"testing"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// TestNetworksRead reads, changing nothing, the network each adapter that
// is up is on, through the Network List Manager's method tables as
// inbound_windows.go calls them. For a real machine:
//
//	GOOS=windows go test -c -o tun.test.exe ./engine/tun
//	tun.test.exe -test.run TestNetworksRead -test.v
func TestNetworksRead(t *testing.T) {
	aa, err := winipcfg.GetAdaptersAddresses(windows.AF_UNSPEC, winipcfg.GAAFlagDefault)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range aa {
		if a.OperStatus != winipcfg.IfOperStatusUp {
			continue
		}
		guid, err := a.LUID.GUID()
		if err != nil {
			t.Errorf("%s: %v", a.FriendlyName(), err)
			continue
		}
		n, changed, found, err := adapterNetwork(*guid, false)
		if err != nil || changed {
			t.Errorf("%s: %v (changed: %v)", a.FriendlyName(), err, changed)
			continue
		}
		if found {
			t.Logf("%s: network %v", a.FriendlyName(), n)
		} else {
			t.Logf("%s: on no network", a.FriendlyName())
		}
	}
	t.Logf("firewall rule %s: %v", FirewallRule, firewallRuleExists())
}
