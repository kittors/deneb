package control

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	texttemplate "text/template"
	"time"
)

// Mail: the login codes go out by mail (M7). How is pluggable: SMTP, which
// every mail service speaks, or, for development only, the server's log.
// Which service and which account are the deployment's: they never go
// into the repository.

// Mailer sends a login code to an email; link, when not empty, is a login
// link that works instead of the code (proto.LoginLinkURL), for a login
// begun in a browser.
type Mailer interface {
	SendCode(ctx context.Context, to, code, link string) error
}

// LogMailer writes the codes into the log instead of mailing them: for a
// development server only, since anyone who reads the log could log in
// as anyone (cmd/deneb-control allows it with -dev alone). The login link
// goes there too, for the same reason; a production log has neither.
type LogMailer struct {
	Logf func(format string, args ...any)
}

// SendCode logs the code, and the link.
func (m LogMailer) SendCode(_ context.Context, to, code, link string) error {
	m.Logf("mail (development, not sent): the login code for %s is %s", to, code)
	if link != "" {
		m.Logf("mail (development, not sent): its login link is %s", link)
	}
	return nil
}

// SMTPConfig is how to reach the mail service that sends the codes. It is
// the deployment's own: in <data>/mail.json (SMTPConfigFile, 0600), in the
// environment (DENEB_SMTP_*), or, all but the password, in flags.
type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"` // default 465 with TLS "tls", 587 otherwise
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// From is who the mail is from: "Deneb <noreply@example.com>".
	From string `json:"from"`
	// TLS is "tls" (implicit, the usual on port 465) or "starttls" (port
	// 587, the default). Either way the mail goes encrypted: a server that
	// offers neither is refused, unless it is on this machine.
	TLS string `json:"tls,omitempty"`
}

// SMTPConfigFile is the mail settings' file in the data directory.
const SMTPConfigFile = "mail.json"

// LoadSMTPConfig reads the mail settings: dir/mail.json if it is there,
// then the environment over it (DENEB_SMTP_HOST, _PORT, _USERNAME,
// _PASSWORD, _FROM, _TLS). Host empty means none are set. The file holds a
// password: it is made 0600 if it was open to others.
func LoadSMTPConfig(dir string, getenv func(string) string) (SMTPConfig, error) {
	var c SMTPConfig
	path := filepath.Join(dir, SMTPConfigFile)
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &c); err != nil {
			return c, fmt.Errorf("%s: %w", path, err)
		}
		if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
			os.Chmod(path, 0o600)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	for name, field := range map[string]*string{
		"DENEB_SMTP_HOST": &c.Host, "DENEB_SMTP_USERNAME": &c.Username, "DENEB_SMTP_PASSWORD": &c.Password,
		"DENEB_SMTP_FROM": &c.From, "DENEB_SMTP_TLS": &c.TLS,
	} {
		if v := getenv(name); v != "" {
			*field = v
		}
	}
	if v := getenv("DENEB_SMTP_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return c, fmt.Errorf("DENEB_SMTP_PORT %q: %w", v, err)
		}
		c.Port = p
	}
	return c, nil
}

// SMTPMailer mails the codes through an SMTP server, encrypted, with the
// deployment's account.
type SMTPMailer struct {
	// Site is the Web version's address (-web-url), named at the foot of
	// the mail; empty, the mail names none.
	Site string

	cfg  SMTPConfig
	from *mail.Address
	// roots are the CAs to trust instead of the system's: tests only.
	roots *x509.CertPool
}

// NewSMTPMailer checks the settings and fills in the defaults.
func NewSMTPMailer(c SMTPConfig) (*SMTPMailer, error) {
	c.Host = strings.TrimSpace(c.Host)
	if c.Host == "" {
		return nil, errors.New("mail: no SMTP host")
	}
	switch c.TLS {
	case "":
		c.TLS = "starttls"
		if c.Port == 465 {
			c.TLS = "tls"
		}
	case "tls", "starttls":
	default:
		return nil, fmt.Errorf("mail: TLS %q: want tls or starttls", c.TLS)
	}
	if c.Port == 0 {
		c.Port = 587
		if c.TLS == "tls" {
			c.Port = 465
		}
	}
	if c.Port < 1 || c.Port > 65535 {
		return nil, fmt.Errorf("mail: port %d", c.Port)
	}
	if (c.Username == "") != (c.Password == "") {
		return nil, errors.New("mail: give the SMTP username and password both, or neither")
	}
	from, err := mail.ParseAddress(c.From)
	if err != nil || !validEmail(strings.ToLower(from.Address)) {
		return nil, fmt.Errorf("mail: from %q is not an address", c.From)
	}
	return &SMTPMailer{cfg: c, from: from}, nil
}

// String says where the mail goes, without the password, for the log.
func (m *SMTPMailer) String() string {
	return fmt.Sprintf("%s:%d (%s) as %s", m.cfg.Host, m.cfg.Port, m.cfg.TLS, m.from.Address)
}

// mailTimeout bounds one mail's whole conversation with the server.
const mailTimeout = 30 * time.Second

// SendCode mails code, and link if there is one, to to.
func (m *SMTPMailer) SendCode(ctx context.Context, to, code, link string) error {
	if !validEmail(to) {
		return fmt.Errorf("mail: %q is not an address", to)
	}
	msg, err := codeMail(m.from, to, codeLetter{Code: code, Link: link, Site: m.Site}, time.Now())
	if err != nil {
		return err
	}
	return m.send(ctx, to, msg)
}

func (m *SMTPMailer) tlsConfig() *tls.Config {
	return &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12, RootCAs: m.roots}
}

func (m *SMTPMailer) send(ctx context.Context, to string, msg []byte) error {
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	nd := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	var err error
	if m.cfg.TLS == "tls" {
		conn, err = (&tls.Dialer{NetDialer: nd, Config: m.tlsConfig()}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = nd.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(mailTimeout))
	// Closing the server ends a conversation that hangs.
	defer context.AfterFunc(ctx, func() { conn.Close() })()
	c, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if m.cfg.TLS == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(m.tlsConfig()); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		} else if !loopbackHost(m.cfg.Host) {
			// The password and the codes do not cross the network in
			// the clear.
			return errors.New("the mail server offers no STARTTLS")
		}
	}
	if m.cfg.Username != "" {
		a, err := m.auth(c)
		if err != nil {
			return err
		}
		if err := c.Auth(a); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	if err := c.Mail(m.from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(msg); err != nil {
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// auth picks how to log in: PLAIN, which most services take, or LOGIN.
func (m *SMTPMailer) auth(c *smtp.Client) (smtp.Auth, error) {
	ok, mechs := c.Extension("AUTH")
	if !ok {
		return nil, errors.New("the mail server takes no login")
	}
	ms := strings.Fields(strings.ToUpper(mechs))
	switch {
	case slices.Contains(ms, "PLAIN"):
		return smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host), nil
	case slices.Contains(ms, "LOGIN"):
		return &loginAuth{username: m.cfg.Username, password: m.cfg.Password, host: m.cfg.Host}, nil
	}
	return nil, fmt.Errorf("the mail server takes no login this server speaks: %s", mechs)
}

// loginAuth is SMTP's LOGIN, which some services take instead of PLAIN.
// Like smtp.PlainAuth it sends the password only over TLS, or to this
// machine.
type loginAuth struct{ username, password, host string }

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !loopbackHost(server.Name) {
		return "", nil, errors.New("unencrypted connection")
	}
	if server.Name != a.host {
		return "", nil, errors.New("wrong host name")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(a.username), nil
	case "password:":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("unexpected LOGIN challenge %q", fromServer)
}

// loopbackHost reports whether host is this machine.
func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// The login code's mail (DESIGN.md §14.14): HTML and plain text in one
// message, the icon going with it, so no mail program fetches anything
// from the web.
var (
	//go:embed mailtmpl/code.html
	codeHTMLSource string
	//go:embed mailtmpl/code.txt
	codeTextSource string
	// mailIcon is the 64 px app icon of design/brand/final
	// (macos/AppIcon.iconset/icon_32x32@2x.png), the mail's one picture.
	//go:embed mailtmpl/icon.png
	mailIcon []byte

	codeHTML = htmltemplate.Must(htmltemplate.New("code.html").Parse(codeHTMLSource))
	codeText = texttemplate.Must(texttemplate.New("code.txt").Parse(codeTextSource))
)

// mailIconID is the icon's Content-ID, which the HTML names as cid:.
const mailIconID = "deneb-icon@deneb.mail"

// codeLetter is what one login code's mail says.
type codeLetter struct {
	Code string
	// Link is a login link for a login begun in a browser (the button);
	// empty for a device's.
	Link string
	// Site is the Web version's address for the foot of the mail; empty,
	// the link's site, if there is a link.
	Site string
}

// codeMail is the mail with a login code: the code in the subject too, so
// it shows in a phone's notification. It is multipart/alternative, the
// plain text first, then the HTML with its icon (multipart/related), which
// is what a program that shows HTML takes.
func codeMail(from *mail.Address, to string, l codeLetter, now time.Time) ([]byte, error) {
	site, siteName := l.Site, ""
	if site == "" && l.Link != "" {
		if u, err := url.Parse(l.Link); err == nil {
			site = u.Scheme + "://" + u.Host
		}
	}
	if u, err := url.Parse(site); site != "" && err == nil {
		siteName = u.Host
	}
	data := struct {
		Code, Link, Site, SiteName string
		Icon                       htmltemplate.URL
		Minutes                    int
	}{l.Code, l.Link, site, siteName, htmltemplate.URL("cid:" + mailIconID), int(codeLife / time.Minute)}
	var text, html bytes.Buffer
	if err := codeText.Execute(&text, data); err != nil {
		return nil, err
	}
	if err := codeHTML.Execute(&html, data); err != nil {
		return nil, err
	}

	var body bytes.Buffer
	alt := multipart.NewWriter(&body)
	if err := mailPart(alt, `text/plain; charset="utf-8"`, crlf(text.Bytes()), nil); err != nil {
		return nil, err
	}
	var related bytes.Buffer
	rel := multipart.NewWriter(&related)
	if err := mailPart(rel, `text/html; charset="utf-8"`, crlf(html.Bytes()), nil); err != nil {
		return nil, err
	}
	if err := mailPart(rel, `image/png; name="deneb.png"`, mailIcon, textproto.MIMEHeader{
		"Content-Id":          {"<" + mailIconID + ">"},
		"Content-Disposition": {`inline; filename="deneb.png"`},
	}); err != nil {
		return nil, err
	}
	rel.Close()
	rp, err := alt.CreatePart(textproto.MIMEHeader{"Content-Type": {`multipart/related; type="text/html"; boundary="` + rel.Boundary() + `"`}})
	if err != nil {
		return nil, err
	}
	rp.Write(related.Bytes())
	alt.Close()

	var b bytes.Buffer
	header := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	header("From", from.String())
	header("To", "<"+to+">")
	header("Subject", mime.BEncoding.Encode("utf-8", "Deneb 验证码 "+l.Code))
	header("Date", now.Format(time.RFC1123Z))
	header("Message-ID", "<"+strings.ToLower(randomCode(20))+"@"+from.Address[strings.LastIndexByte(from.Address, '@')+1:]+">")
	header("MIME-Version", "1.0")
	header("Content-Type", `multipart/alternative; boundary="`+alt.Boundary()+`"`)
	// No vacation replies to it (RFC 3834).
	header("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	b.Write(body.Bytes())
	return b.Bytes(), nil
}

// mailPart adds a part of type ct, its content in base64 in lines of 76.
func mailPart(w *multipart.Writer, ct string, content []byte, more textproto.MIMEHeader) error {
	h := textproto.MIMEHeader{"Content-Type": {ct}, "Content-Transfer-Encoding": {"base64"}}
	for k, v := range more {
		h[k] = v
	}
	p, err := w.CreatePart(h)
	if err != nil {
		return err
	}
	enc := base64.StdEncoding.EncodeToString(content)
	for len(enc) > 76 {
		io.WriteString(p, enc[:76]+"\r\n")
		enc = enc[76:]
	}
	_, err = io.WriteString(p, enc+"\r\n")
	return err
}

// crlf ends every line with CRLF, as mail's text does.
func crlf(b []byte) []byte {
	return bytes.ReplaceAll(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
}
