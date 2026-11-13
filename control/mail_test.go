package control

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// fakeSMTP is a mail server on this machine, for the tests: it speaks
// enough SMTP for net/smtp (EHLO, STARTTLS, AUTH PLAIN and LOGIN, MAIL,
// RCPT, DATA, QUIT) and hands each mail it takes to mails.
type fakeSMTP struct {
	t        *testing.T
	ln       net.Listener
	port     int
	cert     tls.Certificate
	roots    *x509.CertPool
	startTLS bool          // offers STARTTLS
	implicit bool          // TLS from the first byte, as on port 465
	mechs    string        // the AUTH mechanisms it offers; empty: no login
	greet    time.Duration // waits this long before its greeting: a slow server
	mails    chan fakeMail
}

// fakeUser and fakePassword are the fake server's account: not anyone's.
const (
	fakeUser     = "deneb-test"
	fakePassword = "fake-smtp-password"
)

type fakeMail struct {
	From, To string
	Data     string
	TLS      bool   // it came over TLS
	User     string // who logged in to send it
}

func newFakeSMTP(t *testing.T, opts ...func(*fakeSMTP)) *fakeSMTP {
	t.Helper()
	cert, roots := testCert(t)
	f := &fakeSMTP{t: t, cert: cert, roots: roots, mails: make(chan fakeMail, 32)}
	for _, o := range opts {
		o(f)
	}
	var err error
	if f.implicit {
		f.ln, err = tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	} else {
		f.ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		t.Fatal(err)
	}
	f.port = f.ln.Addr().(*net.TCPAddr).Port
	t.Cleanup(func() { f.ln.Close() })
	go func() {
		for {
			c, err := f.ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func withStartTLS(f *fakeSMTP)    { f.startTLS = true }
func withImplicitTLS(f *fakeSMTP) { f.implicit = true }
func withAuth(mechs string) func(*fakeSMTP) {
	return func(f *fakeSMTP) { f.mechs = mechs }
}
func withGreetDelay(d time.Duration) func(*fakeSMTP) {
	return func(f *fakeSMTP) { f.greet = d }
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer func() { conn.Close() }()
	if f.greet > 0 {
		time.Sleep(f.greet)
	}
	tp := textproto.NewConn(conn)
	isTLS := f.implicit
	user := ""
	var m fakeMail
	tp.PrintfLine("220 fake ESMTP")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO") || strings.HasPrefix(cmd, "HELO"):
			ext := []string{"fake"}
			if f.startTLS && !isTLS {
				ext = append(ext, "STARTTLS")
			}
			if f.mechs != "" {
				ext = append(ext, "AUTH "+f.mechs)
			}
			ext = append(ext, "8BITMIME")
			for i, e := range ext {
				sep := "-"
				if i == len(ext)-1 {
					sep = " "
				}
				tp.PrintfLine("250%s%s", sep, e)
			}
		case cmd == "STARTTLS" && f.startTLS && !isTLS:
			tp.PrintfLine("220 go ahead")
			tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{f.cert}})
			if err := tc.Handshake(); err != nil {
				return
			}
			conn, tp, isTLS = tc, textproto.NewConn(tc), true
		case strings.HasPrefix(cmd, "AUTH PLAIN") && strings.Contains(f.mechs, "PLAIN"):
			arg := strings.TrimSpace(line[len("AUTH PLAIN"):])
			b, _ := base64.StdEncoding.DecodeString(arg)
			if parts := strings.Split(string(b), "\x00"); len(parts) == 3 && parts[1] == fakeUser && parts[2] == fakePassword {
				user = fakeUser
				tp.PrintfLine("235 ok")
			} else {
				tp.PrintfLine("535 no")
			}
		case cmd == "AUTH LOGIN" && strings.Contains(f.mechs, "LOGIN"):
			tp.PrintfLine("334 %s", base64.StdEncoding.EncodeToString([]byte("Username:")))
			u, _ := tp.ReadLine()
			tp.PrintfLine("334 %s", base64.StdEncoding.EncodeToString([]byte("Password:")))
			p, _ := tp.ReadLine()
			ub, _ := base64.StdEncoding.DecodeString(u)
			pb, _ := base64.StdEncoding.DecodeString(p)
			if string(ub) == fakeUser && string(pb) == fakePassword {
				user = fakeUser
				tp.PrintfLine("235 ok")
			} else {
				tp.PrintfLine("535 no")
			}
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if f.mechs != "" && user == "" {
				tp.PrintfLine("530 log in first")
				continue
			}
			m = fakeMail{From: angle(line), TLS: isTLS, User: user}
			tp.PrintfLine("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			m.To = angle(line)
			tp.PrintfLine("250 ok")
		case cmd == "DATA":
			tp.PrintfLine("354 go on")
			b, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			m.Data = string(b)
			f.mails <- m
			tp.PrintfLine("250 queued")
		case cmd == "QUIT":
			tp.PrintfLine("221 bye")
			return
		case cmd == "RSET" || cmd == "NOOP":
			tp.PrintfLine("250 ok")
		default:
			tp.PrintfLine("502 not here")
		}
	}
}

func angle(line string) string {
	i, j := strings.IndexByte(line, '<'), strings.LastIndexByte(line, '>')
	if i < 0 || j < i {
		return ""
	}
	return line[i+1 : j]
}

// mailer is an SMTPMailer that sends through f, trusting its certificate.
func (f *fakeSMTP) mailer(t *testing.T) *SMTPMailer {
	t.Helper()
	c := SMTPConfig{Host: "127.0.0.1", Port: f.port, From: "Deneb <noreply@example.com>", TLS: "starttls"}
	if f.implicit {
		c.TLS = "tls"
	}
	if f.mechs != "" {
		c.Username, c.Password = fakeUser, fakePassword
	}
	m, err := NewSMTPMailer(c)
	if err != nil {
		t.Fatal(err)
	}
	m.roots = f.roots
	return m
}

// next is the next mail f takes, within 5 s.
func (f *fakeSMTP) next(t *testing.T) fakeMail {
	t.Helper()
	select {
	case m := <-f.mails:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no mail went out")
	}
	return fakeMail{}
}

// none checks that no mail comes within d.
func (f *fakeSMTP) none(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case m := <-f.mails:
		t.Fatalf("a mail went out to %s", m.To)
	case <-time.After(d):
	}
}

var sixDigits = regexp.MustCompile(`\b[0-9]{6}\b`)

// codeIn reads a code mail: well formed, to whom it says, and the code in
// both its subject and its text.
func codeIn(t *testing.T, m fakeMail) string {
	t.Helper()
	msg, l := readLetter(t, m.Data)
	if to := msg.Header.Get("To"); to != "<"+m.To+">" {
		t.Fatalf("the mail is to %q, sent to %q", to, m.To)
	}
	if from, err := mail.ParseAddress(msg.Header.Get("From")); err != nil || from.Address != "noreply@example.com" || from.Name != "Deneb" {
		t.Fatalf("the mail is from %q", msg.Header.Get("From"))
	}
	code := sixDigits.FindString(l.Subject)
	if code == "" || !strings.Contains(l.Text, "你的 Deneb 登录验证码："+code) || !strings.Contains(l.Text, "10 分钟内有效，只能用一次") ||
		!strings.Contains(l.HTML, ">"+code+"</span>") || msg.Header.Get("Message-ID") == "" {
		t.Fatalf("the mail: subject %q\n%s", l.Subject, l.Text)
	}
	return code
}

// letter is a code mail taken apart.
type letter struct {
	Subject, Text, HTML, Link string
	Icon                      []byte
	IconID                    string
}

// loginLink finds a login link in a mail's text.
var loginLink = regexp.MustCompile(`https?://\S+/login/link#[A-Za-z0-9_-]+`)

// readLetter takes a code mail apart, checking its shape on the way:
// multipart/alternative of the plain text, then multipart/related of the
// HTML and its icon, each part in base64.
func readLetter(t *testing.T, raw string) (*mail.Message, letter) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("the mail does not parse: %v\n%s", err, raw)
	}
	var l letter
	if l.Subject, err = new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject")); err != nil {
		t.Fatal(err)
	}
	parts := func(r io.Reader, ct, want string) *multipart.Reader {
		t.Helper()
		mt, params, err := mime.ParseMediaType(ct)
		if err != nil || mt != want || params["boundary"] == "" {
			t.Fatalf("a part is %q, want %s", ct, want)
		}
		return multipart.NewReader(r, params["boundary"])
	}
	next := func(r *multipart.Reader, want string) ([]byte, textproto.MIMEHeader) {
		t.Helper()
		p, err := r.NextPart()
		if err != nil {
			t.Fatalf("no %s part: %v", want, err)
		}
		if mt, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type")); mt != want {
			t.Fatalf("a %s part, want %s", mt, want)
		}
		if p.Header.Get("Content-Transfer-Encoding") != "base64" {
			t.Fatalf("the %s part is not in base64", want)
		}
		b, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
		if err != nil {
			t.Fatalf("the %s part: %v", want, err)
		}
		return b, p.Header
	}
	alt := parts(msg.Body, msg.Header.Get("Content-Type"), "multipart/alternative")
	text, _ := next(alt, "text/plain")
	p, err := alt.NextPart()
	if err != nil {
		t.Fatalf("no HTML: %v", err)
	}
	rel := parts(p, p.Header.Get("Content-Type"), "multipart/related")
	html, _ := next(rel, "text/html")
	icon, h := next(rel, "image/png")
	if _, err := rel.NextPart(); err != io.EOF {
		t.Fatalf("more than the HTML and its icon: %v", err)
	}
	if _, err := alt.NextPart(); err != io.EOF {
		t.Fatalf("more than the text and the HTML: %v", err)
	}
	l.Text, l.HTML, l.Icon = string(text), string(html), icon
	l.IconID = strings.Trim(h.Get("Content-Id"), "<>")
	l.Link = loginLink.FindString(l.Text)
	return msg, l
}

// testCert is a certificate for 127.0.0.1 and the pool that trusts it.
func testCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake smtp"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// TestSMTPMailer: a code goes out over STARTTLS with PLAIN, over implicit
// TLS with LOGIN, and plainly only to this machine; a wrong password is an
// error, not a mail.
func TestSMTPMailer(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		opts []func(*fakeSMTP)
		tls  bool
	}{
		{"starttls, plain", []func(*fakeSMTP){withStartTLS, withAuth("PLAIN LOGIN")}, true},
		{"implicit tls, login", []func(*fakeSMTP){withImplicitTLS, withAuth("LOGIN")}, true},
		{"this machine, no tls", nil, false},
	} {
		f := newFakeSMTP(t, tc.opts...)
		link := "https://deneb.example.com/login/link#" + tc.name[:4]
		if err := f.mailer(t).SendCode(ctx, "lin@example.net", "042137", link); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		m := f.next(t)
		if m.TLS != tc.tls || m.To != "lin@example.net" || m.From != "noreply@example.com" || (f.mechs != "") != (m.User == fakeUser) {
			t.Fatalf("%s: %+v", tc.name, m)
		}
		if code := codeIn(t, m); code != "042137" {
			t.Fatalf("%s: the mail carries %q", tc.name, code)
		}
		if _, l := readLetter(t, m.Data); l.Link != link {
			t.Fatalf("%s: the mail's link is %q", tc.name, l.Link)
		}
	}

	f := newFakeSMTP(t, withStartTLS, withAuth("PLAIN"))
	m := f.mailer(t)
	m.cfg.Password = "wrong"
	if err := m.SendCode(ctx, "lin@example.net", "042137", ""); err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("a wrong password: %v", err)
	}
	// A certificate it does not trust is no server to send to.
	m = f.mailer(t)
	m.roots = nil
	if err := m.SendCode(ctx, "lin@example.net", "042137", ""); err == nil {
		t.Fatal("sent over TLS to a certificate no one trusts")
	}
	f.none(t, 100*time.Millisecond)
	if err := f.mailer(t).SendCode(ctx, "lin@example.net\r\nBcc: x@example.net", "042137", ""); err == nil {
		t.Fatal("sent to an address with a header in it")
	}

	// Only this machine hears the mail in the clear.
	for host, want := range map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, "smtp.example.com": false, "192.0.2.1": false} {
		if loopbackHost(host) != want {
			t.Errorf("loopbackHost(%q) = %v", host, !want)
		}
	}
}

// TestNewSMTPMailer: the settings are checked, and the defaults follow the
// port or the TLS asked for.
func TestNewSMTPMailer(t *testing.T) {
	ok := SMTPConfig{Host: "smtp.example.com", From: "Deneb <noreply@example.com>"}
	for _, tc := range []struct {
		edit       func(*SMTPConfig)
		port       int
		tls, error string
	}{
		{func(c *SMTPConfig) {}, 587, "starttls", ""},
		{func(c *SMTPConfig) { c.Port = 465 }, 465, "tls", ""},
		{func(c *SMTPConfig) { c.TLS = "tls" }, 465, "tls", ""},
		{func(c *SMTPConfig) { c.Port = 2525 }, 2525, "starttls", ""},
		{func(c *SMTPConfig) { c.Host = " " }, 0, "", "no SMTP host"},
		{func(c *SMTPConfig) { c.TLS = "none" }, 0, "", "want tls or starttls"},
		{func(c *SMTPConfig) { c.Username = "deneb" }, 0, "", "both, or neither"},
		{func(c *SMTPConfig) { c.From = "nobody" }, 0, "", "not an address"},
		{func(c *SMTPConfig) { c.Port = 70000 }, 0, "", "port"},
	} {
		c := ok
		tc.edit(&c)
		m, err := NewSMTPMailer(c)
		switch {
		case tc.error != "":
			if err == nil || !strings.Contains(err.Error(), tc.error) {
				t.Errorf("%+v: %v, want %q", c, err, tc.error)
			}
		case err != nil || m.cfg.Port != tc.port || m.cfg.TLS != tc.tls:
			t.Errorf("%+v: %+v, %v", c, m, err)
		}
	}
	m, _ := NewSMTPMailer(SMTPConfig{Host: "smtp.example.com", From: "noreply@example.com", Username: "deneb", Password: "secret-value"})
	if s := m.String(); strings.Contains(s, "secret-value") || !strings.Contains(s, "smtp.example.com:587") {
		t.Fatalf("the log line: %q", s)
	}
}

// TestLoadSMTPConfig: the file in the data directory, the environment over
// it; a file open to others is closed.
func TestLoadSMTPConfig(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if c, err := LoadSMTPConfig(dir, getenv); err != nil || c.Host != "" {
		t.Fatalf("no file: %+v, %v", c, err)
	}
	path := filepath.Join(dir, SMTPConfigFile)
	os.WriteFile(path, []byte(`{"host": "smtp.example.com", "port": 465, "username": "deneb", "password": "from-the-file", "from": "Deneb <noreply@example.com>"}`), 0o644)
	c, err := LoadSMTPConfig(dir, getenv)
	if err != nil || c.Host != "smtp.example.com" || c.Port != 465 || c.Password != "from-the-file" || c.From != "Deneb <noreply@example.com>" {
		t.Fatalf("the file: %+v, %v", c, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file is %v", fi.Mode())
	}
	env["DENEB_SMTP_HOST"], env["DENEB_SMTP_PORT"], env["DENEB_SMTP_PASSWORD"] = "mail.example.net", "587", "from-the-env"
	if c, err := LoadSMTPConfig(dir, getenv); err != nil || c.Host != "mail.example.net" || c.Port != 587 || c.Password != "from-the-env" || c.Username != "deneb" {
		t.Fatalf("the environment over it: %+v, %v", c, err)
	}
	env["DENEB_SMTP_PORT"] = "five"
	if _, err := LoadSMTPConfig(dir, getenv); err == nil {
		t.Fatal("a port that is not a number")
	}
	os.WriteFile(path, []byte(`{"host": `), 0o600)
	if _, err := LoadSMTPConfig(dir, func(string) string { return "" }); err == nil {
		t.Fatal("a broken file")
	}
}

// TestDeletedMail: the mail an operator's deleting an account sends its
// owner (DESIGN §14.15) is the code mail's kind, icon and all; it says the
// account went at their request, and nothing of who did it.
func TestDeletedMail(t *testing.T) {
	from, _ := mail.ParseAddress(`"Deneb" <noreply@example.com>`)
	raw, err := deletedMail(from, "leo@example.net", "https://deneb.example.com", time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	msg, l := readLetter(t, string(raw))
	switch {
	case l.Subject != "你的 Deneb 账号注销了", msg.Header.Get("To") != "<leo@example.net>", l.IconID != mailIconID, l.Link != "":
		t.Fatalf("the mail: %q %v", l.Subject, msg.Header)
	case !strings.Contains(l.Text, "应你的要求，你的 Deneb 账号注销了。"), !strings.Contains(l.Text, "注销的是 leo@example.net"),
		!strings.Contains(l.HTML, "应你的要求，你的 Deneb 账号注销了。"), !strings.Contains(l.HTML, `src="cid:`+mailIconID+`"`),
		!strings.Contains(l.HTML, ">deneb.example.com</a>"):
		t.Fatalf("what it says:\n%s", l.Text)
	case strings.Contains(l.Text+l.HTML, "管理员"), strings.Contains(l.Text+l.HTML, "操作"):
		t.Fatal("it says who did it")
	}
}

// TestCodeMail: the mail (DESIGN.md §14.14) is HTML and plain text in one,
// its headers well formed whatever the sender's name, every line ASCII and
// short; the code is in the subject, the text and the HTML; the icon goes
// with it, the brand's own, which the HTML names by its Content-ID and,
// blocked, shows as ✦. A browser's login gets the link button, a
// device's none.
func TestCodeMail(t *testing.T) {
	from, _ := mail.ParseAddress(`"Deneb 组网" <noreply@example.com>`)
	brand, err := os.ReadFile(filepath.Join("..", "design", "brand", "final", "macos", "AppIcon.iconset", "icon_32x32@2x.png"))
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{"", "https://deneb.example.com/login/link#AbC-12_x"} {
		raw, err := codeMail(from, "lin@example.net", codeLetter{Code: "000123", Link: link}, time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\r\n") {
			if len(line) > 998 {
				t.Fatalf("a line of %d bytes", len(line))
			}
			for _, r := range line {
				if r >= 0x80 {
					t.Fatalf("a raw non-ASCII header or line: %q", line)
				}
			}
		}
		msg, l := readLetter(t, string(raw))
		if f, err := mail.ParseAddress(msg.Header.Get("From")); err != nil || f.Name != "Deneb 组网" {
			t.Fatalf("From: %q", msg.Header.Get("From"))
		}
		switch {
		case l.Subject != "Deneb 验证码 000123", msg.Header.Get("Auto-Submitted") != "auto-generated",
			!strings.HasSuffix(msg.Header.Get("Message-ID"), "@example.com>"):
			t.Fatalf("the headers: %q, %v", l.Subject, msg.Header)
		case !strings.Contains(l.Text, "你的 Deneb 登录验证码：000123"), !strings.Contains(l.Text, "10 分钟内有效，只能用一次"),
			!strings.Contains(l.Text, "如果不是你在登录 Deneb，不用理会这封邮件"):
			t.Fatalf("the text:\n%s", l.Text)
		case !strings.Contains(l.HTML, ">000123</span>"), !strings.Contains(l.HTML, `src="cid:`+l.IconID+`"`), l.IconID != mailIconID,
			!strings.Contains(l.HTML, `alt="&#10022;"`), !strings.Contains(l.HTML, "<title>Deneb 验证码 000123</title>"):
			t.Fatalf("the HTML:\n%s", l.HTML)
		case !bytes.Equal(l.Icon, brand):
			t.Fatal("the icon is not the brand's 64 px app icon")
		}
		// DENEB_MAIL_PREVIEW=<dir> writes the two mails there, to open in a
		// browser (the icon inlined, as no browser loads cid:).
		if dir := os.Getenv("DENEB_MAIL_PREVIEW"); dir != "" {
			name := map[bool]string{false: "code-device", true: "code-browser"}[link != ""]
			html := strings.ReplaceAll(l.HTML, "cid:"+l.IconID, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(l.Icon))
			os.WriteFile(filepath.Join(dir, name+".html"), []byte(html), 0o644)
			os.WriteFile(filepath.Join(dir, name+".txt"), []byte(l.Text), 0o644)
		}
		button := strings.Contains(l.HTML, "在这台电脑上登录")
		if link == "" {
			if l.Link != "" || button || strings.Contains(l.HTML, "/login/link") {
				t.Fatalf("a device's mail with a link:\n%s", l.Text)
			}
			continue
		}
		if l.Link != link || !button || !strings.Contains(l.HTML, `href="`+link+`"`) ||
			!strings.Contains(l.HTML, `>deneb.example.com</a>`) || !strings.Contains(l.Text, "\r\nhttps://deneb.example.com\r\n") {
			t.Fatalf("a browser's mail: link %q, button %v\n%s", l.Link, button, l.Text)
		}
	}
}
