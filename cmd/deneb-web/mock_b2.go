package main

import (
	"fmt"
	"strings"

	"github.com/kittors/deneb/proto"
)

// createLAN makes a LAN of mine as the control server does when it is made
// in a browser (DESIGN §14.14): me its owner and only member, no device in
// it yet; a name I am in already is refused.
func (m *mock) createLAN(me proto.PersonID, name string) (proto.LANID, *opError) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fail(400, proto.ErrInvalid, "给局域网起个名字")
	}
	for _, l := range m.lans {
		if l.joined(me) && l.name == name {
			return "", fail(409, proto.ErrConflict, "你已经在一个叫「"+name+"」的局域网里了")
		}
	}
	m.seq++
	id := proto.LANID(fmt.Sprintf("l_new%d", m.seq))
	now := m.now()
	m.lans = append(m.lans, &mlan{id: id, name: name, owner: me, mode: proto.LANModeLAN, created: now,
		members: []proto.Member{{Person: me, Role: proto.RoleOwner, State: proto.MemberJoined, JoinedAt: now}}})
	return id, nil
}
