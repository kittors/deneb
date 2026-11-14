package main

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
)

type serviceOpenCall struct {
	device  model.DeviceID
	service model.Service
}

type serviceOpenStore struct {
	*client.Demo
	opened []serviceOpenCall
}

func (s *serviceOpenStore) Open(_ context.Context, device model.DeviceID, service model.Service) (string, error) {
	s.opened = append(s.opened, serviceOpenCall{device: device, service: service})
	return "127.0.0.1:65080", nil
}

// An owned remote computer needs both controls: open its enabled service
// here, and keep managing what it exposes to the LAN. Ownership must not
// hide the same open action that another person's device already has.
func TestDeviceServiceOpen(t *testing.T) {
	for _, tc := range []struct {
		name   string
		device model.DeviceID
		on     bool
		open   bool
	}{
		{"owned remote enabled", "game", true, true},
		{"owned remote disabled", "game", false, false},
		{"this computer", "mac", true, false},
		{"other remote enabled", "mia", true, true},
		{"other remote disabled", "mia", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, dark := range []bool{false, true} {
				d := client.NewDemo()
				d.Manual()
				svc := model.Service{Key: "tcp/19080", Kind: "web", Name: "量子大屏", Proto: "tcp", Port: 19080, Ports: "19080", On: tc.on}
				d.Snapshot().LAN("weekend").Services[tc.device] = []model.Service{svc}
				d.Snapshot().Devices[tc.device].Catalog = []model.Service{svc}
				s := &serviceOpenStore{Demo: d}
				a := newApp(s, demoPlaces())
				a.selected = string(tc.device)
				var opened []string
				a.openURL = func(url string) error { opened = append(opened, url); return nil }
				tt := ui.NewTester(a.view, 980, 640)
				tt.SetDark(dark)
				tt.Frame()
				tt.Advance(800 * time.Millisecond)
				if !tt.HasText("量子大屏") {
					t.Fatalf("service missing from the card: %q", tt.Texts())
				}
				if got := tt.HasText("打开"); got != tc.open {
					t.Fatalf("dark=%v: open button=%v, want %v; texts=%q", dark, got, tc.open, tt.Texts())
				}
				if tc.open {
					if err := tt.Click("打开"); err != nil {
						t.Fatal(err)
					}
					tt.Frame()
					if len(s.opened) != 1 || s.opened[0].device != tc.device || s.opened[0].service.Key != svc.Key {
						t.Fatalf("wrong service opened: %+v", s.opened)
					}
					if len(opened) != 1 || opened[0] != "http://127.0.0.1:65080" {
						t.Fatalf("wrong browser URL: %q", opened)
					}
				}
				if d.Snapshot().Devices[tc.device].Owner == d.Snapshot().Me {
					box, ok := tt.Find("量子大屏")
					if !ok {
						t.Fatal("service label disappeared after opening")
					}
					tt.ClickAt(a.origin.X+a.cardAt.X+a.cardAt.W-16-12-19, box.Y+box.H/2)
					if tc.on {
						acceptConfirmation(t, tt, "关闭服务")
					}
					tt.Frame()
					if got := d.Snapshot().LAN("weekend").Services[tc.device][0].On; got == tc.on {
						t.Fatalf("dark=%v: service toggle stopped working", dark)
					}
					if len(s.opened) > 1 {
						t.Fatal("toggling the service opened the browser")
					}
				}
			}
		})
	}
}
