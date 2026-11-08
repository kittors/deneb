package control

import (
	"crypto/subtle"
	"errors"
	"hash/fnv"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/kittors/deneb/proto"
)

// The server's own two pages (implementation.md §3 M1): the login page a
// device's login link opens, and the landing page of an invite link. They
// are plain HTML with no script; they are not the Web client (M3).

func (s *Server) webRoutes(m *http.ServeMux) {
	// The forms post only from these pages: the check refuses cross-site
	// posts, so another site cannot submit them in someone's browser.
	cop := http.NewCrossOriginProtection()
	post := func(h http.HandlerFunc) http.Handler { return cop.Handler(h) }
	m.HandleFunc("GET /{$}", s.handleLoginPage)
	m.HandleFunc("GET /login", s.handleLoginPage)
	m.Handle("POST /login/email", post(s.handleLoginEmail))
	m.Handle("POST /login/code", post(s.handleLoginCode))
	m.HandleFunc("GET "+proto.InvitePath+"{token}", s.handleInvitePage)
}

// Every device login asks for an email and its code, even in a browser that
// logged in a device before. The browser is often not the device's owner's:
// in testing one person logs in everyone's devices from one Mac, and a
// remembered account one click away put devices under the wrong person.

// loginPage is what the login template shows.
type loginPage struct {
	Step    string // none, expired, email, code, done, error
	Code    string // the device's login code, carried through the forms
	Device  *pageDevice
	Email   string
	Account string // on the code step, whose account it is: "登录到阿杰（jie@deneb.test）"
	Error   string
	Dev     bool
	Done    string // the done step's sentence
}

type pageDevice struct {
	Name, OS, From, When string
}

func (s *Server) pageDevice(pl *pendingLogin) *pageDevice {
	name := pl.req.DisplayName
	if name == "" {
		name = strings.TrimSuffix(pl.req.Hostname, ".local")
	}
	if name == "" {
		name = "一台新设备"
	}
	when := "刚刚"
	if m := int(s.now().Sub(pl.created).Minutes()); m >= 1 {
		when = strconv.Itoa(m) + " 分钟前"
	}
	return &pageDevice{Name: name, OS: osLabel(pl.req.OS), From: pl.remoteIP, When: when}
}

func osLabel(o proto.OS) string {
	switch o {
	case proto.MacOS:
		return "macOS"
	case proto.Windows:
		return "Windows"
	case proto.Linux:
		return "Linux"
	case proto.IOS:
		return "iOS"
	case proto.Android:
		return "Android"
	}
	return ""
}

// shortCode is enough of a login code to tell logins apart in the log,
// and not enough to use one.
func shortCode(c string) string {
	if len(c) > 4 {
		c = c[:4]
	}
	return c + "…"
}

// pendingFor finds the login a page is about, and fills the page for it.
func (s *Server) pendingFor(r *http.Request, code string, page *loginPage) *pendingLogin {
	page.Dev = s.cfg.Dev
	if code == "" {
		page.Step = "none"
		return nil
	}
	pl := s.logins.byLoginCode(code, s.now())
	if pl == nil {
		s.logf("login page: no login waits for code %s (expired or used), from %s", shortCode(code), remoteIP(r))
		page.Step = "expired"
		return nil
	}
	page.Code = pl.code
	page.Device = s.pageDevice(pl)
	return pl
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	var page loginPage
	if pl := s.pendingFor(r, r.URL.Query().Get("c"), &page); pl != nil {
		page.Step = "email"
	}
	s.render(w, http.StatusOK, loginTmpl, page)
}

func (s *Server) handleLoginEmail(w http.ResponseWriter, r *http.Request) {
	var page loginPage
	pl := s.pendingFor(r, r.PostFormValue("c"), &page)
	if pl == nil {
		s.render(w, http.StatusOK, loginTmpl, page)
		return
	}
	page.Email = normEmail(r.PostFormValue("email"))
	page.Step = "email"
	p := s.state.Load().w.byEmail[page.Email]
	if _, ok := s.codes[page.Email]; !ok || p == nil {
		s.logf("login: %q has no account (code %s, from %s)", page.Email, shortCode(pl.code), remoteIP(r))
		page.Error = "这个邮箱还没有开通测试账号"
		s.render(w, http.StatusOK, loginTmpl, page)
		return
	}
	page.Step = "code"
	page.Account = pangu("登录到" + p.DisplayName + "（" + p.Email + "）")
	s.render(w, http.StatusOK, loginTmpl, page)
}

func (s *Server) handleLoginCode(w http.ResponseWriter, r *http.Request) {
	var page loginPage
	pl := s.pendingFor(r, r.PostFormValue("c"), &page)
	if pl == nil {
		s.render(w, http.StatusOK, loginTmpl, page)
		return
	}
	page.Email = normEmail(r.PostFormValue("email"))
	page.Step = "code"
	p := s.state.Load().w.byEmail[page.Email]
	if p != nil {
		page.Account = pangu("登录到" + p.DisplayName + "（" + p.Email + "）")
	}
	now := s.now()
	if s.tries.blocked(page.Email, now) {
		s.logf("login: %q is blocked for too many wrong codes (code %s, from %s)", page.Email, shortCode(pl.code), remoteIP(r))
		page.Error = "试错的次数太多了，请 15 分钟后再试"
		s.render(w, http.StatusTooManyRequests, loginTmpl, page)
		return
	}
	want, ok := s.codes[page.Email]
	got := strings.TrimSpace(r.PostFormValue("code"))
	if !ok || p == nil || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		s.tries.failed(page.Email, now)
		s.logf("login: wrong code for %q (code %s, from %s)", page.Email, shortCode(pl.code), remoteIP(r))
		page.Error = "验证码不对"
		s.render(w, http.StatusOK, loginTmpl, page)
		return
	}
	s.tries.reset(page.Email)
	s.finishPage(w, r, pl, p.ID, &page)
}

// finishPage logs p in on the device and shows the done step.
func (s *Server) finishPage(w http.ResponseWriter, r *http.Request, pl *pendingLogin, p proto.PersonID, page *loginPage) {
	if _, err := s.completeLogin(pl, p, "in a browser at "+remoteIP(r)); err != nil {
		var ae *apiError
		page.Step, page.Error = "error", "登录没有成功，请稍后再试"
		if errors.As(err, &ae) {
			page.Error = ae.body.Message
		} else {
			s.logf("login %s: %v", shortCode(pl.code), err)
		}
		s.render(w, http.StatusOK, loginTmpl, page)
		return
	}
	owner := ""
	if pp := s.state.Load().w.people[p]; pp != nil {
		owner = pp.DisplayName
	}
	page.Step = "done"
	page.Done = pangu("「" + page.Device.Name + "」现在在" + owner + "名下。可以回到 Deneb 了；命令行会自己接着往下走。")
	s.render(w, http.StatusOK, loginTmpl, page)
}

// invitePage is what the invite template shows.
type invitePage struct {
	Found    bool
	State    proto.InviteState
	Title    string
	Lead     string
	Initial  string
	Color    template.CSS
	JoinLink template.URL // deneb://, which html/template would otherwise blank out
	URL      string
	Hint     string
}

func (s *Server) handleInvitePage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	var page invitePage
	if p, err := s.invitePreview(s.state.Load().w, token, ""); err == nil {
		name := p.Inviter.DisplayName
		page = invitePage{
			Found:    true,
			State:    p.State,
			Initial:  p.Inviter.Initial,
			Color:    template.CSS(ballColor(p.Inviter.ID)),
			JoinLink: template.URL(proto.JoinLink(token)),
			URL:      proto.InviteURL(s.cfg.PublicURL, token),
		}
		switch p.State {
		case proto.InvitePending:
			page.Title = pangu(name + "邀请你加入「" + p.LANName + "」")
			page.Lead = "在 Deneb 里打开，你的设备就能和「" + p.LANName + "」里的设备连在一起。"
			page.Hint = pangu("还没装 Deneb？装好后再点一次上面的按钮。这个邀请" + p.ExpiresAt.In(chinaTime).Format("1 月 2 日") + "前有效，只能一个人用。")
		case proto.InviteExpired:
			page.Title = "这个邀请已经过期了"
		case proto.InviteAccepted:
			page.Title = "这个邀请已经有人用过了"
		case proto.InviteRevoked:
			page.Title = "这个邀请已经被收回了"
		default:
			page.Title = "这个邀请已经被拒绝了"
		}
		if p.State != proto.InvitePending {
			page.Lead = pangu("请" + name + "重新发一个邀请链接。每个链接只能一个人用。")
		}
	}
	status := http.StatusOK
	if !page.Found {
		status = http.StatusNotFound
	}
	s.render(w, status, inviteTmpl, page)
}

// chinaTime is the zone the pages print dates in: the scratch image has no
// zone database, and the people using M1 are in UTC+8.
var chinaTime = time.FixedZone("UTC+8", 8*3600)

// ballColor picks someone's color on a page that has no "me": a fixed rule
// on the id, from the owner colors of the client (DESIGN §3.3).
func ballColor(id proto.PersonID) string {
	colors := []string{"#EC4899", "#F59E0B", "#8B5CF6", "#10B981", "#06B6D4", "#F97316"}
	h := fnv.New32a()
	h.Write([]byte(id))
	return colors[h.Sum32()%uint32(len(colors))]
}

// pangu puts a space between Chinese and Latin letters or digits, as the
// interface sets them ("Mia 邀请你加入", "阿杰邀请你加入"); full-width
// punctuation takes no space.
func pangu(s string) string {
	var b strings.Builder
	var prev rune
	for i, r := range s {
		if i > 0 && (isHan(prev) && isLatin(r) || isLatin(prev) && isHan(r)) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

func isHan(r rune) bool { return unicode.Is(unicode.Han, r) }

func isLatin(r rune) bool { return r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) }

func (s *Server) render(w http.ResponseWriter, status int, t *template.Template, data any) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	if err := t.Execute(w, data); err != nil {
		s.logf("render: %v", err)
	}
}

const pageHead = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{block "title" .}}Deneb{{end}}</title>
<style>
:root { color-scheme: light; }
body { margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
  background: #F6F7F9; color: #1F2328;
  font: 15px/1.6 -apple-system, BlinkMacSystemFont, "PingFang SC", "Microsoft YaHei", "Segoe UI", sans-serif; }
.card { width: min(400px, calc(100vw - 32px)); box-sizing: border-box; background: #fff; border: 1px solid #E6E8EB;
  border-radius: 16px; box-shadow: 0 8px 30px rgba(15, 23, 42, .06); padding: 32px 28px; }
h1 { font-size: 20px; line-height: 1.4; margin: 0 0 8px; font-weight: 600; }
p { margin: 0 0 16px; color: #57606A; }
.device { display: flex; flex-direction: column; padding: 12px 14px; border-radius: 12px; background: #F6F7F9; margin: 0 0 20px; }
.device b { font-weight: 600; color: #1F2328; }
.device span { font-size: 13px; color: #8C959F; }
label { display: block; font-size: 13px; color: #57606A; margin-bottom: 6px; }
input { width: 100%; box-sizing: border-box; font: inherit; padding: 10px 12px; border: 1px solid #D0D7DE; border-radius: 10px; outline: none; }
input:focus { border-color: #3B82F6; box-shadow: 0 0 0 3px rgba(59, 130, 246, .15); }
.button { display: block; width: 100%; box-sizing: border-box; margin-top: 16px; padding: 11px 14px; border: 0; border-radius: 10px;
  background: #3B82F6; color: #fff; font: inherit; font-weight: 500; text-align: center; text-decoration: none; cursor: pointer; }
.error { color: #D1242F; font-size: 13px; margin: 8px 0 0; }
.hint { font-size: 13px; color: #8C959F; margin: 16px 0 0; }
.who { display: flex; align-items: center; gap: 12px; margin: 0 0 16px; }
.ball { width: 40px; height: 40px; border-radius: 50%; color: #fff; display: flex; align-items: center; justify-content: center; font-weight: 600; flex: none; }
code { display: block; margin-top: 6px; font: 13px/1.5 ui-monospace, SFMono-Regular, Menlo, monospace; background: #F6F7F9;
  border-radius: 8px; padding: 10px 12px; word-break: break-all; color: #1F2328; user-select: all; }
.section { margin-top: 24px; padding-top: 16px; border-top: 1px solid #EEF0F2; font-size: 13px; color: #57606A; }
</style>
</head>
<body><main class="card">`

const pageFoot = `</main></body></html>`

var loginTmpl = template.Must(template.New("login").Parse(pageHead + `
{{- define "device"}}{{with .Device}}<div class="device"><b>{{.Name}}</b><span>{{if .OS}}{{.OS}} · {{end}}从 {{.From}} 发起 · {{.When}}</span></div>{{end}}{{end -}}
{{- if eq .Step "none"}}
<h1>登录 Deneb</h1>
<p>在 Deneb 里点"登录"，或者在命令行执行 <b>deneb login</b>，会给你一个登录链接。</p>
{{- else if eq .Step "expired"}}
<h1>这个登录链接过期了</h1>
<p>登录链接 1 小时内有效，用过一次也会失效。回到要登录的那台设备上拿一条新的：</p>
<p>在 Deneb 里再点一次"登录"；在命令行里执行 <b>deneb login</b>，或者用 <b>deneb status</b> 看当前的链接。</p>
{{- else if eq .Step "email"}}
<h1>登录 Deneb</h1>
<p>填这台设备主人的邮箱，登录后它就记在这个人名下。</p>
{{template "device" .}}
<form method="post" action="/login/email">
<input type="hidden" name="c" value="{{.Code}}">
<label for="email">邮箱</label>
<input id="email" name="email" type="email" value="{{.Email}}" autocomplete="email" required autofocus>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
<button class="button" type="submit">下一步</button>
</form>
{{- else if eq .Step "code"}}
<h1>输入验证码</h1>
<p>{{.Account}}</p>
{{template "device" .}}
<form method="post" action="/login/code">
<input type="hidden" name="c" value="{{.Code}}">
<input type="hidden" name="email" value="{{.Email}}">
<label for="code">验证码</label>
<input id="code" name="code" inputmode="numeric" autocomplete="one-time-code" required autofocus>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
<button class="button" type="submit">登录</button>
</form>
<p class="hint">{{if .Dev}}开发模式：验证码都是 000000。{{else}}测试期间不发邮件，测试账号的验证码写在服务器的种子文件里。{{end}}</p>
{{- else if eq .Step "done"}}
<h1>登录好了</h1>
<p>{{.Done}}</p>
{{- else}}
<h1>没有登录成功</h1>
<p>{{.Error}}</p>
{{- end}}
` + pageFoot))

var inviteTmpl = template.Must(template.New("invite").Parse(pageHead + `
{{- if not .Found}}
<h1>这个邀请链接不存在</h1>
<p>检查一下链接有没有复制完整。</p>
{{- else}}
<div class="who"><div class="ball" style="background: {{.Color}}">{{.Initial}}</div><h1>{{.Title}}</h1></div>
<p>{{.Lead}}</p>
{{- if eq .State "pending"}}
<a class="button" href="{{.JoinLink}}">用 Deneb 打开</a>
<p class="hint">{{.Hint}}</p>
<div class="section">在服务器或 NAS 上，执行：<code>deneb lan join {{.URL}}</code></div>
{{- end}}
{{- end}}
` + pageFoot))
