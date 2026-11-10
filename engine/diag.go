package engine

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/pprof"
	"strings"
	"time"

	"github.com/kittors/deneb/engine/tun"
)

// Diagnostics (M6 i7) is one zip a person can send with a bug report:
//
//   - the engine: version, platform, mode, options, the local API's state
//     (devices, LANs, problems), each peer's path, the relays, the
//     filter's counters, its goroutines;
//   - the last network map, and in kernel mode what the engine set up on
//     the system (tun.Plan);
//   - the end of the log.
//
// Never in it: keys of any kind (the state file, which holds the private
// ones, stays out; public keys are cut to their last 4 hex digits, enough
// to tell the same key across files), the link of a login in progress,
// and traffic: the engine logs addresses and ports, never what is sent.
func (e *Engine) Diagnostics(ctx context.Context, w io.Writer) error {
	e.mu.Lock()
	login := e.loginURL
	e.mu.Unlock()
	scrub := func(b []byte) []byte {
		if login != "" {
			b = bytes.ReplaceAll(b, []byte(login), []byte("<login link removed>"))
		}
		return redactText(b)
	}
	z := zip.NewWriter(w)
	add := func(name string, b []byte) error {
		f, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return err
		}
		_, err = f.Write(scrub(b))
		return err
	}
	addJSON := func(name string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return add(name, []byte(fmt.Sprintf("{\"error\": %q}\n", err.Error())))
		}
		var tree any
		if err := json.Unmarshal(b, &tree); err != nil {
			return err
		}
		out, err := json.MarshalIndent(redactJSON(tree), "", "  ")
		if err != nil {
			return err
		}
		return add(name, append(out, '\n'))
	}

	if err := add("README.txt", []byte(diagReadme)); err != nil {
		return err
	}
	if err := addJSON("engine.json", e.diagEngine()); err != nil {
		return err
	}
	st := e.State()
	st.UserView = nil // the person's account: people, invites (their links let anyone in)
	if err := addJSON("state.json", st); err != nil {
		return err
	}
	if err := addJSON("paths.json", e.diagPaths()); err != nil {
		return err
	}
	if err := addJSON("relays.json", e.ms.Relays()); err != nil {
		return err
	}
	if err := addJSON("filter.json", map[string]any{"drops": e.f.Drops(), "flows": e.f.Flows()}); err != nil {
		return err
	}
	if b, err := os.ReadFile(e.netmapPath()); err == nil {
		var tree any
		if json.Unmarshal(b, &tree) == nil {
			if err := addJSON("netmap.json", tree); err != nil {
				return err
			}
		}
	}
	if err := add("system.txt", e.diagSystem()); err != nil {
		return err
	}
	if err := add("log.txt", e.diagLog()); err != nil {
		return err
	}
	var g bytes.Buffer
	pprof.Lookup("goroutine").WriteTo(&g, 1)
	if err := add("goroutines.txt", g.Bytes()); err != nil {
		return err
	}
	return z.Close()
}

const diagReadme = `Deneb 诊断包

里面有：
  engine.json      引擎版本、平台、模式、启动参数
  state.json       窗口看到的那份状态：设备、局域网、问题
  paths.json       到每台设备的路径：直连还是中转、往返时间、丢包
  relays.json      中转站连接
  filter.json      过滤器丢掉的包，按原因计数
  netmap.json      最近一份网络图
  system.txt       内核模式在系统上设的网卡、路由、DNS
  log.txt          引擎日志的最后一段
  goroutines.txt   引擎里正在跑的协程，查卡住用

不含：任何密钥（私钥所在的状态文件不放进来，公钥只留最后 4 位）、
登录和邀请链接、账号里的人和邀请、传输的内容（日志里只有地址和端口）。
`

// diagEngine is engine.json: what this engine is and how it was started.
func (e *Engine) diagEngine() map[string]any {
	o := e.opts
	logPath := o.LogPath
	if logPath == "" {
		logPath = filepath.Join(o.StateDir, "denebd.log")
	}
	return map[string]any{
		"version": o.Version, "testBuild": TestBuild,
		"os": runtime.GOOS, "arch": runtime.GOARCH, "go": runtime.Version(),
		"time": time.Now().Format(time.RFC3339), "mode": o.Mode,
		"stateDir": o.StateDir, "logPath": logPath,
		"control": o.ControlURL, "controlPin": o.ControlPin,
		"options": map[string]any{
			"port": o.Port, "bindInterface": o.BindInterface, "socks": o.SOCKS,
			"heartbeat": o.Heartbeat.String(), "trust": o.Trust.String(), "idle": o.Idle.String(),
			"homeRelay": o.HomeRelay, "relayIdle": o.RelayIdle.String(), "pathByLoss": o.PathByLoss,
			"dupSmall": o.DupSmall, "userView": o.UserView, "hostname": o.Hostname, "kind": o.Kind,
			"staticEndpoints": o.StaticEndpoints,
		},
	}
}

// diagPaths is paths.json: each peer's path, by name.
func (e *Engine) diagPaths() map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]any{}
	if e.nm == nil {
		return out
	}
	for _, p := range e.nm.Peers {
		name := p.FQDN
		if name == "" {
			name = string(p.ID)
		}
		st, ok := e.ms.Status(p.NodeKey)
		if !ok {
			out[name] = map[string]any{"device": p.ID, "kind": "inactive"}
			continue
		}
		out[name] = map[string]any{
			"device": p.ID, "kind": st.Kind, "endpoint": st.Endpoint.String(), "relay": st.Relay,
			"rttMs": st.RTT.Milliseconds(), "loss": st.Loss, "since": st.Since, "candidates": st.Candidates,
			"blocked": st.Blocked, "idle": st.Idle, "held": st.Held, "reason": st.Reason, "prev": st.Prev,
			"dup": st.Dup, "localRefused": st.LocalRefused, "backedOff": st.BackedOff,
			"txDirect": st.TxDirect, "txRelay": st.TxRelay, "rxDirect": st.RxDirect, "rxRelay": st.RxRelay,
			"pingsTx": st.PingsTx, "pingsRelay": st.PingsRelay, "pingsRx": st.PingsRx, "pongsRx": st.PongsRx,
		}
	}
	return out
}

// diagSystem is system.txt: in kernel mode, the interface, routes and DNS
// setup as the platform describes them (what preflight prints).
func (e *Engine) diagSystem() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "mode: %s\n", e.opts.Mode)
	e.mu.Lock()
	kern := e.kern
	e.mu.Unlock()
	if kern != nil {
		fmt.Fprintf(&b, "interface: %s\n", kern.Name)
	}
	cfg, peers, err := KernelPlan(e.opts.StateDir, 0)
	if err != nil {
		fmt.Fprintf(&b, "plan: %v\n", err)
		return b.Bytes()
	}
	lines, err := tun.Plan(cfg, peers)
	if err != nil {
		fmt.Fprintf(&b, "plan: %v\n", err)
	}
	for _, l := range lines {
		fmt.Fprintln(&b, l)
	}
	return b.Bytes()
}

// diagLogMax bounds log.txt: the end of the log, where the trouble is.
const diagLogMax = 4 << 20

// diagLog is log.txt: the last diagLogMax bytes of the log, from a line
// start.
func (e *Engine) diagLog() []byte {
	path := e.opts.LogPath
	if path == "" {
		path = filepath.Join(e.opts.StateDir, "denebd.log")
	}
	f, err := os.Open(path)
	if err != nil {
		return []byte(fmt.Sprintf("no log: %v\n", err))
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return []byte(fmt.Sprintf("no log: %v\n", err))
	}
	off := max(fi.Size()-diagLogMax, 0)
	b, err := io.ReadAll(io.NewSectionReader(f, off, fi.Size()-off))
	if err != nil {
		return []byte(fmt.Sprintf("log: %v\n", err))
	}
	if off > 0 {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return b
}

var (
	// Keys as the engine writes them (proto/keys.go): the private one is
	// removed, a public one keeps its last 4 hex digits.
	reKeyText = regexp.MustCompile(`\b(priv)?(machine|node|disco)key:[0-9a-f]{60}([0-9a-f]{4})\b`)
	// Other key-like strings: 32 bytes in hex or base64 (WireGuard's).
	reHex32     = regexp.MustCompile(`\b[0-9a-fA-F]{60}([0-9a-fA-F]{4})\b`)
	reBase64Key = regexp.MustCompile(`(?:^|[^A-Za-z0-9+/])[A-Za-z0-9+/]{39}([A-Za-z0-9+/]{4})=`)
	// Links that let someone in: a login's (/login?c=…) and an invite's
	// (/i/…).
	reLink = regexp.MustCompile(`(/login\?c=|/i/)[A-Za-z0-9_%=-]+`)
)

// redactText cuts keys out of text: logs, and JSON after redactJSON.
func redactText(b []byte) []byte {
	b = reKeyText.ReplaceAllFunc(b, func(m []byte) []byte {
		sub := reKeyText.FindSubmatch(m)
		if len(sub[1]) > 0 {
			return []byte("priv" + string(sub[2]) + "key:<removed>")
		}
		return []byte(string(sub[2]) + "key:…" + string(sub[3]))
	})
	b = reHex32.ReplaceAll(b, []byte("…$1"))
	b = reLink.ReplaceAll(b, []byte("${1}<removed>"))
	b = reBase64Key.ReplaceAllFunc(b, func(m []byte) []byte {
		lead := ""
		if len(m) == 45 { // the character before the key
			lead, m = string(m[0]), m[1:]
		}
		return []byte(lead + "…" + string(m[39:43]) + "=")
	})
	return b
}

// redactJSON removes values of fields that are secret by name (login
// links, tokens, private keys, passwords) from a decoded JSON tree.
func redactJSON(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if secretField(k) && x != nil {
				t[k] = "<removed>"
				continue
			}
			t[k] = redactJSON(x)
		}
	case []any:
		for i := range t {
			t[i] = redactJSON(t[i])
		}
	}
	return v
}

func secretField(name string) bool {
	n := strings.ToLower(name)
	for _, s := range []string{"private", "secret", "token", "password", "cookie", "loginurl", "authkey"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}
