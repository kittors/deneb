// Package cli is the `deneb` command line: login, status, lan, ping and a
// few helpers for servers and tests. It only talks to the local engine,
// through the same local API as the desktop client.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/localclient"
	"github.com/kittors/deneb/proto"
)

// Run runs one command; args do not include the program name.
func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(stdout)
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	c := &cmd{out: stdout, err: stderr, lc: localclient.New(os.Getenv("DENEB_SOCKET"))}
	var err error
	switch args[0] {
	case "login":
		err = c.login(ctx, args[1:])
	case "logout":
		err = c.lc.Logout(ctx)
		if err == nil {
			fmt.Fprintln(c.out, "已退出。")
		}
	case "status":
		err = c.status(ctx, args[1:])
	case "lan":
		err = c.lan(ctx, args[1:])
	case "ping":
		err = c.ping(ctx, args[1:])
	case "service":
		err = c.service(ctx, args[1:])
	case "refuse":
		err = c.refuse(ctx, args[1:])
	case "resolve":
		err = c.resolve(ctx, args[1:])
	case "services":
		err = c.services(ctx)
	case "open":
		err = c.open(ctx, args[1:])
	case "op":
		err = c.rawOp(ctx, args[1:])
	case "debug":
		err = c.debug(ctx, args[1:])
	case "diag":
		err = c.diag(ctx, args[1:])
	case "shutdown":
		err = c.shutdown(ctx)
	case "update":
		err = c.update(ctx, args[1:])
	default:
		usage(stderr)
		return fmt.Errorf("不认识的命令 %q", args[0])
	}
	return friendly(err)
}

func usage(w io.Writer) {
	fmt.Fprint(w, `用法：deneb <命令>

  login [--control URL] [--no-wait]   登录：打印一条链接，在任何一台设备的浏览器里打开
  logout                              退出
  status [--json]                     这台设备、局域网和连接状态
  lan [list]                          我的局域网
  lan create <名字>                   新建局域网（这台设备跟着加进去）
  lan invite <局域网>                 拿一条邀请链接
  lan join <链接>                     用邀请链接加入（这台设备跟着加进去）
  lan add <局域网> [设备]             把设备加进局域网，默认这台
  lan remove <局域网> [设备]          把设备撤出局域网，默认这台
  lan pause|resume <局域网>           只在这台设备上暂停、恢复
  lan leave <局域网>                  退出别人的局域网
  lan delete <局域网>                 删除自己建的局域网
  ping <设备> [-c 次数]               测连接：设备名、名字.主人、地址都行
  service <局域网> <种类> <端口> on|off [--device 设备]
                                      开关服务，例如 service 工作室 web tcp/80 on
  refuse <设备> <人> [--undo]         不让某人用我的设备
  resolve <名字>                      解析名字
  services                            这台设备在听的常见服务
  open <设备> <端口>                  给对方的一项服务一个本机能打开的地址
  op <操作> [JSON 参数]               直接发一个操作
  diag [-o 文件]                      导出诊断包（不含密钥和传输内容），默认存到当前目录
  shutdown                            停下用户态引擎，等它的进程退出（系统服务里的引擎由系统来停）
  update <denebd 的完整路径>          让系统服务换成这个文件里的引擎（Windows）
`)
}

// diag saves the engine's diagnostics zip.
func (c *cmd) diag(ctx context.Context, args []string) error {
	out := "deneb-diagnostics-" + time.Now().Format("20060102-150405") + ".zip"
	if len(args) == 2 && args[0] == "-o" {
		out = args[1]
	} else if len(args) != 0 {
		return errors.New("用法：deneb diag [-o 文件]")
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	if err := c.lc.Diagnostics(ctx, f); err != nil {
		f.Close()
		os.Remove(out)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "诊断包存到了 %s\n", out)
	return nil
}

// shutdown stops the user-space engine and returns once its process is
// gone: the Windows installer and uninstaller run it before they replace
// or remove denebd.exe, which the running engine holds (M6 i2). Nothing
// running is fine; a system service's engine refuses, the system stops it.
func (c *cmd) shutdown(ctx context.Context) error {
	v, err := c.lc.Version(ctx)
	if errors.Is(err, localclient.ErrNotRunning) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := c.lc.Shutdown(ctx); err != nil {
		return err
	}
	if v.PID > 0 && !waitExit(v.PID, 10*time.Second) {
		return fmt.Errorf("引擎（进程 %d）10 秒还没退出", v.PID)
	}
	fmt.Fprintln(c.out, "引擎已停下。")
	return nil
}

// update asks the system service to replace itself with an executable.
func (c *cmd) update(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("用法：deneb update <denebd 的完整路径>")
	}
	path, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}
	res, err := c.lc.UpdateEngine(ctx, path)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s %s %s\n", res.Result, res.Version, res.Message)
	if res.Result != apitype.UpdateDone && res.Result != apitype.UpdateSame {
		return fmt.Errorf("没有更新：%s", res.Result)
	}
	return nil
}

type cmd struct {
	out, err io.Writer
	lc       *localclient.Client
}

// friendly turns errors into one line a person can act on.
func friendly(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, localclient.ErrNotRunning) {
		return errors.New("Deneb 引擎没在运行。先启动它：denebd run")
	}
	if errors.Is(err, localclient.ErrNotAllowed) {
		return errors.New("这台电脑上的 Deneb 引擎在为别的用户运行，这个用户用不了它。")
	}
	if ae, ok := localclient.AsError(err); ok {
		if len(ae.Candidates) > 0 {
			var b strings.Builder
			b.WriteString(ae.Message)
			for _, c := range ae.Candidates {
				fmt.Fprintf(&b, "\n  %s  %s", c.FQDN, c.Addr)
			}
			return errors.New(b.String())
		}
		return errors.New(ae.Message)
	}
	return err
}

func (c *cmd) login(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	control := fs.String("control", "", "Deneb 服务器地址（第一次登录时用）")
	noWait := fs.Bool("no-wait", false, "只打印链接，不等登录完成")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := c.lc.State(ctx)
	if err != nil {
		return err
	}
	if st.LoggedIn {
		fmt.Fprintf(c.out, "已经登录了：%s\n", whoami(st))
		return nil
	}
	url, err := c.lc.Login(ctx, *control)
	if err != nil {
		return err
	}
	if url != "" {
		fmt.Fprintf(c.out, "在浏览器里打开这条链接登录（哪台设备上都行）：\n\n  %s\n\n", url)
	}
	if *noWait {
		return nil
	}
	fmt.Fprintln(c.out, "等你在浏览器里登录……（Ctrl-C 结束等待，登录照样有效）")
	errDone := errors.New("done")
	err = c.lc.Watch(ctx, func(ev apitype.Event) error {
		if ev.State != nil && ev.State.LoggedIn {
			fmt.Fprintf(c.out, "已登录：%s\n", whoami(ev.State))
			return errDone
		}
		return nil
	})
	if errors.Is(err, errDone) {
		return nil
	}
	return err
}

func whoami(st *apitype.State) string {
	if st.Self != nil {
		return fmt.Sprintf("%s（%s，%s）", st.Self.DisplayName, st.Self.FQDN, st.Self.OwnerName)
	}
	return string(st.SelfDeviceID)
}

var kindWords = map[apitype.PathKind]string{
	apitype.PathDirect:      "直接连上",
	apitype.PathRelay:       "经中转",
	apitype.PathConnecting:  "正在连接…",
	apitype.PathUnreachable: "连不上",
	apitype.PathOffline:     "对方离线",
	apitype.PathPaused:      "已暂停",
	apitype.PathRefused:     "用不了这台设备",
}

func (c *cmd) status(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "输出 JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := c.lc.State(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		st.UserView = nil
		enc := json.NewEncoder(c.out)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	if !st.LoggedIn {
		fmt.Fprintln(c.out, "还没登录。执行 deneb login。")
		if st.LoginURL != "" {
			fmt.Fprintf(c.out, "登录链接：%s\n", st.LoginURL)
		}
		return nil
	}
	control := "连着"
	if !st.ControlConnected {
		control = "连不上（照上一份网络图工作）"
	}
	fmt.Fprintf(c.out, "这台设备：%s\n", whoami(st))
	if st.Self != nil && len(st.Self.Addrs) > 0 {
		fmt.Fprintf(c.out, "地址：%s\n", strings.Join(st.Self.Addrs, "、"))
	}
	fmt.Fprintf(c.out, "Deneb 服务器：%s，%s\n", st.ControlURL, control)
	mode := "用户态"
	if st.Mode == apitype.ModeKernel {
		mode = "内核网卡"
	}
	fmt.Fprintf(c.out, "模式：%s；UDP %d", mode, st.Engine.ListenPort)
	if st.Engine.BindInterface != "" {
		fmt.Fprintf(c.out, "，绑在 %s", st.Engine.BindInterface)
	}
	if st.Engine.SOCKS != "" {
		fmt.Fprintf(c.out, "，SOCKS5 %s", st.Engine.SOCKS)
	}
	fmt.Fprintln(c.out)
	for _, r := range st.Relays {
		state := "连着"
		if !r.Connected {
			state = "没连上"
		}
		home := ""
		if r.Home {
			home = "（常驻）"
		}
		fmt.Fprintf(c.out, "中转站：%s%s，%s\n", r.Name, home, state)
	}
	for _, p := range st.Engine.Problems {
		fmt.Fprintf(c.out, "注意：%s\n", p.Message)
	}
	scopeName := map[apitype.ScopeID]string{}
	fmt.Fprintln(c.out)
	for _, s := range st.Scopes {
		scopeName[s.ID] = s.Name
		state := ""
		if s.Paused {
			state = "（已暂停）"
		}
		var open []string
		for _, o := range s.Open {
			open = append(open, fmt.Sprintf("%s %s/%d", o.Kind, o.Proto, o.Port))
		}
		if len(open) == 0 {
			open = []string{"没开服务"}
		}
		fmt.Fprintf(c.out, "局域网「%s」%s：开放 %s\n", s.Name, state, strings.Join(open, "、"))
	}
	if len(st.Peers) == 0 {
		fmt.Fprintln(c.out, "\n还看不到别的设备。")
		return nil
	}
	paths := map[apitype.DeviceID]apitype.PeerPath{}
	for _, p := range st.Paths {
		paths[p.DeviceID] = p
	}
	fmt.Fprintln(c.out)
	tw := tabwriter.NewWriter(c.out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "设备\t名字\t地址\t局域网\t连接")
	peers := slices.Clone(st.Peers)
	sort.Slice(peers, func(i, j int) bool { return peers[i].FQDN < peers[j].FQDN })
	for _, p := range peers {
		var lans []string
		for _, id := range p.Scopes {
			if n := scopeName[id]; n != "" {
				lans = append(lans, n)
			} else {
				lans = append(lans, string(id))
			}
		}
		addr := ""
		if len(p.Addrs) > 0 {
			addr = p.Addrs[0]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.DisplayName, p.FQDN, addr, strings.Join(lans, "、"), pathWords(paths[p.ID], st.Relays))
	}
	return tw.Flush()
}

func pathWords(p apitype.PeerPath, relays []apitype.Relay) string {
	w := kindWords[p.Kind]
	if w == "" {
		w = string(p.Kind)
	}
	switch p.Kind {
	case apitype.PathRelay:
		for _, r := range relays {
			if r.ID == p.Relay && r.Name != "" {
				w = "经" + r.Name + "中转"
			}
		}
		fallthrough
	case apitype.PathDirect:
		if p.RTTMs > 0 {
			w += fmt.Sprintf(" %dms", p.RTTMs)
		}
		if p.Loss >= 0.05 {
			w += fmt.Sprintf("，丢包 %.0f%%", p.Loss*100)
		}
	}
	return w
}

func (c *cmd) userView(ctx context.Context) (*proto.UserView, error) {
	raw, err := c.lc.UserView(ctx)
	if err != nil {
		return nil, err
	}
	var uv proto.UserView
	if err := json.Unmarshal(raw, &uv); err != nil {
		return nil, fmt.Errorf("读不懂服务器给的用户视图：%w", err)
	}
	return &uv, nil
}

// findLAN takes a LAN's name or id.
func findLAN(uv *proto.UserView, q string) (proto.LANView, error) {
	var hits []proto.LANView
	for _, l := range uv.LANs {
		if string(l.ID) == q || l.Name == q {
			hits = append(hits, l)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return proto.LANView{}, fmt.Errorf("找不到局域网「%s」。deneb lan 看看有哪些", q)
	}
	return proto.LANView{}, fmt.Errorf("有 %d 个局域网叫「%s」，请用 id", len(hits), q)
}

// findDevice takes a device's display name, DNS name, formal name or id.
func findDevice(uv *proto.UserView, q string) (proto.DeviceView, error) {
	var hits []proto.DeviceView
	for _, d := range uv.Devices {
		if string(d.ID) == q || d.DisplayName == q || d.Name == q || d.FQDN == q || strings.TrimSuffix(d.FQDN, "."+proto.Domain) == q {
			hits = append(hits, d)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return proto.DeviceView{}, fmt.Errorf("找不到设备「%s」", q)
	}
	return proto.DeviceView{}, fmt.Errorf("有 %d 台设备叫「%s」，请写全名（设备.主人）", len(hits), q)
}

func (c *cmd) selfID(ctx context.Context) (proto.DeviceID, error) {
	st, err := c.lc.State(ctx)
	if err != nil {
		return "", err
	}
	if !st.LoggedIn {
		return "", errors.New("还没登录。执行 deneb login")
	}
	return proto.DeviceID(st.SelfDeviceID), nil
}

func (c *cmd) op(ctx context.Context, op proto.OpType, args proto.OpArgs) (*apitype.OpResult, *proto.OpOutput, error) {
	res, err := c.lc.DoArgs(ctx, string(op), args)
	if err != nil {
		return nil, nil, err
	}
	var out proto.OpOutput
	if len(res.Result) > 0 {
		json.Unmarshal(res.Result, &out)
	}
	return res, &out, nil
}

func (c *cmd) lan(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "list" {
		return c.lanList(ctx)
	}
	need := func(n int, what string) error {
		if len(args) < n+1 {
			return fmt.Errorf("用法：deneb lan %s %s", args[0], what)
		}
		return nil
	}
	switch args[0] {
	case "create":
		if err := need(1, "<名字>"); err != nil {
			return err
		}
		name := strings.Join(args[1:], " ")
		_, out, err := c.op(ctx, proto.OpLANCreate, proto.OpArgs{Name: name})
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "建好了局域网「%s」（%s），这台设备已经在里面。\n", name, out.LAN)
		return nil
	case "invite":
		if err := need(1, "<局域网>"); err != nil {
			return err
		}
		uv, err := c.userView(ctx)
		if err != nil {
			return err
		}
		l, err := findLAN(uv, args[1])
		if err != nil {
			return err
		}
		_, out, err := c.op(ctx, proto.OpLANInvite, proto.OpArgs{LAN: l.ID})
		if err != nil {
			return err
		}
		if out.Invite == nil {
			return errors.New("服务器没有给出邀请链接")
		}
		fmt.Fprintf(c.out, "邀请链接（发给要邀请的人，一条链接只能进一个人）：\n\n  %s\n\n", out.Invite.URL)
		return nil
	case "join":
		if err := need(1, "<链接>"); err != nil {
			return err
		}
		link := args[1]
		if raw, err := c.lc.PreviewInvite(ctx, link); err == nil {
			var p proto.InvitePreview
			if json.Unmarshal(raw, &p) == nil && p.LANName != "" {
				fmt.Fprintf(c.out, "%s 邀请你加入「%s」。\n", p.Inviter.DisplayName, p.LANName)
				if p.AlreadyIn {
					fmt.Fprintln(c.out, "你已经在这个局域网里了。")
				}
			}
		} else {
			return err
		}
		_, out, err := c.op(ctx, proto.OpInviteAccept, proto.OpArgs{Invite: link})
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已加入（%s），这台设备已经在里面。\n", out.LAN)
		return nil
	case "add", "remove":
		if err := need(1, "<局域网> [设备]"); err != nil {
			return err
		}
		uv, err := c.userView(ctx)
		if err != nil {
			return err
		}
		l, err := findLAN(uv, args[1])
		if err != nil {
			return err
		}
		dev, err := c.selfID(ctx)
		if err != nil {
			return err
		}
		label := "这台设备"
		if len(args) > 2 {
			d, err := findDevice(uv, strings.Join(args[2:], " "))
			if err != nil {
				return err
			}
			dev, label = d.ID, d.DisplayName
		}
		op, verb := proto.OpLANAddDevice, "加进"
		if args[0] == "remove" {
			op, verb = proto.OpLANRemoveDevice, "撤出"
		}
		if _, _, err := c.op(ctx, op, proto.OpArgs{LAN: l.ID, Device: dev}); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已把%s%s「%s」。\n", label, verb, l.Name)
		return nil
	case "pause", "resume":
		if err := need(1, "<局域网>"); err != nil {
			return err
		}
		st, err := c.lc.State(ctx)
		if err != nil {
			return err
		}
		var id apitype.ScopeID
		for _, s := range st.Scopes {
			if s.Name == args[1] || string(s.ID) == args[1] || string(s.ID) == "lan/"+args[1] {
				id = s.ID
			}
		}
		if id == "" {
			return fmt.Errorf("这台设备不在「%s」里", args[1])
		}
		paused := args[0] == "pause"
		if err := c.lc.Pause(ctx, id, paused); err != nil {
			return err
		}
		if paused {
			fmt.Fprintf(c.out, "这台设备上已暂停「%s」，别的局域网不受影响。\n", args[1])
		} else {
			fmt.Fprintf(c.out, "已恢复「%s」。\n", args[1])
		}
		return nil
	case "delete":
		if err := need(1, "<局域网>"); err != nil {
			return err
		}
		uv, err := c.userView(ctx)
		if err != nil {
			return err
		}
		l, err := findLAN(uv, args[1])
		if err != nil {
			return err
		}
		if _, _, err := c.op(ctx, proto.OpLANDelete, proto.OpArgs{LAN: l.ID}); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已删除「%s」。\n", l.Name)
		return nil
	case "leave":
		if err := need(1, "<局域网>"); err != nil {
			return err
		}
		uv, err := c.userView(ctx)
		if err != nil {
			return err
		}
		l, err := findLAN(uv, args[1])
		if err != nil {
			return err
		}
		if _, _, err := c.op(ctx, proto.OpLANLeave, proto.OpArgs{LAN: l.ID}); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已退出「%s」。\n", l.Name)
		return nil
	}
	return fmt.Errorf("不认识的 lan 子命令 %q", args[0])
}

func (c *cmd) lanList(ctx context.Context) error {
	uv, err := c.userView(ctx)
	if err != nil {
		return err
	}
	st, _ := c.lc.State(ctx)
	people := map[proto.PersonID]proto.Person{}
	for _, p := range uv.People {
		people[p.ID] = p
	}
	devs := map[proto.DeviceID]proto.DeviceView{}
	for _, d := range uv.Devices {
		devs[d.ID] = d
	}
	if len(uv.LANs) == 0 {
		fmt.Fprintln(c.out, "还没有局域网。deneb lan create <名字> 新建一个。")
		return nil
	}
	for _, l := range uv.LANs {
		role := "成员"
		if l.Owner == uv.Me {
			role = "拥有者"
		}
		here := ""
		var devNames []string
		for _, d := range l.Devices {
			if st != nil && proto.DeviceID(st.SelfDeviceID) == d.Device {
				here = "，这台设备在里面"
			}
			name := string(d.Device)
			if dv, ok := devs[d.Device]; ok {
				name = dv.DisplayName
			}
			devNames = append(devNames, name)
		}
		var members []string
		for _, m := range l.Members {
			members = append(members, people[m.Person].DisplayName)
		}
		fmt.Fprintf(c.out, "「%s」（%s，你是%s%s）\n  人：%s\n  设备：%s\n", l.Name, l.ID, role, here, strings.Join(members, "、"), strings.Join(devNames, "、"))
	}
	return nil
}

func (c *cmd) ping(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ping", flag.ContinueOnError)
	n := fs.Int("c", 4, "次数")
	interval := fs.Duration("i", time.Second, "间隔")
	if err := fs.Parse(reorder(args, map[string]bool{"c": true, "i": true})); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("用法：deneb ping <设备> [-c 次数]")
	}
	to := fs.Arg(0)
	sum, err := c.lc.Ping(ctx, to, localclient.PingOptions{N: *n, Interval: *interval}, func(r apitype.PingResult) {
		if r.Err != "" {
			fmt.Fprintf(c.out, "#%d %s：没有回应（%s）\n", r.Seq, to, r.Err)
			return
		}
		path := "直接连上"
		if r.Path == apitype.PathRelay {
			path = "经中转"
		}
		via := ""
		if r.Endpoint != "" {
			via = " " + r.Endpoint
		}
		fmt.Fprintf(c.out, "#%d %s：%.1fms，%s%s\n", r.Seq, to, r.RTTMs, path, via)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s（%s）：发 %d 收 %d", to, sum.Addr, sum.Sent, sum.Received)
	if sum.Received > 0 {
		fmt.Fprintf(c.out, "，最短 %.1fms，平均 %.1fms，最长 %.1fms", sum.MinMs, sum.AvgMs, sum.MaxMs)
	}
	fmt.Fprintln(c.out)
	if sum.Received == 0 {
		return errors.New("连不上")
	}
	return nil
}

// reorder lets flags come after positional arguments (deneb ping nas -c 3).
func reorder(args []string, withValue map[string]bool) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if withValue[name] && !strings.Contains(a, "=") && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		rest = append(rest, a)
	}
	return append(flags, rest...)
}

// service <局域网> <种类> <端口...> on|off [--device 设备]
func (c *cmd) service(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	devName := fs.String("device", "", "哪台设备，默认这台")
	name := fs.String("name", "", "服务名，默认按种类")
	if err := fs.Parse(reorder(args, map[string]bool{"device": true, "name": true})); err != nil {
		return err
	}
	a := fs.Args()
	if len(a) < 4 {
		return errors.New("用法：deneb service <局域网> <screen|files|web|shell|game|other> <tcp/端口,...> on|off [--device 设备]")
	}
	on := a[len(a)-1] == "on"
	if a[len(a)-1] != "on" && a[len(a)-1] != "off" {
		return errors.New("最后一个参数要是 on 或 off")
	}
	svc := proto.Service{Kind: proto.ServiceKind(a[1]), Name: *name}
	for _, s := range strings.Split(strings.Join(a[2:len(a)-1], ","), ",") {
		if s == "" {
			continue
		}
		pr, err := proto.ParsePortRange(s)
		if err != nil {
			return err
		}
		svc.Ports = append(svc.Ports, pr)
	}
	if err := svc.Validate(); err != nil {
		return err
	}
	uv, err := c.userView(ctx)
	if err != nil {
		return err
	}
	l, err := findLAN(uv, a[0])
	if err != nil {
		return err
	}
	dev, err := c.selfID(ctx)
	if err != nil {
		return err
	}
	if *devName != "" {
		d, err := findDevice(uv, *devName)
		if err != nil {
			return err
		}
		dev = d.ID
	}
	if _, _, err := c.op(ctx, proto.OpServiceSet, proto.OpArgs{Device: dev, LAN: l.ID, Service: &svc, On: on}); err != nil {
		return err
	}
	state := "关掉"
	if on {
		state = "打开"
	}
	fmt.Fprintf(c.out, "已在「%s」%s %s（%s）。\n", l.Name, state, svc.DisplayName(), svc.Key())
	return nil
}

func (c *cmd) refuse(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("refuse", flag.ContinueOnError)
	undo := fs.Bool("undo", false, "恢复")
	if err := fs.Parse(reorder(args, nil)); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("用法：deneb refuse <设备> <人> [--undo]")
	}
	uv, err := c.userView(ctx)
	if err != nil {
		return err
	}
	d, err := findDevice(uv, fs.Arg(0))
	if err != nil {
		return err
	}
	var person proto.PersonID
	for _, p := range uv.People {
		if string(p.ID) == fs.Arg(1) || p.DisplayName == fs.Arg(1) || p.Name == fs.Arg(1) || p.Email == fs.Arg(1) {
			person = p.ID
		}
	}
	if person == "" {
		return fmt.Errorf("找不到「%s」", fs.Arg(1))
	}
	if _, _, err := c.op(ctx, proto.OpDeviceRefuse, proto.OpArgs{Device: d.ID, Person: person, Refuse: !*undo}); err != nil {
		return err
	}
	if *undo {
		fmt.Fprintf(c.out, "又能用 %s 了。\n", d.DisplayName)
	} else {
		fmt.Fprintf(c.out, "已不让 %s 用 %s。\n", fs.Arg(1), d.DisplayName)
	}
	return nil
}

func (c *cmd) resolve(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("用法：deneb resolve <名字>")
	}
	r, err := c.lc.Resolve(ctx, args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "%s → %s（%s）\n", args[0], r.Addr, r.FQDN)
	return nil
}

func (c *cmd) services(ctx context.Context) error {
	svcs, err := c.lc.Services(ctx)
	if err != nil {
		return err
	}
	if len(svcs) == 0 {
		fmt.Fprintln(c.out, "这台设备上没找到常见服务。")
	}
	for _, s := range svcs {
		fmt.Fprintf(c.out, "%s\t%s/%d\n", proto.ServiceKind(s.Kind).DefaultName(), s.Proto, s.Port)
	}
	return nil
}

func (c *cmd) open(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return errors.New("用法：deneb open <设备> <端口>")
	}
	var port uint16
	if _, err := fmt.Sscan(args[1], &port); err != nil {
		return err
	}
	r, err := c.lc.Resolve(ctx, args[0])
	if err != nil {
		return err
	}
	res, err := c.lc.Open(ctx, apitype.OpenRequest{Device: r.Device, Port: port})
	if err != nil {
		return err
	}
	fmt.Fprintln(c.out, res.Addr)
	return nil
}

func (c *cmd) rawOp(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return errors.New("用法：deneb op <操作> [JSON 参数]")
	}
	var raw json.RawMessage
	if len(args) > 1 {
		raw = json.RawMessage(strings.Join(args[1:], " "))
		if !json.Valid(raw) {
			return errors.New("参数不是合法的 JSON")
		}
	}
	res, err := c.lc.Do(ctx, apitype.OpRequest{Op: args[0], Args: raw})
	if err != nil {
		return err
	}
	return json.NewEncoder(c.out).Encode(res)
}

// debug talks to the test build's /v1/test endpoints.
func (c *cmd) debug(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("用法：deneb debug block|loss|attack|unattack|inject|filter|tick|http|probe|ping-addr|drops|paths|netchange|shutdown ...")
	}
	var out json.RawMessage
	var err error
	switch args[0] {
	case "dnsflush":
		// dnsflush off|on|now：内核模式下名字回来时刷不刷系统的 DNS 缓存
		if len(args) != 2 || (args[1] != "off" && args[1] != "on" && args[1] != "now") {
			return errors.New("用法：deneb debug dnsflush off|on|now")
		}
		err = c.lc.Raw(ctx, "POST", "/v1/test/dnsflush", map[string]any{"mode": args[1]}, &out)
	case "goroutines":
		// Every goroutine's stack, to see what is blocked during a stall.
		return c.lc.RawStream(ctx, "/v1/test/goroutines", func(line []byte) error {
			fmt.Fprintf(c.out, "%s\n", line)
			return nil
		})
	case "trace":
		// trace <端口>|off：把这个端口的每个 TCP 包记进引擎日志（带序号）
		if len(args) != 2 {
			return errors.New("用法：deneb debug trace <端口>|off")
		}
		port := 0
		if args[1] != "off" {
			if port, err = strconv.Atoi(args[1]); err != nil {
				return errors.New("端口要是数字")
			}
		}
		err = c.lc.Raw(ctx, "POST", "/v1/test/trace", map[string]any{"port": port}, &out)
	case "netchange":
		// As if the network had changed: reconnect everything (M2).
		if err := c.lc.Raw(ctx, "POST", "/v1/test/netchange", nil, nil); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "已按网络变了处理：重新连中转、重新探测对端、重连控制服务器。")
		return nil
	case "shutdown":
		// The kernel-mode engine runs as root; this lets its user stop it,
		// which removes the interface, the route and the resolver file.
		if err := c.lc.Raw(ctx, "POST", "/v1/test/shutdown", nil, nil); err != nil {
			return err
		}
		fmt.Fprintln(c.out, "已让引擎停下。")
		return nil
	case "loss":
		// loss <设备> <百分比> [out|in]：默认两个方向都丢
		if len(args) < 3 {
			return errors.New("用法：deneb debug loss <设备> <百分比，如 20> [out|in]")
		}
		pct, perr := strconv.ParseFloat(strings.TrimSuffix(args[2], "%"), 64)
		if perr != nil || pct < 0 || pct > 100 {
			return errors.New("百分比要在 0 到 100 之间")
		}
		tx, rx := pct/100, pct/100
		if len(args) > 3 {
			switch args[3] {
			case "out":
				rx = 0
			case "in":
				tx = 0
			default:
				return errors.New("方向是 out 或 in")
			}
		}
		err = c.lc.Raw(ctx, "POST", "/v1/test/loss", map[string]any{"peer": args[1], "tx": tx, "rx": rx}, &out)
	case "relaylate":
		// relaylate <设备> <百分比> <毫秒>：中转的回应按比例晚到
		if len(args) != 4 {
			return errors.New("用法：deneb debug relaylate <设备> <百分比，如 25> <毫秒，如 400>")
		}
		pct, perr := strconv.ParseFloat(strings.TrimSuffix(args[2], "%"), 64)
		ms, merr := strconv.Atoi(args[3])
		if perr != nil || merr != nil || pct < 0 || pct > 100 || ms < 0 {
			return errors.New("百分比在 0 到 100 之间，毫秒不能是负数")
		}
		err = c.lc.Raw(ctx, "POST", "/v1/test/relaylate", map[string]any{"peer": args[1], "frac": pct / 100, "ms": ms}, &out)
	case "dup":
		// dup auto|on|off：丢包的直连上小包发两份
		if len(args) != 2 {
			return errors.New("用法：deneb debug dup auto|on|off")
		}
		err = c.lc.Raw(ctx, "POST", "/v1/test/dup", map[string]any{"mode": args[1]}, &out)
	case "block", "unblock":
		if len(args) != 2 {
			return errors.New("用法：deneb debug block|unblock <设备>")
		}
		err = c.lc.Raw(ctx, "POST", "/v1/test/block", map[string]any{"peer": args[1], "on": args[0] == "block"}, &out)
	case "attack", "unattack":
		if len(args) < 2 {
			return errors.New("用法：deneb debug attack <经过的设备> <目标地址/32>")
		}
		body := map[string]any{"peer": args[1]}
		if args[0] == "attack" {
			if len(args) != 3 {
				return errors.New("用法：deneb debug attack <经过的设备> <目标地址/32>")
			}
			body["prefix"] = args[2]
		} else {
			body["clear"] = true
		}
		err = c.lc.Raw(ctx, "POST", "/v1/test/attack-route", body, &out)
	case "inject":
		if len(args) != 4 {
			return errors.New("用法：deneb debug inject <伪造的源地址> <目标地址> <端口>")
		}
		var port uint16
		fmt.Sscan(args[3], &port)
		err = c.lc.Raw(ctx, "POST", "/v1/test/inject", map[string]any{"src": args[1], "dst": args[2], "dport": port, "count": 3}, &out)
	case "filter":
		err = c.lc.Raw(ctx, "POST", "/v1/test/filter", map[string]any{"off": len(args) > 1 && args[1] == "off"}, &out)
	case "tick":
		if len(args) < 2 {
			return errors.New("用法：deneb debug tick <设备> [秒数]")
		}
		secs := "10"
		if len(args) > 2 {
			secs = args[2]
		}
		err = c.lc.Raw(ctx, "GET", "/v1/test/tick?to="+args[1]+"&seconds="+secs, nil, &out)
	case "http":
		if len(args) < 2 {
			return errors.New("用法：deneb debug http <设备> [端口]")
		}
		port := "80"
		if len(args) > 2 {
			port = args[2]
		}
		err = c.lc.Raw(ctx, "GET", "/v1/test/http?to="+args[1]+"&port="+port, nil, &out)
	case "ping-addr":
		if len(args) != 2 {
			return errors.New("用法：deneb debug ping-addr <地址>")
		}
		err = c.lc.Raw(ctx, "GET", "/v1/test/ping-addr?n=3&addr="+args[1], nil, &out)
	case "probe":
		// probe <设备> [ping|tcp|http] [端口] [间隔] [秒数]
		if len(args) < 2 {
			return errors.New("用法：deneb debug probe <设备> [ping|tcp|http] [端口] [间隔，如 250ms] [秒数]")
		}
		kind, port, interval, secs := "ping", "80", "250ms", "60"
		for i, a := range args[2:] {
			switch i {
			case 0:
				kind = a
			case 1:
				port = a
			case 2:
				interval = a
			case 3:
				secs = a
			}
		}
		path := fmt.Sprintf("/v1/test/probe?to=%s&kind=%s&port=%s&interval=%s&seconds=%s", args[1], kind, port, interval, secs)
		var oks, fails int
		err = c.lc.RawStream(ctx, path, func(b []byte) error {
			var l struct {
				T   time.Time `json:"t"`
				OK  bool      `json:"ok"`
				Ms  float64   `json:"ms"`
				Err string    `json:"err"`
			}
			if json.Unmarshal(b, &l) != nil {
				return nil
			}
			if l.OK {
				oks++
				fmt.Fprintf(c.out, "%s ok %.1fms\n", l.T.Local().Format("15:04:05.000"), l.Ms)
			} else {
				fails++
				fmt.Fprintf(c.out, "%s FAIL %s\n", l.T.Local().Format("15:04:05.000"), l.Err)
			}
			return nil
		})
		fmt.Fprintf(c.out, "%d ok, %d failed\n", oks, fails)
		return err
	case "drops":
		err = c.lc.Raw(ctx, "GET", "/v1/test/drops", nil, &out)
	case "paths":
		err = c.lc.Raw(ctx, "GET", "/v1/test/paths", nil, &out)
	default:
		return fmt.Errorf("不认识的 debug 子命令 %q", args[0])
	}
	if err != nil {
		if ae, ok := localclient.AsError(err); ok && ae.Code == apitype.ErrNotFound && strings.Contains(ae.Message, "404") {
			return errors.New("这个引擎不是测试构建（-tags denebtest），没有 debug 接口")
		}
		return err
	}
	c.out.Write(out)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		fmt.Fprintln(c.out)
	}
	return nil
}
