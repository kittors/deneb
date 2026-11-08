package client_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kittors/deneb/client"
	"github.com/kittors/deneb/client/model"
	"github.com/kittors/deneb/engine/localapi/localclient"
)

// TestRealEngine runs the live store against a real engine and a control
// server in its development mode, when DENEB_REAL_SOCKET names the engine's
// socket and DENEB_REAL_CONTROL the server (for instance
// http://127.0.0.1:47181): it logs in without a browser (/dev/approve),
// makes a LAN, invites into it and reads what the snapshot says of it all.
// What the engine and the server send is what the client reads, not what
// the fake world makes up.
func TestRealEngine(t *testing.T) {
	sock, control := os.Getenv("DENEB_REAL_SOCKET"), os.Getenv("DENEB_REAL_CONTROL")
	if sock == "" || control == "" {
		t.Skip("set DENEB_REAL_SOCKET and DENEB_REAL_CONTROL to run against a real engine")
	}
	l := client.NewLive(localclient.New(sock))
	l.Start()
	defer l.Close()
	s := waitFor(t, l, "the engine", func(s *model.Snapshot) bool { return s.Engine })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if !s.LoggedIn {
		link, err := l.Login(ctx)
		if err != nil || link == "" {
			t.Fatalf("login: %q, %v", link, err)
		}
		t.Logf("login link %s", link)
		body, _ := json.Marshal(map[string]string{"login": link, "email": "kittors@deneb.test"})
		resp, err := http.Post(strings.TrimRight(control, "/")+"/dev/approve", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			t.Fatalf("/dev/approve: %s", resp.Status)
		}
	}
	s = waitFor(t, l, "logged in, the world loaded", func(s *model.Snapshot) bool { return s.LoggedIn && s.Loaded && s.Self != "" })
	me := s.People[s.Me]
	self := s.Devices[s.Self]
	if me == nil || self == nil {
		t.Fatalf("no me (%v) or no this computer (%v) in the snapshot", me, self)
	}
	t.Logf("me %+v; this computer %+v", *me, *self)
	if me.Color != 0 || me.Initial == "" || me.Name == "" {
		t.Errorf("I show as %+v", *me)
	}
	if self.Owner != s.Me || self.Addr == "" || !strings.HasSuffix(self.FQDN, ".deneb.internal") {
		t.Errorf("this computer is %+v", *self)
	}
	if !slicesContains(s.Mine, s.Self) {
		t.Errorf("this computer is not among my devices %v", s.Mine)
	}

	// 工作室, made now, or on a run before against the same server (which
	// takes no second LAN of a name: "你已经在一个叫「工作室」的局域网里了").
	var lan *model.LAN
	for _, x := range s.LANs {
		if x.Name == "工作室" {
			lan = x
		}
	}
	if lan == nil {
		r, err := l.Do(ctx, model.Op{Kind: model.OpLANCreate, Name: "工作室"})
		if err != nil {
			t.Fatal(err)
		}
		s = waitFor(t, l, "工作室", func(s *model.Snapshot) bool { return s.Version >= r.Version && s.LAN(r.LAN) != nil })
		lan = s.LAN(r.LAN)
	}
	t.Logf("工作室 %+v", *lan)
	if lan.Name != "工作室" || lan.Owner != s.Me || !lan.Has(s.Self) || !slicesContains(lan.Members, s.Me) {
		t.Errorf("工作室 is %+v", *lan)
	}
	if len(lan.Relays) == 0 {
		t.Error("工作室 uses no relay")
	}
	if p := s.Path(s.Self); p.Kind != model.PathSelf {
		t.Errorf("this computer's path is %s", p.Kind)
	}

	inv, err := l.Do(ctx, model.Op{Kind: model.OpLANInvite, LAN: lan.ID})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("invite link %s", inv.Link)
	if !strings.Contains(inv.Link, "/i/") {
		t.Errorf("the invite link is %q", inv.Link)
	}
	pv, err := l.Preview(ctx, inv.Link)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("preview %+v", *pv)
	if pv.LANName != "工作室" || pv.Inviter.ID != s.Me || !pv.AlreadyIn {
		t.Errorf("the invite says %+v", *pv)
	}

	// A service this computer listens on, opened for the first time from
	// its card: it joins the device's list, on.
	if s = waitFor(t, l, "this computer's services", func(s *model.Snapshot) bool { return len(s.Local) > 0 }); len(s.Local) > 0 {
		svc := s.Local[0]
		r, err := l.Do(ctx, model.Op{Kind: model.OpServiceSet, LAN: lan.ID, Device: s.Self, Service: svc.Key, On: true})
		if err != nil {
			t.Fatal(err)
		}
		s = waitFor(t, l, "the service on", func(s *model.Snapshot) bool { return s.Version >= r.Version })
		got := s.LAN(lan.ID).Services[s.Self]
		t.Logf("this computer opens %+v", got)
		if len(got) == 0 || got[0].Key != svc.Key || !got[0].On {
			t.Errorf("after opening %s, this computer opens %+v", svc.Key, got)
		}
	} else {
		t.Log("nothing listens here: opening a service for the first time is not tried")
	}

	_, err = l.Do(ctx, model.Op{Kind: model.OpInviteAccept, Invite: "deneb://join/nosuchinvite"})
	var me2 *model.Error
	if !errorsAs(err, &me2) || me2.Message == "" {
		t.Errorf("a made-up invite: %v", err)
	} else {
		t.Logf("a made-up invite: %s: %s", me2.Code, me2.Message)
	}
}

func slicesContains[T comparable](s []T, v T) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func errorsAs(err error, target **model.Error) bool {
	e, ok := err.(*model.Error)
	if ok {
		*target = e
	}
	return ok
}
