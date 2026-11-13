package main

// The deployment's console for its operators (DESIGN §14.15): what
// deneb-control's commands show, on the web. The mock serves a deployment
// of its own for it, made up and the same every time (132 people, about
// 340 devices), apart from the LAN world above; its operator is kittors.
// The shapes and the rules are the control server's (control/console.go,
// control/opslog.go, design/plan/m7/ops-console-api.md), so that the
// demo and the real thing never differ.

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kittors/deneb/proto"
)

const opsOperator proto.PersonID = "p_kittors"

// OpsWho is a person as a row names them.
type OpsWho struct {
	ID      proto.PersonID `json:"id"`
	Name    string         `json:"name"`
	Initial string         `json:"initial"`
}

// OpsPerson is a person in the console's list.
type OpsPerson struct {
	ID            proto.PersonID `json:"id"`
	Name          string         `json:"name"`
	Initial       string         `json:"initial"`
	Email         string         `json:"email"`
	Created       time.Time      `json:"created"`
	LastSeen      time.Time      `json:"lastSeen,omitzero"`
	Online        bool           `json:"online"`
	Devices       int            `json:"devices"`
	DevicesOnline int            `json:"devicesOnline"`
	Operator      bool           `json:"operator,omitempty"`
	Test          bool           `json:"test,omitempty"`
}

// OpsDevice is a device in the console: never what it reports of its
// addresses and ports (PRIVACY §9), only its Deneb address. The made-up
// ones are in 10.53.0.0/16, a /16 a deployment may pick (control/addr.go;
// never Tailscale's 100.64.0.0/10).
type OpsDevice struct {
	ID       proto.DeviceID   `json:"id"`
	Name     string           `json:"name"`
	Kind     proto.DeviceKind `json:"kind"`
	OS       proto.OS         `json:"os"`
	Engine   string           `json:"engine"`
	State    string           `json:"state"` // online, offline, loggedOut, expired
	LastSeen time.Time        `json:"lastSeen,omitzero"`
	Expires  time.Time        `json:"expires,omitzero"`
	Address  string           `json:"address"`
	Owner    OpsWho           `json:"owner"`
}

// OpsOwnedLAN is a LAN of the person's with others in it: deleting the
// account asks what becomes of it, as deleting one's own does.
type OpsOwnedLAN struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Others int    `json:"others"`
	First  string `json:"first"` // the earliest of the others, who would get it
}

// OpsPersonDetail is the person's card.
type OpsPersonDetail struct {
	OpsPerson
	DeviceList []OpsDevice `json:"deviceList"`
	Seats      struct {
		Used    int  `json:"used"`
		Limit   int  `json:"limit"`
		Own     bool `json:"own"`
		Default int  `json:"default"`
	} `json:"seats"`
	Browsers struct {
		Count  int       `json:"count"`
		Newest time.Time `json:"newest,omitzero"`
	} `json:"browsers"`
	LANs struct {
		Count int `json:"count"`
		Owned int `json:"owned"`
	} `json:"lans"`
	Owned  []OpsOwnedLAN `json:"owned"`
	Relays int           `json:"relays"`
}

// OpsAlert is an alert, now or resolved.
type OpsAlert struct {
	Key      string    `json:"key"`
	Level    string    `json:"level"` // bad, warn
	Title    string    `json:"title"`
	Text     string    `json:"text,omitempty"`
	At       time.Time `json:"at"`
	Resolved time.Time `json:"resolved,omitzero"`
	Relay    string    `json:"relay,omitempty"`
}

// OpsOverview is the console's first page.
type OpsOverview struct {
	Alerts []OpsAlert `json:"alerts"`
	Recent []OpsAlert `json:"recent"` // the last 7 days', resolved
	Server struct {
		Started     time.Time `json:"started"`
		Version     string    `json:"version"`
		Memory      int64     `json:"memory"`
		MemoryLimit int64     `json:"memoryLimit"`
		DiskFree    float64   `json:"diskFree"`
		Backup      time.Time `json:"backup,omitzero"` // the newest
		Backups     int       `json:"backups"`
		BackupAt    string    `json:"backupAt,omitempty"` // when the newest was written, "08:12" (UTC+8)
	} `json:"server"`
	Relays struct {
		Count  int      `json:"count"`
		Online int      `json:"online"`
		Names  []string `json:"names"`
		Full   []string `json:"full"`
		Busy   []string `json:"busy"`
	} `json:"relays"`
	Counts struct {
		People      int     `json:"people"`
		NewThisWeek int     `json:"newThisWeek"`
		Devices     int     `json:"devices"`
		Online      int     `json:"online"`
		LANs        int     `json:"lans"`
		LANPeople   float64 `json:"lanPeople"`
		Browsers    int     `json:"browsers"`
	} `json:"counts"`
}

// OpsBackup is a copy of the data, in the backups' folder.
type OpsBackup struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Written time.Time `json:"written"`
}

// OpsLogin is a device waiting to log in to a test account (the operator
// lets it in: deneb-control approve).
type OpsLogin struct {
	ID       string    `json:"id"`
	Hostname string    `json:"hostname"`
	OS       proto.OS  `json:"os"`
	Created  time.Time `json:"created"`
}

// OpsRecord is a line of the operators' record: who, when, what, to whom.
type OpsRecord struct {
	At   time.Time `json:"at"`
	By   OpsWho    `json:"by"`
	Text string    `json:"text"`
}

// OpsLine is a line of the server's log, today's.
type OpsLine struct {
	Seq  int64     `json:"seq"` // a cursor: its day times opsLogDay, plus where it is in that day's file
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // login, device, op, alert, error, other
	Text string    `json:"text"`
}

type opsPerson struct {
	OpsPerson
	seatsUsed, seatsLimit int
	browsers              int
	browserNewest         time.Time
	lans, lansOwned       int
	owned                 []OpsOwnedLAN
	relays                int
}

type opsWorld struct {
	people     []*opsPerson
	devices    []*OpsDevice
	logins     []OpsLogin
	records    []OpsRecord // newest first
	log        []OpsLine
	alerts     []OpsAlert
	recent     []OpsAlert
	backups    []OpsBackup
	started    time.Time
	lastBackup time.Time
	pos        map[int64]int64 // where each day's file ends, for seq
}

const opsDefaultSeats = 6

// rnd is a small deterministic generator: the made-up deployment is the
// same every time.
type rnd uint64

func (r *rnd) next() uint64 {
	*r = *r*6364136223846793005 + 1442695040888963407
	return uint64(*r >> 33)
}
func (r *rnd) n(k int) int { return int(r.next() % uint64(k)) }

var opsNamesZH = [][2]string{
	{"王磊", "wanglei"}, {"李娜", "lina"}, {"张伟", "zhangwei"}, {"刘洋", "liuyang"}, {"陈静", "chenjing"}, {"杨帆", "yangfan"}, {"赵敏", "zhaomin"},
	{"黄涛", "huangtao"}, {"周婷", "zhouting"}, {"吴昊", "wuhao"}, {"徐明", "xuming"}, {"孙悦", "sunyue"}, {"胡军", "hujun"}, {"朱琳", "zhulin"},
	{"高翔", "gaoxiang"}, {"林晨", "linchen"}, {"何静", "hejing"}, {"郭鹏", "guopeng"}, {"马超", "machao"}, {"罗丹", "luodan"}, {"梁宇", "liangyu"},
	{"宋佳", "songjia"}, {"郑浩", "zhenghao"}, {"谢颖", "xieying"}, {"韩冰", "hanbing"}, {"唐磊", "tanglei"}, {"冯雪", "fengxue"}, {"于洋", "yuyang"},
	{"董倩", "dongqian"}, {"萧然", "xiaoran"}, {"程远", "chengyuan"}, {"曹宁", "caoning"}, {"袁梦", "yuanmeng"}, {"邓辉", "denghui"}, {"许诺", "xunuo"},
	{"傅晴", "fuqing"}, {"沈哲", "shenzhe"}, {"曾琪", "zengqi"}, {"彭博", "pengbo"}, {"吕瑶", "lvyao"}, {"苏航", "suhang"}, {"蒋丽", "jiangli"},
	{"蔡文", "caiwen"}, {"贾宁", "jianing"}, {"丁一", "dingyi"}, {"魏然", "weiran"}, {"薛峰", "xuefeng"}, {"叶青", "yeqing"}, {"阎飞", "yanfei"},
	{"余晖", "yuhui"}, {"潘越", "panyue"}, {"杜鹃", "dujuan"}, {"戴军", "daijun"}, {"夏天", "xiatian"}, {"钟毅", "zhongyi"}, {"汪洋", "wangyang"},
	{"田甜", "tiantian"}, {"任重", "renzhong"}, {"姜涛", "jiangtao"}, {"范冰", "fanbing"}, {"方圆", "fangyuan"}, {"石磊", "shilei"}, {"姚远", "yaoyuan"},
	{"谭晶", "tanjing"}, {"廖凡", "liaofan"}, {"邹静", "zoujing"}, {"熊伟", "xiongwei"}, {"金鑫", "jinxin"}, {"陆川", "luchuan"}, {"郝帅", "haoshuai"},
}

var opsNamesEN = []string{"Alex", "Amy", "Ben", "Chloe", "Daniel", "Emma", "Ethan", "Grace", "Henry", "Ivy", "Jack", "Kate", "Lucas", "Nina", "Oscar",
	"Ruby", "Sam", "Tina", "Victor", "Zoe", "Aaron", "Bella", "Chris", "Diana", "Eric", "Fiona", "Gavin", "Hannah", "Ian", "Julia", "Kevin",
	"Lily", "Max", "Nora", "Owen", "Paul", "Queenie", "Ryan", "Sara", "Tom", "Uma", "Vera", "Will", "Yuki", "Zack", "Evan", "Molly", "Felix", "Iris", "Leon", "Rose", "Jay"}

var opsDomains = []string{"example.com", "example.net", "example.org"}

// newOpsWorld makes the deployment the console shows.
func newOpsWorld(now time.Time) *opsWorld {
	w := &opsWorld{started: now.Add(-30*24*time.Hour - 5*time.Hour)}
	loc := now.Local()
	w.lastBackup = time.Date(loc.Year(), loc.Month(), loc.Day(), 4, 0, 0, 0, time.Local)
	for now.Sub(w.lastBackup) < 26*time.Hour {
		w.lastBackup = w.lastBackup.Add(-24 * time.Hour)
	}
	r := rnd(13)
	addr := 0
	dev := func(p *opsPerson, name string, kind proto.DeviceKind, os proto.OS, state string, seen time.Duration) *OpsDevice {
		addr++
		d := &OpsDevice{ID: proto.DeviceID(fmt.Sprintf("od_%03d", addr)), Name: name, Kind: kind, OS: os, Engine: "0.7.3", State: state,
			Address: fmt.Sprintf("10.53.%d.%d", addr/250, addr%250+2), Owner: OpsWho{p.ID, p.Name, p.Initial}}
		if state != "online" {
			d.LastSeen = now.Add(-seen)
		} else {
			d.LastSeen = now
		}
		if state != "expired" && state != "loggedOut" {
			d.Expires = now.Add(time.Duration(150+r.n(30)) * 24 * time.Hour)
		}
		w.devices = append(w.devices, d)
		return d
	}
	person := func(id, name, initial, email string, created time.Duration) *opsPerson {
		p := &opsPerson{OpsPerson: OpsPerson{ID: proto.PersonID(id), Name: name, Initial: initial, Email: email, Created: now.Add(-created)}}
		w.people = append(w.people, p)
		return p
	}
	day := 24 * time.Hour
	// The people of the demo's world, as the console sees them.
	k := person("p_kittors", "kittors", "K", "kittors@example.com", 62*day)
	k.Operator, k.lans, k.lansOwned, k.seatsUsed, k.browsers, k.browserNewest = true, 2, 2, 5, 1, now.Add(-3*time.Minute)
	dev(k, "MacBook", proto.KindLaptop, proto.MacOS, "online", 0)
	dev(k, "游戏主机", proto.KindConsole, "other", "online", 0)
	dev(k, "工作室主机", proto.KindMini, proto.MacOS, "online", 0)
	dev(k, "NAS", proto.KindNAS, "linux", "online", 0)
	dev(k, "旧笔记本", proto.KindLaptop, proto.MacOS, "loggedOut", 40*day)
	mia := person("p_mia", "Mia", "M", "mia@example.com", 58*day)
	mia.lans, mia.seatsUsed, mia.browsers, mia.browserNewest = 2, 1, 1, now.Add(-2*day)
	dev(mia, "MacBook", proto.KindLaptop, proto.MacOS, "online", 0)
	dev(mia, "iPhone", proto.KindPhone, "ios", "offline", 35*time.Minute)
	jie := person("p_jie", "阿杰", "杰", "jie@example.com", 57*day)
	jie.Operator, jie.lans, jie.lansOwned, jie.seatsUsed = true, 2, 0, 0
	dev(jie, "台式机", proto.KindDesktop, proto.Windows, "online", 0)
	dev(jie, "NAS", proto.KindNAS, "linux", "offline", 2*time.Hour)
	dev(jie, "旧 NAS", proto.KindNAS, "linux", "loggedOut", 96*day)
	leo := person("p_leo", "Leo", "L", "leo.chen@example.com", 21*day)
	leo.lans, leo.lansOwned, leo.seatsUsed, leo.browsers, leo.browserNewest = 3, 1, 4, 2, now.Add(-42*time.Minute)
	leo.owned = []OpsOwnedLAN{{ID: "ol_home", Name: "家里", Others: 2, First: "阿杰"}}
	leo.relays = 1
	dev(leo, "ThinkPad", proto.KindLaptop, proto.Windows, "online", 0)
	dev(leo, "MacBook", proto.KindLaptop, proto.MacOS, "offline", 26*time.Hour)
	dev(leo, "树莓派", proto.KindOther, "linux", "offline", 3*day)
	lin := person("p_lin", "小林", "林", "lin@example.com", 50*day)
	lin.lans, lin.seatsUsed, lin.browsers, lin.browserNewest = 1, 0, 1, now.Add(-5*time.Hour)
	dev(lin, "笔记本", proto.KindLaptop, proto.MacOS, "online", 0)
	dev(lin, "台式机", proto.KindDesktop, proto.Windows, "expired", 190*day)
	// The others: 124 people, made up.
	kinds := []struct {
		kind  proto.DeviceKind
		os    proto.OS
		names []string
	}{
		{proto.KindLaptop, proto.MacOS, []string{"MacBook", "MacBook Air", "MacBook Pro"}},
		{proto.KindLaptop, proto.Windows, []string{"ThinkPad", "笔记本", "Surface"}},
		{proto.KindDesktop, proto.Windows, []string{"台式机", "游戏电脑", "家里的电脑"}},
		{proto.KindMini, proto.MacOS, []string{"Mac mini", "Mac Studio"}},
		{proto.KindNAS, "linux", []string{"NAS", "群晖"}},
		{proto.KindServer, "linux", []string{"服务器", "云主机"}},
		{proto.KindPhone, "ios", []string{"iPhone"}},
		{proto.KindPhone, "android", []string{"手机"}},
		{proto.KindLaptop, "linux", []string{"笔记本"}},
	}
	zh, en := 0, 0
	for i := 0; i < 124; i++ {
		var name, local, initial string
		if i%3 == 2 {
			name = opsNamesEN[en%len(opsNamesEN)]
			local = strings.ToLower(name)
			if en >= len(opsNamesEN) {
				local += strconv.Itoa(10 + en)
			}
			initial = name[:1]
			en++
		} else {
			z := opsNamesZH[zh%len(opsNamesZH)]
			name, local = z[0], z[1]
			if zh >= len(opsNamesZH) {
				local += strconv.Itoa(10 + zh)
			}
			initial = string([]rune(name)[len([]rune(name))-1:])
			zh++
		}
		var created time.Duration
		if i < 9 {
			created = time.Duration(1+r.n(6*24)) * time.Hour // this week's newcomers
		} else {
			created = time.Duration(8+r.n(55)) * day
		}
		p := person(fmt.Sprintf("p_o%03d", i), name, initial, local+"@"+opsDomains[r.n(len(opsDomains))], created)
		p.lans = r.n(4)
		if p.lans > 0 && r.n(3) == 0 {
			p.lansOwned = 1
			p.seatsUsed = 1 + r.n(5)
		}
		if r.n(100) < 44 {
			p.browsers = 1 + r.n(2)
			p.browserNewest = now.Add(-time.Duration(r.n(72*60)) * time.Minute)
		}
		n := 1 + r.n(5)
		if r.n(5) == 0 {
			n = 1
		}
		for j := 0; j < n; j++ {
			kd := kinds[r.n(len(kinds))]
			state, seen := "online", time.Duration(0)
			switch x := r.n(100); {
			case x < 61:
			case x < 86:
				state, seen = "offline", time.Duration(5+r.n(60*24*6))*time.Minute
			case x < 94:
				state, seen = "loggedOut", time.Duration(3+r.n(80))*day
			default:
				state, seen = "expired", time.Duration(181+r.n(40))*day
			}
			if p.Created.After(now.Add(-seen)) && state != "online" {
				seen = now.Sub(p.Created) / 2
			}
			dev(p, kd.names[r.n(len(kd.names))], kd.kind, kd.os, state, seen)
		}
	}
	// The deployment's test accounts, last.
	for i, t := range []string{"a", "b", "c"} {
		p := person("p_test_"+t, "测试 "+strings.ToUpper(t), strings.ToUpper(t), "test-"+t+"@deneb.test", 61*day)
		p.Test = true
		if i == 0 {
			dev(p, "测试机", proto.KindMini, "linux", "online", 0)
		}
	}
	for _, d := range w.devices {
		if r.n(10) < 2 && d.Owner.ID != "p_kittors" {
			d.Engine = []string{"0.7.2", "0.6.9"}[r.n(2)]
		}
	}
	w.count()
	w.logins = []OpsLogin{{ID: "lg_1", Hostname: "ThinkPad-X1", OS: proto.Windows, Created: now.Add(-2 * time.Minute)}}
	by := OpsWho{"p_kittors", "kittors", "K"}
	// What deneb-control did on the server: no one's, the command's.
	cmd := OpsWho{Name: "服务器上的命令"}
	w.records = []OpsRecord{
		{now.Add(-1*day + 3*time.Hour), cmd, "把阿杰设成了部署管理员"},
		{now.Add(-1*day - 4*time.Hour + 2*time.Minute), by, "又让 Leo 用公共中转站「香港」了"},
		{now.Add(-1*day - 4*time.Hour), by, "不让 Leo 用公共中转站「香港」"},
		{now.Add(-2*day - 2*time.Hour), by, "让 Mia 在 1 个浏览器里都退出了"},
		{now.Add(-2*day - 3*time.Hour), by, "把小林的位置上限恢复成了部署的默认（6 个）"},
		{now.Add(-2*day - 5*time.Hour), by, "把小林的位置上限调成了 8"},
	}
	// Alerts: the hub full, the backup late; and the week's, gone again.
	w.alerts = []OpsAlert{
		{Key: "relay-full/r_hk", Level: "warn", Title: "公共中转站「香港」满了", Text: "现在 212 台设备在经它中转，同时最多 200 台；新连上来的先直连，连不上的要等", At: now.Add(-12 * time.Minute), Relay: "香港"},
		{Key: "backup-stale", Level: "warn", Title: "超过 26 小时没有新的备份", Text: "最新的一份是 " + backupName(w.lastBackup) + "，" + w.lastBackup.In(utc8).Format("15:04") + " 写的", At: now.Add(-2*time.Hour - 10*time.Minute)},
	}
	w.recent = []OpsAlert{
		{Key: "relay-offline/r_hk", Level: "bad", Title: "公共中转站「香港」连不上了", Text: "又连上了，断了 4 分钟", At: now.Add(-3*day - 2*time.Hour), Resolved: now.Add(-3*day - 2*time.Hour + 4*time.Minute)},
		{Key: "devices-dropped", Level: "warn", Title: "在线设备一下子少了很多", Text: "5 分钟里从 231 台掉到 160 台，10 分钟后回到 226 台", At: now.Add(-5*day + 7*time.Hour), Resolved: now.Add(-5*day + 7*time.Hour + 10*time.Minute)},
		{Key: "memory-high", Level: "warn", Title: "控制服务器的内存用得太多", Text: "到过 231 MB，后来降到 90 MB", At: now.Add(-6 * day), Resolved: now.Add(-6*day + 20*time.Minute)},
	}
	// The backups: 7 days', then a week each for 8 weeks; the newest is late
	// (each is written at 04:00, local time).
	last := w.lastBackup
	for i := 0; i < 7; i++ {
		t := last.Add(-time.Duration(i) * day)
		w.backups = append(w.backups, OpsBackup{backupName(t), 2_150_000 - int64(i)*21_000, t})
	}
	for i := 1; i <= 8; i++ {
		t := last.Add(-time.Duration(6+i*7) * day)
		w.backups = append(w.backups, OpsBackup{backupName(t), 1_900_000 - int64(i)*45_000, t})
	}
	w.makeLog(now, &r)
	return w
}

func backupName(t time.Time) string { return "deneb-" + t.Format("20060102-1504") + ".db" }

// utc8 is the zone the console's hours are in, as the server's.
var utc8 = time.FixedZone("UTC+8", 8*3600)

// The log's cursors, as the server's (control/opslog.go): a line's seq is
// its day (since 1970-01-01, UTC) times opsLogDay plus where it starts in
// that day's file; 200 a page, the newest 2000 of a kind at most.
const (
	opsLogDay  = 10_000_000_000
	opsLogPage = 200
	opsLogMost = 2000
)

func (w *opsWorld) count() {
	for _, p := range w.people {
		p.Devices, p.DevicesOnline, p.Online = 0, 0, false
		p.LastSeen = time.Time{}
	}
	byID := map[proto.PersonID]*opsPerson{}
	for _, p := range w.people {
		byID[p.ID] = p
	}
	for _, d := range w.devices {
		p := byID[d.Owner.ID]
		if p == nil {
			continue
		}
		p.Devices++
		if d.State == "online" {
			p.DevicesOnline++
			p.Online = true
		}
		if d.LastSeen.After(p.LastSeen) {
			p.LastSeen = d.LastSeen
		}
	}
}

// makeLog writes yesterday's and today's log (UTC days, as the server's
// files): what the server's own would say, the addresses documentation
// ones.
func (w *opsWorld) makeLog(now time.Time, r *rnd) {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(-24 * time.Hour)
	span := now.Sub(start)
	n := 640
	for i := 0; i < n; i++ {
		at := start.Add(span * time.Duration(i) / time.Duration(n)).Add(time.Duration(r.n(50)) * time.Second)
		p := w.people[r.n(len(w.people))]
		ip := fmt.Sprintf("203.0.113.%d", 10+r.n(200))
		var kind, text string
		switch x := r.n(100); {
		case x < 30:
			kind, text = "device", fmt.Sprintf("device %s of %s online, engine 0.7.3", w.devices[r.n(len(w.devices))].Name, p.Email)
		case x < 50:
			kind, text = "device", fmt.Sprintf("device %s of %s offline", w.devices[r.n(len(w.devices))].Name, p.Email)
		case x < 64:
			kind, text = "login", fmt.Sprintf("web login: code asked for %s from %s: sent, with a login link", p.Email, ip)
		case x < 72:
			kind, text = "login", fmt.Sprintf("web login: %s logged in from %s", p.Email, ip)
		case x < 78:
			kind, text = "login", fmt.Sprintf("web login: code asked for \"…@example.com\" #%012x from %s, account new: sent, with a login link", r.next()&0xffffffffffff, ip)
		case x < 92:
			kind, text = "op", fmt.Sprintf("op %s from device of %s", []string{"lan.invite", "service.set", "share.create", "lan.addDevice", "relay.setWeight"}[r.n(5)], p.Email)
		case x < 97:
			kind, text = "other", fmt.Sprintf("relay 香港 refused a device from %s: kicked", ip)
		default:
			kind, text = "error", fmt.Sprintf("stream: write to %s: broken pipe", ip)
		}
		w.addLine(at, kind, text)
	}
	w.addLine(now.Add(-2*time.Hour-10*time.Minute), "alert", "alert backup-stale: 超过 26 小时没有新的备份")
	w.addLine(now.Add(-12*time.Minute), "alert", "alert relay-full/r_hk: 公共中转站「香港」满了")
	slices.SortStableFunc(w.log, func(a, b OpsLine) int { return a.At.Compare(b.At) })
	w.pos = map[int64]int64{}
	for i := range w.log {
		w.log[i].Seq = w.seqAt(w.log[i].At, w.log[i].Text)
	}
}

// seqAt is the next line's seq on at's day: where it starts in that day's
// file, about.
func (w *opsWorld) seqAt(at time.Time, text string) int64 {
	day := at.UTC().Unix() / 86400
	p := w.pos[day]
	w.pos[day] = p + int64(len(text)) + 44 // the line's time and the newline
	return day*opsLogDay + p
}

func (w *opsWorld) addLine(at time.Time, kind, text string) {
	if w.pos == nil {
		w.pos = map[int64]int64{}
	}
	w.log = append(w.log, OpsLine{Seq: w.seqAt(at, text), At: at, Kind: kind, Text: text})
}

func (w *opsWorld) person(id proto.PersonID) *opsPerson {
	for _, p := range w.people {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func (w *opsWorld) device(id proto.DeviceID) *OpsDevice {
	for _, d := range w.devices {
		if d.ID == id {
			return d
		}
	}
	return nil
}

func (w *opsWorld) record(now time.Time, by *opsPerson, act, text string) {
	text = spaced(text)
	w.records = append([]OpsRecord{{now, OpsWho{by.ID, by.Name, by.Initial}, text}}, w.records...)
	w.addLine(now, "op", "operator "+by.Email+": "+text+" ["+act+"]")
}

// ---- HTTP -------------------------------------------------------------------------

// operatorOnly lets the deployment's operators through; anyone else gets 403.
func (m *mock) operatorOnly(h func(http.ResponseWriter, *http.Request, *opsPerson)) http.HandlerFunc {
	return m.authed(func(w http.ResponseWriter, r *http.Request, me proto.PersonID) {
		m.mu.Lock()
		p := m.console.person(me)
		m.mu.Unlock()
		if p == nil || !p.Operator {
			writeErr(w, fail(403, proto.ErrForbidden, "只有部署管理员能看"))
			return
		}
		h(w, r, p)
	})
}

func (m *mock) opsRoutes(mux *http.ServeMux, post func(http.HandlerFunc) http.Handler) {
	mux.HandleFunc("GET /api/v1/operator/overview", m.operatorOnly(m.opsOverview))
	mux.HandleFunc("GET /api/v1/operator/backups", m.operatorOnly(func(w http.ResponseWriter, r *http.Request, _ *opsPerson) {
		m.mu.Lock()
		defer m.mu.Unlock()
		writeJSON(w, m.console.backups)
	}))
	mux.HandleFunc("GET /api/v1/operator/people", m.operatorOnly(m.opsPeople))
	mux.HandleFunc("GET /api/v1/operator/people/{id}", m.operatorOnly(m.opsPerson))
	mux.HandleFunc("GET /api/v1/operator/devices", m.operatorOnly(m.opsDevices))
	mux.HandleFunc("GET /api/v1/operator/logins", m.operatorOnly(func(w http.ResponseWriter, r *http.Request, _ *opsPerson) {
		m.mu.Lock()
		defer m.mu.Unlock()
		tests := []OpsWho{}
		for _, p := range m.console.people {
			if p.Test {
				tests = append(tests, OpsWho{p.ID, p.Email, p.Initial})
			}
		}
		writeJSON(w, map[string]any{"logins": m.console.logins, "accounts": tests})
	}))
	mux.HandleFunc("GET /api/v1/operator/records", m.operatorOnly(m.opsRecords))
	mux.HandleFunc("GET /api/v1/operator/log", m.operatorOnly(m.opsLog))
	mux.Handle("POST /api/v1/operator/act", post(m.operatorOnly(m.opsAct)))
	// The demo's stand-in for what happens on the server meanwhile: an
	// alert fires (a line in the log, and on the first page).
	mux.Handle("POST /api/v1/_mock/ops/alert", post(m.operatorOnly(func(w http.ResponseWriter, r *http.Request, _ *opsPerson) {
		m.mu.Lock()
		defer m.mu.Unlock()
		now := m.now()
		m.console.alerts = append([]OpsAlert{{Key: "disk-low", Level: "warn", Title: "数据目录所在的磁盘快满了", Text: "还剩 9%", At: now}}, m.console.alerts...)
		m.console.addLine(now, "alert", "alert disk-low: 数据目录所在的磁盘快满了（还剩 9%）")
		writeJSON(w, struct{}{})
	})))
}

func (m *mock) opsOverview(w http.ResponseWriter, r *http.Request, _ *opsPerson) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o := m.console
	v := OpsOverview{Alerts: o.alerts, Recent: o.recent}
	// The version is the commit the server was built from, as the server's.
	v.Server.Started, v.Server.Version = o.started, "e4f12ee"
	v.Server.Memory, v.Server.MemoryLimit, v.Server.DiskFree = 82<<20, 256<<20, 0.64
	if len(o.backups) > 0 {
		v.Server.Backup, v.Server.Backups, v.Server.BackupAt = o.backups[0].Written, len(o.backups), o.backups[0].Written.In(utc8).Format("15:04")
	}
	v.Relays.Count, v.Relays.Online, v.Relays.Names, v.Relays.Full, v.Relays.Busy = 1, 1, []string{"香港"}, []string{"香港"}, []string{}
	week := m.now().Add(-7 * 24 * time.Hour)
	for _, p := range o.people {
		v.Counts.People++
		if p.Created.After(week) {
			v.Counts.NewThisWeek++
		}
		if p.browsers > 0 {
			v.Counts.Browsers++
		}
	}
	for _, d := range o.devices {
		v.Counts.Devices++
		if d.State == "online" {
			v.Counts.Online++
		}
	}
	v.Counts.LANs, v.Counts.LANPeople = 42, 3.1
	writeJSON(w, v)
}

// page reads offset and limit as the server does: 30 by default, 100 at most.
func page(r *http.Request) (offset, limit int) {
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 30
	}
	return max(offset, 0), min(limit, 100)
}

func (m *mock) opsPeople(w http.ResponseWriter, r *http.Request, _ *opsPerson) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	filter := r.URL.Query().Get("filter")
	if filter != "" && filter != "new" && filter != "online" && filter != "operators" && filter != "test" {
		writeErr(w, fail(400, proto.ErrInvalid, "没有这种筛法"))
		return
	}
	offset, limit := page(r)
	m.mu.Lock()
	defer m.mu.Unlock()
	week := m.now().Add(-7 * 24 * time.Hour)
	var out []OpsPerson
	for _, p := range m.console.people {
		if q != "" && !strings.Contains(strings.ToLower(p.Name+" "+p.Email), q) {
			continue
		}
		switch filter {
		case "new":
			if !p.Created.After(week) {
				continue
			}
		case "online":
			if !p.Online {
				continue
			}
		case "operators":
			if !p.Operator {
				continue
			}
		case "test":
			if !p.Test {
				continue
			}
		}
		out = append(out, p.OpsPerson)
	}
	// As the server: the early ones first, then by email; the newest first
	// for 最近新来的.
	slices.SortStableFunc(out, func(a, b OpsPerson) int {
		if filter == "new" {
			return b.Created.Compare(a.Created)
		}
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return strings.Compare(a.Email, b.Email)
	})
	total := len(out)
	out = out[min(offset, total):min(offset+limit, total)]
	if out == nil {
		out = []OpsPerson{}
	}
	writeJSON(w, map[string]any{"total": total, "people": out})
}

func (m *mock) opsDetail(p *opsPerson) OpsPersonDetail {
	d := OpsPersonDetail{OpsPerson: p.OpsPerson, DeviceList: []OpsDevice{}, Owned: p.owned, Relays: p.relays}
	for _, x := range m.console.devices {
		if x.Owner.ID == p.ID {
			d.DeviceList = append(d.DeviceList, *x)
		}
	}
	slices.SortStableFunc(d.DeviceList, func(a, b OpsDevice) int { return strings.Compare(a.Name, b.Name) })
	// limit is what holds (0: none), own whether it is theirs.
	d.Seats.Used, d.Seats.Limit, d.Seats.Own, d.Seats.Default = p.seatsUsed, opsDefaultSeats, p.seatsLimit != 0, opsDefaultSeats
	switch {
	case p.seatsLimit > 0:
		d.Seats.Limit = p.seatsLimit
	case p.seatsLimit < 0:
		d.Seats.Limit = 0
	}
	d.Browsers.Count, d.Browsers.Newest = p.browsers, p.browserNewest
	d.LANs.Count, d.LANs.Owned = p.lans, p.lansOwned
	if d.Owned == nil {
		d.Owned = []OpsOwnedLAN{}
	}
	return d
}

func (m *mock) opsPerson(w http.ResponseWriter, r *http.Request, _ *opsPerson) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.console.person(proto.PersonID(r.PathValue("id")))
	if p == nil {
		writeErr(w, fail(404, proto.ErrNotFound, "没有这个人了"))
		return
	}
	writeJSON(w, m.opsDetail(p))
}

func (m *mock) opsDevices(w http.ResponseWriter, r *http.Request, _ *opsPerson) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	state := r.URL.Query().Get("state")
	if state != "" && state != "online" && state != "offline" && state != "loggedOut" && state != "expired" {
		writeErr(w, fail(400, proto.ErrInvalid, "没有这种状态"))
		return
	}
	offset, limit := page(r)
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []OpsDevice
	online := 0
	for _, d := range m.console.devices {
		if d.State == "online" {
			online++
		}
		owner := m.console.person(d.Owner.ID)
		if q != "" && !strings.Contains(strings.ToLower(d.Name+" "+d.Owner.Name+" "+owner.Email), q) {
			continue
		}
		if state != "" && d.State != state {
			continue
		}
		out = append(out, *d)
	}
	// As the server: by the owner's email, then by name.
	email := func(d OpsDevice) string {
		if p := m.console.person(d.Owner.ID); p != nil {
			return p.Email
		}
		return ""
	}
	slices.SortStableFunc(out, func(a, b OpsDevice) int {
		if c := strings.Compare(email(a), email(b)); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	total := len(out)
	out = out[min(offset, total):min(offset+limit, total)]
	if out == nil {
		out = []OpsDevice{}
	}
	writeJSON(w, map[string]any{"total": total, "all": len(m.console.devices), "online": online, "devices": out})
}

// opsRecords is the operators' record, newest first: 200 by default, 1000
// at most, before an RFC 3339 time to page back.
func (m *mock) opsRecords(w http.ResponseWriter, r *http.Request, _ *opsPerson) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	limit = min(limit, 1000)
	var before time.Time
	if b := r.URL.Query().Get("before"); b != "" {
		t, err := time.Parse(time.RFC3339Nano, b)
		if err != nil {
			writeErr(w, fail(400, proto.ErrInvalid, "before 要 RFC 3339 的时间"))
			return
		}
		before = t
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []OpsRecord{}
	for _, rec := range m.console.records {
		if !before.IsZero() && !rec.At.Before(before) {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, rec)
	}
	writeJSON(w, out)
}

// opsLog answers as the server's: the kind's newest opsLogMost lines are
// all there is; with neither after nor before, the last page; with before,
// the page before it (oldest first) and whether there are more; with
// after, all after it. last is the newest line's seq of all, filtered
// or not.
func (m *mock) opsLog(w http.ResponseWriter, r *http.Request, _ *opsPerson) {
	kind := r.URL.Query().Get("kind")
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	m.mu.Lock()
	defer m.mu.Unlock()
	var all []OpsLine
	var last int64
	for _, l := range m.console.log {
		last = max(last, l.Seq)
		if kind == "" || l.Kind == kind {
			all = append(all, l)
		}
	}
	all = all[max(0, len(all)-opsLogMost):]
	var out []OpsLine
	more := false
	switch {
	case after > 0:
		for _, l := range all {
			if l.Seq > after {
				out = append(out, l)
			}
		}
	case before > 0:
		i := 0
		for i < len(all) && all[i].Seq < before {
			i++
		}
		from := max(0, i-opsLogPage)
		out, more = all[from:i], from > 0
	default:
		from := max(0, len(all)-opsLogPage)
		out, more = all[from:], from > 0
	}
	if out == nil {
		out = []OpsLine{}
	}
	writeJSON(w, map[string]any{"lines": out, "more": more, "last": last})
}

// OpsAct is what an operator does: an act and its arguments, all strings.
type OpsAct struct {
	Act  string            `json:"act"`
	Args map[string]string `json:"args"`
}

func (m *mock) opsAct(w http.ResponseWriter, r *http.Request, by *opsPerson) {
	var req OpsAct
	if !decode(w, r, &req) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	o := m.console
	p := o.person(proto.PersonID(req.Args["person"]))
	d := o.device(proto.DeviceID(req.Args["device"]))
	noPerson := func() bool {
		if p == nil {
			writeErr(w, fail(404, proto.ErrNotFound, "没有这个人了"))
		}
		return p == nil
	}
	noDevice := func() bool {
		if d == nil {
			writeErr(w, fail(404, proto.ErrNotFound, "没有这台设备了"))
		}
		return d == nil
	}
	switch req.Act {
	case "seats.set":
		if noPerson() {
			return
		}
		// 1 to 10000; 0 the deployment's again; -1 none. The deployment's
		// own number is its default too.
		n, err := strconv.Atoi(req.Args["limit"])
		if err != nil || n < -1 || n > 10000 {
			writeErr(w, fail(400, proto.ErrInvalid, "上限是 1 到 10000 个，0 恢复部署的默认，-1 不限"))
			return
		}
		if n == opsDefaultSeats {
			n = 0
		}
		p.seatsLimit = n
		switch {
		case n == 0:
			o.record(now, by, req.Act, fmt.Sprintf("把%s的位置上限恢复成了部署的默认（%d 个）", p.Name, opsDefaultSeats))
		case n < 0:
			o.record(now, by, req.Act, fmt.Sprintf("把%s的位置上限改成了不限", p.Name))
		default:
			o.record(now, by, req.Act, fmt.Sprintf("把%s的位置上限调成了 %d", p.Name, n))
		}
		writeJSON(w, m.opsDetail(p))
	case "sessions.revoke":
		if noPerson() {
			return
		}
		n := p.browsers
		p.browsers, p.browserNewest = 0, time.Time{}
		if n > 0 {
			o.record(now, by, req.Act, fmt.Sprintf("让%s在 %d 个浏览器里都退出了", p.Name, n))
		}
		writeJSON(w, m.opsDetail(p))
	case "operator.set":
		if noPerson() {
			return
		}
		on := req.Args["on"] == "true"
		if !on && p.ID == by.ID {
			writeErr(w, fail(409, proto.ErrConflict, "不能不让自己管：请另一位部署管理员来做"))
			return
		}
		p.Operator = on
		if on {
			o.record(now, by, req.Act, "把"+p.Name+"设成了部署管理员")
		} else {
			o.record(now, by, req.Act, "不让"+p.Name+"管这个部署了")
		}
		writeJSON(w, m.opsDetail(p))
	case "device.relogin":
		if noDevice() {
			return
		}
		switch d.State {
		case "loggedOut":
			writeErr(w, fail(409, proto.ErrConflict, "这台设备已经登出了"))
			return
		case "expired":
			// Already: nothing changes, nothing is recorded.
			writeJSON(w, d)
			return
		}
		d.State, d.Expires, d.LastSeen = "expired", time.Time{}, now
		o.count()
		o.record(now, by, req.Act, fmt.Sprintf("让%s的「%s」重新登录", d.Owner.Name, d.Name))
		writeJSON(w, d)
	case "device.delete":
		if noDevice() {
			return
		}
		o.devices = slices.DeleteFunc(o.devices, func(x *OpsDevice) bool { return x == d })
		o.count()
		o.record(now, by, req.Act, fmt.Sprintf("删了%s的「%s」", d.Owner.Name, d.Name))
		writeJSON(w, struct{}{})
	case "account.delete":
		if noPerson() {
			return
		}
		if !strings.EqualFold(strings.TrimSpace(req.Args["confirm"]), p.Email) {
			writeErr(w, fail(400, proto.ErrInvalid, "邮箱对不上，再看一眼"))
			return
		}
		if p.ID == by.ID {
			writeErr(w, fail(409, proto.ErrConflict, "自己的账号在账号卡片里注销"))
			return
		}
		if p.Test {
			writeErr(w, fail(403, proto.ErrForbidden, "种子文件里写着的账号注销不了：服务器每次启动都会把它建回来"))
			return
		}
		// Each LAN of theirs with others in it says what becomes of it.
		var missing []string
		for _, l := range p.owned {
			switch req.Args["lan:"+l.ID] {
			case "transfer", "delete":
			case "":
				missing = append(missing, "「"+l.Name+"」")
			default:
				writeErr(w, fail(400, proto.ErrInvalid, "「"+l.Name+"」要么转给最早加入的人，要么整个删掉"))
				return
			}
		}
		if len(missing) > 0 {
			writeErr(w, fail(409, proto.ErrConflict, "还没说"+strings.Join(missing, "、")+"怎么办"))
			return
		}
		o.devices = slices.DeleteFunc(o.devices, func(x *OpsDevice) bool { return x.Owner.ID == p.ID })
		o.people = slices.DeleteFunc(o.people, func(x *opsPerson) bool { return x == p })
		o.count()
		text := fmt.Sprintf("替%s（%s）注销了账号", p.Name, p.Email)
		for _, l := range p.owned {
			if req.Args["lan:"+l.ID] == "delete" {
				text += fmt.Sprintf("，删了「%s」", l.Name)
			} else {
				text += fmt.Sprintf("，「%s」转给了%s", l.Name, l.First)
			}
		}
		o.record(now, by, req.Act, text)
		// The server mails them "应你的要求，你的 Deneb 账号注销了"; the
		// mock mails no one, it says so in its log.
		o.addLine(now, "other", "mail (development, not sent): account deleted at their request, to "+p.Email)
		writeJSON(w, struct{}{})
	case "login.approve":
		i := slices.IndexFunc(o.logins, func(l OpsLogin) bool { return l.ID == req.Args["login"] })
		if i < 0 {
			writeErr(w, fail(404, proto.ErrNotFound, "这台设备已经不等了"))
			return
		}
		var to *opsPerson
		for _, x := range o.people {
			if x.Email == req.Args["email"] {
				to = x
			}
		}
		if to == nil || !to.Test {
			writeErr(w, fail(403, proto.ErrForbidden, "只能批准进测试账号"))
			return
		}
		l := o.logins[i]
		o.logins = slices.Delete(o.logins, i, i+1)
		o.devices = append(o.devices, &OpsDevice{ID: proto.DeviceID("od_new_" + l.ID), Name: l.Hostname, Kind: proto.KindLaptop, OS: l.OS, Engine: "e4f12ee", State: "online", LastSeen: now,
			Expires: now.Add(180 * 24 * time.Hour), Address: "10.53.9.9", Owner: OpsWho{to.ID, to.Name, to.Initial}})
		o.count()
		o.record(now, by, req.Act, fmt.Sprintf("批准了「%s」登进 %s", l.Hostname, to.Email))
		writeJSON(w, struct{}{})
	default:
		writeErr(w, fail(400, proto.ErrInvalid, "没有这种操作"))
	}
}
