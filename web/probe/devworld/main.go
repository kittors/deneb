// Command devworld fills a development control server (deneb-control -dev)
// with the people, LANs and devices of the design's samples, for the web's
// tests and recordings: real control-server data and code, nothing on the
// live server.
//
// Each device logs in as an engine would (its own machine key, approved by
// /dev/approve) and holds its stream, so it is online; one meant to be
// offline logs in and drops its stream. The world is built with the same
// operations the apps send (/api/v1/ops, as each person). Then devworld
// stays up, holding the streams, and serves on -listen:
//
//	GET  /ids                       the world's keys → server ids, as JSON
//	POST /do {as, op, args}         an operation as someone ("as": a person's
//	                                key); string args may name things by key:
//	                                device "lin.laptop", person "jie", lan
//	                                "weekend", share "lin.laptop>jie"
//	POST /device/<key>/offline      its stream drops (offline after the grace)
//	POST /device/<key>/online       it streams again
//
// usage:
//
//	devworld -seed /tmp/dev/seed.json           write the dev seed (people), then exit
//	deneb-control -dev -dev-reset -seed /tmp/dev/seed.json -listen 127.0.0.1:47190 -data /tmp/dev/data
//	devworld -control http://127.0.0.1:47190 -listen 127.0.0.1:47199 -state /tmp/dev/world.json
//
// For recordings that must start from the very same world (the same ids,
// so the same colors in light and dark), keep a copy of the server's data
// directory and world.json once built; then, for each run, put the copy
// back, start the server without -dev-reset, and run devworld -resume:
// it logs the devices in again with their machine keys and holds their
// streams, building nothing.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/control"
	"github.com/kittors/deneb/engine/controlclient"
	"github.com/kittors/deneb/proto"
)

// ---- The world --------------------------------------------------------------------

type personSpec struct {
	key, email, display, initial string
}

var people = []personSpec{
	{"kittors", "kittors@deneb.test", "kittors", "K"},
	{"jie", "jie@deneb.test", "阿杰", "杰"},
	{"mia", "mia@deneb.test", "Mia", "M"},
	{"leo", "leo@deneb.test", "Leo", "L"},
	{"lin", "lin@deneb.test", "小林", "林"},
	{"ming", "ming@deneb.test", "阿明", "明"},
}

type svc struct {
	kind, name, port string
}

var (
	screen = svc{"screen", "远程桌面", "tcp/5900"}
	rdp    = svc{"screen", "远程桌面", "tcp/3389"}
	files  = svc{"files", "共享文件夹", "tcp/445"}
	web    = svc{"web", "网站", "tcp/80"}
	shell  = svc{"shell", "命令行", "tcp/22"}
	game   = svc{"game", "游戏", "tcp/27015"}
	db     = svc{"other", "数据库", "tcp/5432"}
	print  = svc{"other", "打印", "tcp/631"}
)

// deviceSpec is a device as its owner set it up. Its name is what its owner
// called it, as an engine's --name gives it: the server shows it to everyone
// as it is ("阿杰的 NAS"), so others' devices carry their owner's name.
type deviceSpec struct {
	key, owner, host, display, kind, os string
	offline                             bool
	services                            []svc
}

var devices = []deviceSpec{
	{"kittors.mac", "kittors", "kittors-mac", "MacBook", "laptop", "macos", false, []svc{screen, files}},
	{"kittors.game", "kittors", "game", "游戏主机", "console", "other", false, []svc{game}},
	{"kittors.studio", "kittors", "studio", "工作室主机", "mini", "macos", false, []svc{screen, shell}},
	{"kittors.nas", "kittors", "nas", "NAS", "nas", "linux", false, []svc{files, web}},
	{"kittors.gpu", "kittors", "gpu", "GPU 主机", "server", "linux", false, []svc{shell, screen}},
	{"kittors.build", "kittors", "build", "构建服务器", "server", "linux", false, []svc{shell, web}},
	{"kittors.files", "kittors", "files", "文件服务器", "nas", "linux", false, []svc{files}},
	{"kittors.db", "kittors", "db", "开发数据库", "server", "linux", false, []svc{db}},
	{"kittors.print", "kittors", "print", "打印机", "other", "linux", false, []svc{print}},
	{"kittors.test", "kittors", "test", "测试机", "mini", "linux", false, []svc{screen, shell}},
	{"mia.mac", "mia", "mia-mac", "Mia 的 MacBook", "laptop", "macos", false, []svc{screen, files}},
	{"jie.desk", "jie", "desktop", "阿杰的台式机", "desktop", "windows", false, []svc{rdp, web}},
	{"jie.nas", "jie", "nas", "阿杰的 NAS", "nas", "linux", true, []svc{files, screen, web}},
	{"jie.pi", "jie", "pi", "阿杰的树莓派", "mini", "linux", false, []svc{shell}},
	{"lin.laptop", "lin", "lin-mac", "小林的笔记本", "laptop", "macos", false, []svc{screen, files}},
}

// lanSpec is a LAN: its owner, mode, who is in it, and what is in it. A
// member invited by someone other than the owner waits for the owner when
// the LAN asks (approveMembers); a device added by a member waits likewise.
type lanSpec struct {
	key, name, owner, mode string
	members                []string // invited by the owner: in at once
	byMember               [][2]string
	devices                []string // in the LAN, approved
	pending                []string // added by their owners, waiting
	public                 []string // the owner's, public (share mode)
	off                    map[string][]string
}

var lans = []lanSpec{
	{
		key: "weekend", name: "周末联机", owner: "kittors", mode: "lan",
		members:  []string{"mia", "jie"},
		byMember: [][2]string{{"mia", "ming"}}, // 阿明, invited by Mia: waits for kittors (and a seat of his)
		devices:  []string{"kittors.mac", "kittors.game", "mia.mac", "jie.desk", "jie.nas"},
		pending:  []string{"jie.pi"},
		off:      map[string][]string{"kittors.mac": {"tcp/445"}},
	},
	{
		key: "studio", name: "工作室", owner: "kittors", mode: "share",
		members: []string{"lin", "jie", "mia", "leo"},
		devices: []string{"kittors.studio"},
		public:  []string{"kittors.gpu", "kittors.build", "kittors.files", "kittors.db", "kittors.print", "kittors.test"},
	},
	{
		key: "games", name: "桌游夜", owner: "kittors", mode: "lan",
		members: []string{"leo"},
		devices: []string{"kittors.game"},
	},
}

// relaySpec is a relay of the world: kittors's own server, or a public one
// the deployment runs (kittors is its operator). It registers as the relay
// program does, with the join code of the install command, and then syncs
// every few seconds: the devices on it, what they sent this month, and its
// load. Its name is the place it reports; with none, the server names it
// by its address.
type relaySpec struct {
	key, name, addr string
	public          bool
	lans            []string // the LANs that use it (my own)
	weight          int      // public: the operators' share; 0 keeps it standby
	on              []string // the devices connected through it
	cpu             float64
	gb              float64 // what each device on it sent this month, in GB, at the first sync
}

var relays = []relaySpec{
	{key: "office", name: "公司机房", addr: "198.51.100.4", lans: []string{"weekend", "studio"}, on: []string{"kittors.mac", "mia.mac", "jie.desk"}, cpu: 0.12, gb: 4.1},
	{key: "sh", name: "上海", addr: "198.51.100.17", lans: []string{"weekend"}, on: []string{"kittors.game", "jie.desk"}, cpu: 0.08, gb: 1.6},
	{key: "fresh", addr: "203.0.113.9", cpu: 0.02},
	{key: "pub-hk", name: "香港", addr: "203.0.113.55", public: true, weight: 60, on: []string{"lin.laptop", "mia.mac", "kittors.studio", "jie.pi", "kittors.gpu"}, cpu: 0.62, gb: 9.5},
	{key: "pub-tokyo", name: "东京", addr: "203.0.113.66", public: true, weight: 30, on: []string{"jie.desk", "kittors.db", "mia.mac"}, cpu: 0.21, gb: 5.2},
	{key: "pub-fra", name: "法兰克福", addr: "203.0.113.20", public: true, weight: 10, on: []string{"kittors.print", "lin.laptop"}, cpu: 0.09, gb: 1.4},
}

// shares are device → person, by the device's owner.
var shares = [][2]string{
	{"kittors.build", "lin"}, {"kittors.print", "lin"}, {"kittors.gpu", "jie"}, {"kittors.test", "jie"}, {"kittors.files", "mia"},
}

// ---- People: the web API, as each of them --------------------------------------------

type person struct {
	spec personSpec
	c    *http.Client
	id   proto.PersonID
}

func newPerson(s personSpec) *person {
	jar, _ := cookiejar.New(nil)
	return &person{spec: s, c: &http.Client{Jar: jar, Timeout: 20 * time.Second}}
}

func (p *person) post(path string, body, out any) error {
	b, _ := json.Marshal(body)
	resp, err := p.c.Post(*controlURL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %d %s", p.spec.key, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (p *person) login() error {
	var r struct {
		Person proto.PersonID `json:"person"`
	}
	if err := p.post(proto.PathWebLogin, map[string]string{"email": p.spec.email, "code": control.DevCode}, &r); err != nil {
		return err
	}
	p.id = r.Person
	return nil
}

var opSeq int

func (p *person) op(op string, args map[string]any) (*proto.OpResult, error) {
	opSeq++
	var r proto.OpResult
	err := p.post(proto.PathWebOps, map[string]any{"id": fmt.Sprintf("devworld-%d-%d", time.Now().UnixNano(), opSeq), "op": op, "args": args}, &r)
	return &r, err
}

func (p *person) view() (*proto.UserView, error) {
	resp, err := p.c.Get(*controlURL + proto.PathWebUserView)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var v proto.UserView
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("userview: %d", resp.StatusCode)
	}
	return &v, json.NewDecoder(resp.Body).Decode(&v)
}

// ---- Devices: logged in as engines, holding their streams --------------------------------

type device struct {
	spec    deviceSpec
	cl      *controlclient.Client
	id      proto.DeviceID
	machine proto.MachinePrivate
	node    proto.NodePublic
	mu      sync.Mutex
	cancel  context.CancelFunc
}

// login logs the device in as an engine: a new machine key approved by
// /dev/approve; or, resuming, the machine key it had, still logged in.
func (d *device) login(ctx context.Context, owner *person) error {
	if d.machine == (proto.MachinePrivate{}) {
		d.machine = proto.NewMachineKey()
	}
	cl, err := controlclient.New(controlclient.Options{URL: *controlURL, Machine: d.machine, Logf: func(string, ...any) {}})
	if err != nil {
		return err
	}
	d.cl = cl
	if d.node == (proto.NodePublic{}) {
		rand.Read(d.node[:])
	}
	nk := d.node
	var dk proto.DiscoPublic
	rand.Read(dk[:])
	st, err := cl.Login(ctx, proto.LoginRequest{NodeKey: nk, DiscoKey: dk, Hostname: d.spec.host, OS: proto.OS(d.spec.os), DisplayName: d.spec.display, Kind: proto.DeviceKind(d.spec.kind), EngineVersion: "devworld"})
	if err != nil {
		return fmt.Errorf("%s: login: %w", d.spec.key, err)
	}
	if !st.LoggedIn {
		var r proto.LoginState
		if err := owner.post("/dev/approve", map[string]string{"login": st.LoginURL, "email": owner.spec.email}, &r); err != nil {
			return fmt.Errorf("%s: approve: %w", d.spec.key, err)
		}
		for i := 0; i < 5 && !st.LoggedIn; i++ {
			if st, err = cl.LoginWait(ctx); err != nil {
				return fmt.Errorf("%s: wait: %w", d.spec.key, err)
			}
		}
	}
	if !st.LoggedIn {
		return fmt.Errorf("%s: not logged in", d.spec.key)
	}
	d.id = st.Device
	return nil
}

// stream holds the device's stream (it is online) until stop.
func (d *device) stream() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	go func() {
		for ctx.Err() == nil {
			_ = d.cl.Stream(ctx, 0, false, func(controlclient.Frame) {})
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
	}()
}

func (d *device) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
}

// ---- Relays: as the relay program, without carrying anything ------------------------------

type relay struct {
	spec   relaySpec
	id     proto.RelayID
	secret string
	w      *world
	start  time.Time
	mu     sync.Mutex
	denied map[proto.NodePublic]time.Time // turned away: off it for a while, then tried again
	synced bool
}

// register uses a join code as the install command would.
func (r *relay) register(code string) error {
	_, _, token, err := proto.ParseRelayJoinCode(code)
	if err != nil {
		return err
	}
	var pin [32]byte
	rand.Read(pin[:])
	req := proto.RelayRegisterRequest{Token: token, RelayInfo: r.info("sha256:" + hex.EncodeToString(pin[:]))}
	var res proto.RelayRegisterResult
	if err := relayCall(proto.PathRelayRegister, "", req, &res); err != nil {
		return fmt.Errorf("%s: register: %w", r.spec.key, err)
	}
	r.id, r.secret = res.Relay, res.Secret
	return nil
}

func (r *relay) info(pin string) proto.RelayInfo {
	in := proto.RelayInfo{IPv4: netip.MustParseAddr(r.spec.addr), DERPPort: 47445, STUNPort: 47478, Pin: pin, Version: "devworld"}
	if r.spec.name != "" {
		in.Region = "name:" + r.spec.name
	}
	return in
}

func relayCall(path, secret string, body, out any) error {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", *controlURL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%d %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// run syncs every 3 seconds: the devices on it that are online and not
// turned away; at the first sync, what they sent this month; then a
// trickle; and its load, about steady.
func (r *relay) run() {
	if r.start.IsZero() {
		r.start = time.Now().Add(-time.Duration(len(r.spec.key)) * 24 * time.Hour)
	}
	for {
		r.sync()
		time.Sleep(3 * time.Second)
	}
}

func (r *relay) sync() {
	r.mu.Lock()
	var req proto.RelaySyncRequest
	req.Connected = []proto.NodePublic{}
	now := time.Now()
	for _, k := range r.spec.on {
		d := r.w.devices[k]
		if d == nil || d.cancel == nil {
			continue // offline
		}
		if t, ok := r.denied[d.node]; ok && now.Sub(t) < 20*time.Second {
			continue
		}
		req.Connected = append(req.Connected, d.node)
		sent := int64(40 << 10)
		if !r.synced {
			sent = int64(r.spec.gb * 1e9 / float64(len(req.Connected)))
		}
		req.Usage = append(req.Usage, proto.RelayClientUsage{Node: d.node, Sent: sent, Received: sent / 3})
	}
	n := len(req.Connected)
	req.Health = &proto.RelayHealth{Connections: n * 2, BytesPerSec: int64(n) * 380_000, CPU: r.spec.cpu, Memory: 48 << 20, MemoryLimit: 512 << 20, Uptime: int64(now.Sub(r.start).Seconds())}
	r.mu.Unlock()
	var res proto.RelaySyncResult
	if err := relayCall(proto.PathRelaySync, r.secret, req, &res); err != nil {
		log.Printf("relay %s: sync: %v", r.spec.key, err)
		return
	}
	r.mu.Lock()
	r.synced = true
	for _, nk := range res.Deny {
		r.denied[nk] = now
	}
	r.mu.Unlock()
}

// ---- Building it ------------------------------------------------------------------

var (
	controlURL = flag.String("control", "http://127.0.0.1:47190", "the development control server")
	listen     = flag.String("listen", "127.0.0.1:47199", "where /ids, /do and /device/… are served")
	seedOut    = flag.String("seed", "", "write the development seed (the world's people) to this file, then exit")
	statePath  = flag.String("state", "", "where the built world's ids and machine keys are written (read with -resume)")
	resume     = flag.Bool("resume", false, "take up the world written to -state (its server's data put back): log the devices in again, build nothing")
)

// saved is what -state keeps: the ids, and each device's machine key.
type saved struct {
	Devices map[string]struct {
		ID      proto.DeviceID `json:"id"`
		Machine string         `json:"machine"`
		Node    string         `json:"node"`
	} `json:"devices"`
	Relays  map[string]savedRelay  `json:"relays"`
	LANs    map[string]proto.LANID `json:"lans"`
	Invites map[string]string      `json:"invites"`
}

type savedRelay struct {
	ID     proto.RelayID `json:"id"`
	Secret string        `json:"secret"`
}

type world struct {
	people  map[string]*person
	devices map[string]*device
	relays  map[string]*relay
	lans    map[string]proto.LANID
	invites map[string]string // lan key → an unused invite link by its owner (the join tests' Leo)
	relayMu sync.Mutex        // relays: /install adds to it while /ids reads it
}

func main() {
	flag.Parse()
	log.SetFlags(log.Ltime)
	if *seedOut != "" {
		var s control.Seed
		for _, p := range people {
			s.Accounts = append(s.Accounts, control.SeedAccount{Email: p.email, DisplayName: p.display, Initial: p.initial, Name: p.key, Code: control.DevCode})
		}
		b, _ := json.MarshalIndent(s, "", "  ")
		if err := os.WriteFile(*seedOut, append(b, '\n'), 0o600); err != nil {
			log.Fatal(err)
		}
		fmt.Println("wrote", *seedOut)
		return
	}
	var w *world
	var err error
	if *resume {
		w, err = takeUp()
	} else {
		w, err = build()
		if err == nil && *statePath != "" {
			err = w.save(*statePath)
		}
	}
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("world built: %d people, %d devices, %d LANs; serving %s", len(w.people), len(w.devices), len(w.lans), *listen)
	go func() {
		if err := http.ListenAndServe(*listen, w.handler()); err != nil {
			log.Fatal(err)
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}

func must(r *proto.OpResult, err error) *proto.OpResult {
	if err != nil {
		log.Fatal(err)
	}
	return r
}

func build() (*world, error) {
	ctx := context.Background()
	w := &world{people: map[string]*person{}, devices: map[string]*device{}, lans: map[string]proto.LANID{}, invites: map[string]string{}}
	for _, s := range people {
		p := newPerson(s)
		if err := p.login(); err != nil {
			return nil, err
		}
		w.people[s.key] = p
	}
	for _, s := range devices {
		d := &device{spec: s}
		if err := d.login(ctx, w.people[s.owner]); err != nil {
			return nil, err
		}
		d.stream()
		w.devices[s.key] = d
	}
	// The services each device has set up: on in the first LAN it is in
	// (the catalog), off where a LAN's spec says so.
	for _, l := range lans {
		owner := w.people[l.owner]
		r := must(owner.op("lan.create", map[string]any{"name": l.name}))
		id := r.Result.LAN
		w.lans[l.key] = id
		// Asked for at once: the members it brings in now wait or not as
		// the spec says; turned on again at the end.
		must(owner.op("lan.setApproveDevices", map[string]any{"lan": id, "on": false}))
		must(owner.op("lan.setApproveMembers", map[string]any{"lan": id, "on": false}))
		for _, m := range l.members {
			inv := must(owner.op("lan.invite", map[string]any{"lan": id}))
			must(w.people[m].op("invite.accept", map[string]any{"invite": inv.Result.Invite.URL}))
		}
		for _, dk := range append(append([]string{}, l.devices...), l.public...) {
			d := w.devices[dk]
			must(w.people[d.spec.owner].op("lan.addDevice", map[string]any{"lan": id, "device": d.id}))
			for _, s := range d.spec.services {
				on := true
				for _, p := range l.off[dk] {
					if p == s.port {
						on = false
					}
				}
				args := map[string]any{"lan": id, "device": d.id, "service": map[string]any{"kind": s.kind, "name": s.name, "ports": []string{s.port}}, "on": true}
				must(w.people[d.spec.owner].op("service.set", args))
				if !on {
					args["on"] = false
					must(w.people[d.spec.owner].op("service.set", args))
				}
			}
		}
		for _, dk := range l.public {
			must(owner.op("device.setPublic", map[string]any{"lan": id, "device": w.devices[dk].id}))
		}
		if l.mode == "share" {
			must(owner.op("lan.setMode", map[string]any{"lan": id, "mode": "share"}))
		}
		must(owner.op("lan.setApproveDevices", map[string]any{"lan": id, "on": true}))
		must(owner.op("lan.setApproveMembers", map[string]any{"lan": id, "on": true}))
		// What waits for the owner: devices added by members now, and
		// people let in by members.
		for _, dk := range l.pending {
			d := w.devices[dk]
			must(w.people[d.spec.owner].op("lan.addDevice", map[string]any{"lan": id, "device": d.id}))
		}
		for _, bm := range l.byMember {
			inv := must(w.people[bm[0]].op("lan.invite", map[string]any{"lan": id}))
			must(w.people[bm[1]].op("invite.accept", map[string]any{"invite": inv.Result.Invite.URL}))
		}
		inv := must(owner.op("lan.invite", map[string]any{"lan": id}))
		w.invites[l.key] = inv.Result.Invite.URL
	}
	// Each share opens what the device has (as the apps do: what it opens
	// to its LANs).
	for _, s := range shares {
		d := w.devices[s[0]]
		var keys []string
		for _, v := range d.spec.services {
			keys = append(keys, v.port)
		}
		must(w.people[d.spec.owner].op("share.create", map[string]any{"device": d.id, "person": w.people[s[1]].id, "services": keys}))
	}
	// Offline ones: they were online long enough to be seen, then went.
	for _, d := range w.devices {
		if d.spec.offline {
			d.stop()
		}
	}
	// Relays: kittors's own, then the public ones he runs as the
	// deployment's operator (deneb-control operators -add, before this).
	k := w.people["kittors"]
	w.relays = map[string]*relay{}
	for _, rs := range relays {
		args := map[string]any{}
		if rs.public {
			args["public"] = true
			args["name"] = rs.name
		}
		res := must(k.op("relay.join", args))
		r := &relay{spec: rs, w: w, denied: map[proto.NodePublic]time.Time{}}
		if err := r.register(res.Result.RelayJoin.Code); err != nil {
			return nil, err
		}
		for _, l := range rs.lans {
			must(k.op("relay.add", map[string]any{"relay": r.id, "lan": w.lans[l]}))
		}
		if rs.weight > 0 {
			must(k.op("relay.setWeight", map[string]any{"relay": r.id, "weight": rs.weight}))
		}
		w.relays[rs.key] = r
		go r.run()
	}
	return w, nil
}

func (w *world) save(path string) error {
	var s saved
	s.Devices = map[string]struct {
		ID      proto.DeviceID `json:"id"`
		Machine string         `json:"machine"`
		Node    string         `json:"node"`
	}{}
	for k, d := range w.devices {
		s.Devices[k] = struct {
			ID      proto.DeviceID `json:"id"`
			Machine string         `json:"machine"`
			Node    string         `json:"node"`
		}{d.id, hex.EncodeToString(d.machine[:]), hex.EncodeToString(d.node[:])}
	}
	s.Relays = map[string]savedRelay{}
	for k, r := range w.relays {
		s.Relays[k] = savedRelay{r.id, r.secret}
	}
	s.LANs, s.Invites = w.lans, w.invites
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(path, b, 0o600)
}

// takeUp is -resume: the people log in, the devices log in with the
// machine keys they had (still logged in on the server) and the ones meant
// to be online hold their streams.
func takeUp() (*world, error) {
	b, err := os.ReadFile(*statePath)
	if err != nil {
		return nil, err
	}
	var s saved
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	w := &world{people: map[string]*person{}, devices: map[string]*device{}, lans: s.LANs, invites: s.Invites}
	for _, ps := range people {
		p := newPerson(ps)
		if err := p.login(); err != nil {
			return nil, err
		}
		w.people[ps.key] = p
	}
	ctx := context.Background()
	for _, ds := range devices {
		sd, ok := s.Devices[ds.key]
		if !ok {
			return nil, fmt.Errorf("%s: not in %s", ds.key, *statePath)
		}
		d := &device{spec: ds}
		k, err := hex.DecodeString(sd.Machine)
		if err != nil || len(k) != len(d.machine) {
			return nil, fmt.Errorf("%s: bad machine key", ds.key)
		}
		copy(d.machine[:], k)
		if nk, err := hex.DecodeString(sd.Node); err == nil && len(nk) == len(d.node) {
			copy(d.node[:], nk)
		}
		if err := d.login(ctx, w.people[ds.owner]); err != nil {
			return nil, err
		}
		if d.id != sd.ID {
			return nil, fmt.Errorf("%s: came back as %s, not %s", ds.key, d.id, sd.ID)
		}
		// Every device streams; the ones meant to be offline drop a moment
		// later (offline after the server's grace, seen "刚刚").
		d.stream()
		if ds.offline {
			time.AfterFunc(1500*time.Millisecond, d.stop)
		}
		w.devices[ds.key] = d
	}
	w.relays = map[string]*relay{}
	for _, rs := range relays {
		sr, ok := s.Relays[rs.key]
		if !ok {
			continue
		}
		// Its month is in the data put back already: only the trickle now.
		r := &relay{spec: rs, w: w, id: sr.ID, secret: sr.Secret, denied: map[proto.NodePublic]time.Time{}, synced: true}
		w.relays[rs.key] = r
		go r.run()
	}
	return w, nil
}

// ---- Serving ----------------------------------------------------------------------

func (w *world) ids() map[string]any {
	out := map[string]any{}
	devs, ppl, ls := map[string]string{}, map[string]string{}, map[string]string{}
	for k, d := range w.devices {
		devs[k] = string(d.id)
	}
	for k, p := range w.people {
		ppl[k] = string(p.id)
	}
	for k, id := range w.lans {
		ls[k] = string(id)
	}
	rs := map[string]string{}
	w.relayMu.Lock()
	for k, r := range w.relays {
		rs[k] = string(r.id)
	}
	w.relayMu.Unlock()
	out["devices"], out["people"], out["lans"], out["invites"], out["relays"] = devs, ppl, ls, w.invites, rs
	return out
}

// resolve turns keys in args into server ids.
func (w *world) resolve(as *person, args map[string]any) error {
	for k, v := range args {
		s, ok := v.(string)
		if !ok {
			continue
		}
		switch k {
		case "device":
			if d := w.devices[s]; d != nil {
				args[k] = string(d.id)
			}
		case "person":
			if p := w.people[s]; p != nil {
				args[k] = string(p.id)
			}
		case "lan":
			if id, ok := w.lans[s]; ok {
				args[k] = string(id)
			}
		case "relay":
			if r := w.relays[s]; r != nil {
				args[k] = string(r.id)
			}
		case "share":
			dk, pk, ok := strings.Cut(s, ">")
			if !ok {
				continue
			}
			d, p := w.devices[dk], w.people[pk]
			if d == nil || p == nil {
				return fmt.Errorf("no %s", s)
			}
			found := ""
			for _, viewer := range []*person{as, w.people[d.spec.owner]} {
				v, err := viewer.view()
				if err != nil {
					return err
				}
				for _, dv := range v.Devices {
					for _, sh := range dv.Shares {
						if dv.ID == d.id && sh.Person == p.id {
							found = string(sh.ID)
						}
					}
				}
				for _, sd := range v.SharedWithMe {
					if sd.Device.ID == d.id && viewer.id == p.id {
						found = string(sd.Share)
					}
				}
			}
			if found == "" {
				return fmt.Errorf("no share %s", s)
			}
			args[k] = found
		}
	}
	return nil
}

func (w *world) handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /ids", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(w.ids())
	})
	m.HandleFunc("POST /do", func(rw http.ResponseWriter, r *http.Request) {
		var req struct {
			As   string         `json:"as"`
			Op   string         `json:"op"`
			Args map[string]any `json:"args"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(rw, err.Error(), 400)
			return
		}
		p := w.people[req.As]
		if p == nil {
			http.Error(rw, "no person "+req.As, 404)
			return
		}
		if req.Args == nil {
			req.Args = map[string]any{}
		}
		if err := w.resolve(p, req.Args); err != nil {
			http.Error(rw, err.Error(), 404)
			return
		}
		res, err := p.op(req.Op, req.Args)
		if err != nil {
			http.Error(rw, err.Error(), 409)
			return
		}
		log.Printf("%s %s %v", req.As, req.Op, req.Args)
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(res)
	})
	// POST /install runs an install command as the server it is shown for
	// would (the web's "用我自己的服务器", "加一台公共中转站"): the join code
	// in it registers a new relay, here, which syncs from then on, with no
	// one on it yet. Body: {"command": "...", "name": "新加坡", "addr":
	// "203.0.113.7"}; no name: it has only its address, as a server whose
	// place is not known.
	m.HandleFunc("POST /install", func(rw http.ResponseWriter, r *http.Request) {
		var req struct{ Command, Name, Addr string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(rw, err.Error(), 400)
			return
		}
		code := ""
		for _, f := range strings.Fields(req.Command) {
			if strings.HasPrefix(f, proto.RelayJoinPrefix) {
				code = f
			}
		}
		if _, err := netip.ParseAddr(req.Addr); err != nil || code == "" {
			http.Error(rw, "a command with a join code, and an address", 400)
			return
		}
		rl := &relay{spec: relaySpec{key: "installed-" + req.Addr, name: req.Name, addr: req.Addr, cpu: 0.03}, w: w, denied: map[proto.NodePublic]time.Time{}, start: time.Now()}
		if err := rl.register(code); err != nil {
			http.Error(rw, err.Error(), 409)
			return
		}
		w.relayMu.Lock()
		w.relays[rl.spec.key] = rl
		w.relayMu.Unlock()
		go rl.run()
		log.Printf("installed %s (%s) as %s", req.Addr, req.Name, rl.id)
		rw.Header().Set("Content-Type", "application/json")
		json.NewEncoder(rw).Encode(map[string]string{"relay": string(rl.id)})
	})
	m.HandleFunc("POST /device/{key}/{state}", func(rw http.ResponseWriter, r *http.Request) {
		d := w.devices[r.PathValue("key")]
		if d == nil {
			http.Error(rw, "no device", 404)
			return
		}
		switch r.PathValue("state") {
		case "online":
			d.stream()
		case "offline":
			d.stop()
		default:
			http.Error(rw, "online or offline", 400)
			return
		}
		rw.WriteHeader(204)
	})
	return m
}
