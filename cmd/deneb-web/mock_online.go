package main

// A device going offline and coming back, a LAN renamed, someone coming or
// going, as the world would have it, for the web's recordings of what
// changes (the 19th batch: a device's line, the header's, the sidebar's,
// the card's). POST /api/v1/_mock/online {"device": …, "online": false}
// sets the first; the view goes out as usual.

import (
	"net/http"
	"time"

	"github.com/kittors/deneb/proto"
)

func (m *mock) onlineSet(w http.ResponseWriter, r *http.Request, _ proto.PersonID) {
	var req struct {
		Device proto.DeviceID `json:"device"`
		Online bool           `json:"online"`
	}
	if !decode(w, r, &req) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.devices[req.Device]
	if d == nil {
		http.Error(w, "no such device", http.StatusNotFound)
		return
	}
	d.v.Online = req.Online
	if !req.Online {
		// Seen last a moment ago: its line says when.
		d.v.LastSeen = m.now().Add(-2 * time.Minute)
	}
	m.bump()
	writeJSON(w, map[string]any{"device": req.Device, "online": req.Online})
}

// lanName renames a LAN as its owner would elsewhere: POST
// /api/v1/_mock/lan-name {"lan": …, "name": …} (the header's title, the
// sidebar's row).
func (m *mock) lanName(w http.ResponseWriter, r *http.Request, _ proto.PersonID) {
	var req struct {
		LAN  proto.LANID `json:"lan"`
		Name string      `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.lan(req.LAN)
	if l == nil {
		http.Error(w, "no such LAN", http.StatusNotFound)
		return
	}
	l.name = req.Name
	m.bump()
	writeJSON(w, map[string]any{"lan": req.LAN, "name": req.Name})
}

// memberSet has someone come into a LAN or leave it: POST
// /api/v1/_mock/member {"lan": …, "person": …, "in": true} (the header's
// balls, the sidebar row's).
func (m *mock) memberSet(w http.ResponseWriter, r *http.Request, _ proto.PersonID) {
	var req struct {
		LAN    proto.LANID    `json:"lan"`
		Person proto.PersonID `json:"person"`
		In     bool           `json:"in"`
	}
	if !decode(w, r, &req) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l := m.lan(req.LAN)
	if l == nil || m.person(req.Person) == nil {
		http.Error(w, "no such LAN or person", http.StatusNotFound)
		return
	}
	at := -1
	for i, x := range l.members {
		if x.Person == req.Person {
			at = i
		}
	}
	switch {
	case req.In && at < 0:
		l.members = append(l.members, proto.Member{Person: req.Person, Role: proto.RoleMember, State: proto.MemberJoined, JoinedAt: m.now()})
	case !req.In && at >= 0:
		l.members = append(l.members[:at:at], l.members[at+1:]...)
	}
	m.bump()
	writeJSON(w, map[string]any{"lan": req.LAN, "person": req.Person, "in": req.In})
}
