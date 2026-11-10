package client

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/engine/localapi/localclient"
	"github.com/kittors/deneb/proto"
)

// SignIn signs this computer in from the window's first step (DESIGN §7.3,
// M6 i5): the engine asks the server for a login and gets its link, and the
// email and then the code go to the server's login page as a browser sends
// them, over the server's own TLS, which the window checks by its pin.
// A server whose pin the window does not know (one someone runs on their
// own) is left to the browser: ErrUseBrowser.
type SignIn struct {
	// login starts a login on the engine and returns its link.
	login func(ctx context.Context) (string, error)
	// pins are the servers the window knows, by origin
	// ("https://host:port"), and their pins ("sha256:<hex>").
	pins map[string]string
	// client is how a page is posted to the server of pin; tests replace it.
	client func(pin string) *http.Client

	mu   sync.Mutex
	link string // the login under way, its link
}

var (
	// ErrWrongCode: the code is not the one sent, or was used, or ran out.
	ErrWrongCode = errors.New("验证码不对")
	// ErrUseBrowser: the window does not know this server's pin; its
	// login page is for the browser.
	ErrUseBrowser = errors.New("在浏览器里登录")
)

// NewSignIn signs in through lc's engine, to the servers of pins.
func NewSignIn(lc *localclient.Client, pins map[string]string) *SignIn {
	return &SignIn{login: func(ctx context.Context) (string, error) { return lc.Login(ctx, "") }, pins: pins, client: pinnedClient}
}

// SendCode asks the server to send a code to email, starting a login when
// none is under way, or a new one when the last ran out.
func (s *SignIn) SendCode(ctx context.Context, email string) error {
	for try := 0; ; try++ {
		base, code, pin, err := s.start(ctx)
		if err != nil {
			return err
		}
		pg, err := s.post(ctx, pin, base+"/login/email", url.Values{"c": {code}, "email": {email}})
		if err != nil {
			return err
		}
		switch {
		case pg.title == "输入验证码" && pg.err == "":
			return nil
		case pg.title == "这个登录链接过期了" && try == 0:
			s.forget()
			continue
		case pg.err != "":
			return errors.New(pg.err)
		}
		return fmt.Errorf("登录页说：%s", pg.title)
	}
}

// CheckCode hands the server the code sent to email; ErrWrongCode when it
// is not the one.
func (s *SignIn) CheckCode(ctx context.Context, email, code string) error {
	s.mu.Lock()
	link := s.link
	s.mu.Unlock()
	if link == "" {
		return errors.New("先发验证码")
	}
	base, c, pin, err := s.parse(link)
	if err != nil {
		return err
	}
	pg, err := s.post(ctx, pin, base+"/login/code", url.Values{"c": {c}, "email": {email}, "code": {code}})
	if err != nil {
		return err
	}
	switch {
	case pg.title == "登录好了":
		s.forget()
		return nil
	case strings.HasPrefix(pg.err, "验证码不对"):
		return ErrWrongCode
	case pg.err != "":
		return errors.New(pg.err)
	case pg.title == "这个登录链接过期了":
		s.forget()
		return errors.New("这次登录过期了，再发一次验证码")
	}
	return fmt.Errorf("登录页说：%s", pg.title)
}

// start is the login under way: its server, its code and the server's pin;
// the engine starts one when there is none.
func (s *SignIn) start(ctx context.Context) (base, code, pin string, err error) {
	s.mu.Lock()
	link := s.link
	s.mu.Unlock()
	if link == "" {
		if link, err = s.login(ctx); err != nil {
			return "", "", "", err
		}
		s.mu.Lock()
		s.link = link
		s.mu.Unlock()
	}
	return s.parse(link)
}

// forget drops the login under way: the next code starts another.
func (s *SignIn) forget() {
	s.mu.Lock()
	s.link = ""
	s.mu.Unlock()
}

// parse takes a login link apart: https://host:port/login?c=CODE.
func (s *SignIn) parse(link string) (base, code, pin string, err error) {
	u, err := url.Parse(link)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", "", fmt.Errorf("登录链接不对：%q", link)
	}
	base = u.Scheme + "://" + u.Host
	pin, ok := s.pins[base]
	if !ok {
		return "", "", "", ErrUseBrowser
	}
	if code = u.Query().Get("c"); code == "" {
		return "", "", "", fmt.Errorf("登录链接不对：%q", link)
	}
	return base, code, pin, nil
}

// page is what the login page says: its title, and the error under it.
type page struct{ title, err string }

var (
	pageTitle = regexp.MustCompile(`<h1>([^<]*)</h1>`)
	pageError = regexp.MustCompile(`<p class="error">([^<]*)</p>`)
)

// post sends form to the login page at u and reads what it says.
func (s *SignIn) post(ctx context.Context, pin, u string, form url.Values) (page, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", u, strings.NewReader(form.Encode()))
	if err != nil {
		return page{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.client(pin).Do(req)
	if err != nil {
		return page{}, fmt.Errorf("连不上 Deneb 服务器：%w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return page{}, err
	}
	var pg page
	if m := pageTitle.FindSubmatch(b); m != nil {
		pg.title = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}
	if m := pageError.FindSubmatch(b); m != nil {
		pg.err = html.UnescapeString(strings.TrimSpace(string(m[1])))
	}
	if resp.StatusCode == http.StatusTooManyRequests && pg.err == "" {
		pg.err = "试错的次数太多了，过一会儿再试"
	}
	return pg, nil
}

// pinnedClient trusts the one server whose key is pin, as the engine does
// (proto.PinnedTLSConfig). A pin it cannot read trusts nothing.
func pinnedClient(pin string) *http.Client {
	cfg, err := proto.PinnedTLSConfig(pin)
	if err != nil {
		cfg = &tls.Config{MinVersion: tls.VersionTLS13, VerifyConnection: func(tls.ConnectionState) error { return err }, InsecureSkipVerify: true}
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg},
		// The login page answers on its own address: never follow it
		// elsewhere.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
