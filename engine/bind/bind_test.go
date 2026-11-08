package bind

import (
	"net"
	"testing"
)

func TestDefaultInterfaceIsPhysical(t *testing.T) {
	name, err := DefaultInterface()
	if err != nil {
		t.Skipf("no usable default route here: %v", err)
	}
	if IsTunnel(name) {
		t.Fatalf("default interface %s is a tunnel", name)
	}
	ctl, err := Control(name)
	if err != nil {
		t.Fatal(err)
	}
	lc := net.ListenConfig{Control: ctl}
	pc, err := lc.ListenPacket(t.Context(), "udp4", ":0")
	if err != nil {
		t.Fatalf("listen pinned to %s: %v", name, err)
	}
	pc.Close()
	t.Logf("default interface: %s", name)
}

func TestIsTunnel(t *testing.T) {
	for _, n := range []string{"utun4", "tun0", "wg0", "tailscale0", "Wintun Userspace Tunnel"} {
		if !IsTunnel(n) {
			t.Errorf("%s should be a tunnel", n)
		}
	}
	for _, n := range []string{"en0", "eth0", "Ethernet", "wlan0"} {
		if IsTunnel(n) {
			t.Errorf("%s is not a tunnel", n)
		}
	}
}
