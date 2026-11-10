package model

import (
	"slices"
	"time"
)

// M3 (DESIGN §6.11): one of my devices given to one person, the devices
// given to me, a LAN that only hands out devices, and the seats.

// SharedLANID names the canvas of the devices given to me, "共享给我": not
// a LAN of the server's, but shown as one is, this computer in its middle.
// LAN returns it; LANs never lists it.
const SharedLANID LANID = "\x00shared"

// LANMode is how a LAN works (M3).
type LANMode string

const (
	ModeLAN   LANMode = ""      // members' devices see each other (scene 5)
	ModeShare LANMode = "share" // members only use what the owner gives each of them (scene 4)
)

// Share is one of my devices given to one person, as its card lists it.
type Share struct {
	ID       string // empty while the server has not answered
	Person   PersonID
	Services []Service // what it opens to them, on or off
}

// SharedDevice is a device someone gave me.
type SharedDevice struct {
	Share  string
	Device DeviceID // in Devices
	From   PersonID // who gave it
	// FromLAN is set for a LAN's own device: the LAN's name, as its second
	// line says "设计部 · 直接连上".
	FromLAN  string
	Services []Service // what it opens to me
	At       time.Time
}

// Seats are how many people my seats hold (pricing-and-relays §1.4): the
// people in the LANs I made and those I gave a device to, me too, each
// once.
type Seats struct {
	// Enforced: the server holds to the limit. Only then does the view say
	// a word of seats.
	Enforced          bool
	Limit, Used, Left int
	People            []PersonID // who takes them, me first
}

// Holds reports whether who takes one of my seats already: giving them a
// device, or bringing them into a LAN of mine, takes no new one.
func (s Seats) Holds(who PersonID) bool { return slices.Contains(s.People, who) }

// Full reports whether no one new finds a seat.
func (s Seats) Full() bool { return s.Enforced && s.Left <= 0 }

// SharedOf returns the share that gives me dev, or nil.
func (s *Snapshot) SharedOf(dev DeviceID) *SharedDevice {
	for i := range s.Shared {
		if s.Shared[i].Device == dev {
			return &s.Shared[i]
		}
	}
	return nil
}

// ShareTo returns the share of dev to who, or nil.
func (d *Device) ShareTo(who PersonID) *Share {
	for i := range d.Shares {
		if d.Shares[i].Person == who {
			return &d.Shares[i]
		}
	}
	return nil
}

// sharedLAN is the canvas of the devices given to me as a LAN: this
// computer and those devices, each with what it opens to me; its members
// are me and who gave them, its relays those their paths go through. No one
// owns it, so nothing in it is an owner's to change.
func (s *Snapshot) sharedLAN() *LAN {
	l := &LAN{ID: SharedLANID, Name: "共享给我", Members: []PersonID{s.Me}, Services: map[DeviceID][]Service{}}
	if s.Self != "" {
		l.Devices = append(l.Devices, s.Self)
	}
	for _, sh := range s.Shared {
		if !slices.Contains(l.Members, sh.From) {
			l.Members = append(l.Members, sh.From)
		}
		l.Devices = append(l.Devices, sh.Device)
		svcs := slices.Clone(sh.Services)
		for i := range svcs {
			svcs[i].On = true
		}
		l.Services[sh.Device] = svcs
		if p, ok := s.Paths[sh.Device]; ok && p.Relay != "" && !slices.Contains(l.Relays, p.Relay) {
			l.Relays = append(l.Relays, p.Relay)
		}
	}
	return l
}
