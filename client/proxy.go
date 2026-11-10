package client

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/client/model"
)

// Another program's tunnel takes Deneb's connections (DESIGN §6.8, §6.14):
// the engine says so, and what to add in that program, but not which
// program it is. The store looks among the programs running: a name it
// knows gets that program's pill and words, in its own terms; any other,
// the words for a proxy of any kind.

// proxyApp is a proxy program people run: how to know it among the
// running programs, and where in it the rule goes.
type proxyApp struct {
	name  string   // as people call it: "Surge"
	procs []string // lowercase parts of its processes' names
	// where says where in it the rule goes, before the rule; cidrOnly: it
	// takes the addresses alone, not a rule line.
	where    string
	cidrOnly bool
}

// proxyApps are the programs the store knows, in the order it looks.
var proxyApps = []proxyApp{
	{name: "Clash", procs: []string{"clash", "mihomo"}},
	{name: "Surge", procs: []string{"surge"}, where: "在 Surge 的「规则」里加上这一条，局域网里的连接就不经过它："},
	{name: "v2rayN", procs: []string{"v2rayn"}, where: "在 v2rayN 的「路由设置」里，把这一段加进「直连」：", cidrOnly: true},
}

// proxyNotice is the notice of a proxy's tunnel taking the connections:
// which program, if the running ones tell, and the rule as it takes it.
func proxyNotice(code, rule, app string) model.Notice {
	for _, pa := range proxyApps {
		if pa.name != app {
			continue
		}
		if pa.where == "" {
			// Clash's, as M2 has it: the rule, under the message.
			return model.Notice{Code: code, Short: "Clash 可能挡住连接",
				Message: "Clash 一类的软件正在接管网络，局域网里的设备可能连不上", Rule: rule}
		}
		r := rule
		if pa.cidrOnly {
			r = ruleCIDR(rule)
		}
		return model.Notice{Code: code, Short: pa.name + " 可能挡住连接",
			Message: pa.name + " 正在接管网络，局域网里的设备可能连不上", Detail: pa.where, Rule: r}
	}
	return model.Notice{Code: code, Short: "代理软件可能挡住连接",
		Message: "有别的代理软件正在接管网络，局域网里的设备可能连不上",
		Detail:  "在它的设置里，让这一段地址直连，不经过它：", Rule: ruleCIDR(rule)}
}

// ruleCIDR is the addresses of a rule line, "IP-CIDR,10.48.0.0/16,DIRECT":
// 10.48.0.0/16; the rule as it is when it is not one.
func ruleCIDR(rule string) string {
	f := strings.Split(rule, ",")
	if len(f) >= 2 && strings.Contains(f[1], "/") {
		return strings.TrimSpace(f[1])
	}
	return rule
}

// runningProxy is which of proxyApps runs on this computer, as last found:
// "" for none it knows. It asks the system for the running programs in
// the background, at most every half a minute, and only while the engine
// reports a proxy.
type runningProxy struct {
	mu      sync.Mutex
	at      time.Time
	found   string
	looked  bool // a look finished once
	looking bool
	list    func(ctx context.Context) ([]string, error) // the running programs' names; tests replace it
}

// known returns the program last found, and whether a look finished
// once: until then the store holds the notice back rather than name no
// program and then one. When what it found is old, it looks again in the
// background and calls then if it finds another, or for the first time.
func (r *runningProxy) known(now time.Time, then func()) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.looking && (r.at.IsZero() || now.Sub(r.at) >= 30*time.Second) {
		r.looking, r.at = true, now
		go func() {
			found := r.look()
			r.mu.Lock()
			r.looking = false
			changed := found != r.found || !r.looked
			r.found, r.looked = found, true
			r.mu.Unlock()
			if changed && then != nil {
				then()
			}
		}()
	}
	return r.found, r.looked
}

// look lists the running programs and says which of proxyApps is among
// them.
func (r *runningProxy) look() string {
	list := r.list
	if list == nil {
		list = processNames
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	names, err := list(ctx)
	if err != nil {
		return ""
	}
	for _, pa := range proxyApps {
		for _, n := range names {
			n = strings.ToLower(n)
			for _, part := range pa.procs {
				if strings.Contains(n, part) {
					return pa.name
				}
			}
		}
	}
	return ""
}

// processNames are the running programs' names, as the system lists them.
func processNames(ctx context.Context) ([]string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "tasklist", "/fo", "csv", "/nh")
	} else {
		cmd = exec.CommandContext(ctx, "ps", "-axo", "comm=")
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if runtime.GOOS == "windows" {
			// "v2rayN.exe","1234",...: the first field.
			l, _, _ = strings.Cut(strings.TrimPrefix(l, `"`), `"`)
		} else if i := strings.LastIndex(l, "/"); i >= 0 {
			l = l[i+1:]
		}
		if l != "" {
			names = append(names, l)
		}
	}
	return names, nil
}

// SetRunningPrograms replaces how the store lists the programs running on
// this computer, among which it finds a proxy's (tests).
func (l *Live) SetRunningPrograms(list func(ctx context.Context) ([]string, error)) {
	l.proxy.mu.Lock()
	l.proxy.list = list
	l.proxy.mu.Unlock()
}
