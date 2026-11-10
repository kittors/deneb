package control

import (
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
	msg, err := mail.ReadMessage(strings.NewReader(m.Data))
	if err != nil {
		t.Fatalf("the mail does not parse: %v\n%s", err, m.Data)
	}
	if to := msg.Header.Get("To"); to != "<"+m.To+">" {
		t.Fatalf("the mail is to %q, sent to %q", to, m.To)
	}
	if from, err := mail.ParseAddress(msg.Header.Get("From")); err != nil || from.Address != "noreply@example.com" || from.Name != "Deneb" {
		t.Fatalf("the mail is from %q", msg.Header.Get("From"))
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, msg.Body))
	if err != nil {
		t.Fatalf("the mail's text: %v", err)
	}
	code := sixDigits.FindString(subject)
	if code == "" || !strings.Contains(string(body), "你的 Deneb 验证码是 "+code+"，10 分钟内有效") || msg.Header.Get("Message-ID") == "" {
		t.Fatalf("the mail: subject %q\n%s", subject, body)
	}
	return code
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
		if err := f.mailer(t).SendCode(ctx, "lin@example.net", "042137"); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		m := f.next(t)
		if m.TLS != tc.tls || m.To != "lin@example.net" || m.From != "noreply@example.com" || (f.mechs != "") != (m.User == fakeUser) {
			t.Fatalf("%s: %+v", tc.name, m)
		}
		if code := codeIn(t, m); code != "042137" {
			t.Fatalf("%s: the mail carries %q", tc.name, code)
		}
	}

	f := newFakeSMTP(t, withStartTLS, withAuth("PLAIN"))
	m := f.mailer(t)
	m.cfg.Password = "wrong"
	if err := m.SendCode(ctx, "lin@example.net", "042137"); err == nil || !strings.Contains(err.Error(), "auth") {
		t.Fatalf("a wrong password: %v", err)
	}
	// A certificate it does not trust is no server to send to.
	m = f.mailer(t)
	m.roots = nil
	if err := m.SendCode(ctx, "lin@example.net", "042137"); err == nil {
		t.Fatal("sent over TLS to a certificate no one trusts")
	}
	f.none(t, 100*time.Millisecond)
	if err := f.mailer(t).SendCode(ctx, "lin@example.net\r\nBcc: x@example.net", "042137"); err == nil {
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

// TestCodeMail: the mail's headers are well formed whatever the sender's
// name, and its text says the code and how long it works.
func TestCodeMail(t *testing.T) {
	from, _ := mail.ParseAddress(`"Deneb 组网" <noreply@example.com>`)
	raw := codeMail(from, "lin@example.net", "000123", time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC))
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if f, err := mail.ParseAddress(msg.Header.Get("From")); err != nil || f.Name != "Deneb 组网" {
		t.Fatalf("From: %q", msg.Header.Get("From"))
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
	subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	body, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, msg.Body))
	if subject != "Deneb 验证码 000123" || !strings.Contains(string(body), "你的 Deneb 验证码是 000123，10 分钟内有效，只能用一次。") ||
		msg.Header.Get("Auto-Submitted") != "auto-generated" || !strings.HasSuffix(msg.Header.Get("Message-ID"), "@example.com>") {
		t.Fatalf("the mail: %q\n%s", subject, body)
	}
}
