package control

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"math/big"
	"net/http"
	"net/mail"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/kittors/deneb/proto"
)

// Logging in by email (M7 review, design/plan/m7/control-security.md §6).
// Each login mails a new code: six random digits that work once, within
// ten minutes. The answers never tell whether an email has an account:
// both steps answer the same for every well-formed email, at about the
// same speed, since the mail goes out in the background. The seed's test
// accounts, at reserved domains, keep their fixed code and get no mail.
// With sign-up open (Config.OpenSignup), an email with no account gets a
// code too, and its account is made when the code checks out.

const (
	// codeLife is how long a mailed code works.
	codeLife = 10 * time.Minute
	// codeResend is how often a code goes out at most: asking again
	// sooner changes nothing. A code with less than half its life left
	// is replaced; until then every mail carries the same one, so a slow
	// mail and its resend never disagree.
	codeResend = time.Minute
	// maxSendsPerEmail codes go to one email in an hour at most: the
	// server is no way to fill someone's mailbox.
	maxSendsPerEmail = 5
	// maxAsksPerAddress codes may be asked for from one address (an IPv6
	// /64) in an hour, whatever the emails: one address cannot mail many
	// people, or probe many emails' rate limits.
	maxAsksPerAddress = 30
	// maxWrongPerAddress wrong codes may come from one address in
	// attemptsWindow, whatever the emails: one address cannot spread its
	// guesses over many accounts. Per email it is maxAttempts.
	maxWrongPerAddress = 30
	// maxLoginCodes codes wait at once at most; past it no new one is
	// made (sign-up open lets anyone make one per email).
	maxLoginCodes = 20000
	// mailQueueSize mails wait to go out at most; past it they are
	// dropped (and logged), and the answer is the same.
	mailQueueSize = 256
)

var (
	errBadEmail       = errInvalid("邮箱的格式不对")
	errWrongCode      = errInvalid("验证码不对，或者已经用过、过期了")
	errTooManyTries   = newErr(http.StatusTooManyRequests, proto.ErrForbidden, "试错的次数太多了，请 15 分钟后再试")
	errTooManyAsks    = newErr(http.StatusTooManyRequests, proto.ErrForbidden, "要验证码太频繁了，请过一会儿再试")
	errSignupDisabled = errWrongCode // a code from while sign-up was open
)

// validEmail is an email this server takes: plain ASCII, no spaces or
// display name, as net/mail reads it, up to 254 bytes. It goes into mail
// headers and SMTP commands as it is.
func validEmail(e string) bool {
	if e == "" || len(e) > 254 {
		return false
	}
	for i := 0; i < len(e); i++ {
		if e[i] <= ' ' || e[i] >= 0x7f {
			return false
		}
	}
	at := strings.LastIndexByte(e, '@')
	if at <= 0 || at == len(e)-1 {
		return false
	}
	a, err := mail.ParseAddress(e)
	return err == nil && a.Name == "" && a.Address == e
}

// isTestEmail reports whether e is at a domain reserved for testing (RFC
// 2606, 6761), where no one receives mail: only such seed accounts may
// have a fixed code.
func isTestEmail(e string) bool {
	d := e[strings.LastIndexByte(e, '@')+1:]
	for _, r := range []string{"test", "example", "invalid", "localhost", "example.com", "example.net", "example.org"} {
		if d == r || strings.HasSuffix(d, "."+r) {
			return true
		}
	}
	return false
}

// addressKey is what the per-address limits count by: the IPv4 address,
// or the IPv6 /64, which one household or one machine usually has whole.
func addressKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	if a.Is4() || a.Is4In6() {
		return a.Unmap().String()
	}
	p, _ := a.Prefix(64)
	return p.String()
}

// newLoginCode is six random digits.
func newLoginCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic(err) // crypto/rand does not fail
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// mailedCode is a code mailed to one email.
type mailedCode struct {
	code    string
	created time.Time
	sent    time.Time // when it last went out
	wrong   int       // wrong codes tried against it
	// links are the hashes of the login links mailed with it (a new one
	// with each mail, as only hashes are kept): each logs in instead of
	// the code, and using any of them or the code uses up all.
	links []string
}

func (c *mailedCode) live(now time.Time) bool { return now.Before(c.created.Add(codeLife)) }

// loginCodes are the codes waiting to be typed, one per email, in memory:
// after a restart people ask again.
type loginCodes struct {
	mu    sync.Mutex
	codes map[string]*mailedCode
	links map[string]string      // a login link's hash → its email
	sends map[string][]time.Time // per email, the sends within the hour
	swept time.Time
}

func newLoginCodes() *loginCodes {
	return &loginCodes{codes: map[string]*mailedCode{}, links: map[string]string{}, sends: map[string][]time.Time{}}
}

// drop forgets email's code and its links; t.mu is held.
func (t *loginCodes) drop(email string) {
	if c := t.codes[email]; c != nil {
		for _, h := range c.links {
			delete(t.links, h)
		}
		delete(t.codes, email)
	}
}

// issue returns the code to mail to email now, and with withLink a new
// login link's token for it, or "" and why none goes out: one went out
// less than codeResend ago, the hour's sends are used up, or too many
// codes are waiting.
func (t *loginCodes) issue(email string, now time.Time, withLink bool) (code, link, why string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gc(now)
	c := t.codes[email]
	if c != nil && c.live(now) && now.Sub(c.sent) < codeResend {
		return "", "", "one went out less than a minute ago"
	}
	var sends []time.Time
	for _, s := range t.sends[email] {
		if now.Sub(s) < time.Hour {
			sends = append(sends, s)
		}
	}
	if len(sends) >= maxSendsPerEmail {
		return "", "", "the hour's sends are used up"
	}
	switch {
	case c != nil && c.live(now) && now.Sub(c.created) < codeLife/2:
		why = "sent again"
	case c == nil && len(t.codes) >= maxLoginCodes:
		return "", "", "too many codes are waiting"
	default:
		// A new code: the old one's links go with it.
		t.drop(email)
		c = &mailedCode{code: newLoginCode(), created: now}
		t.codes[email] = c
		why = "sent"
	}
	if withLink {
		link = newRelaySecret() // 256 random bits, as a session's token
		h := hashSecret(link)
		c.links = append(c.links, h)
		t.links[h] = email
	}
	c.sent = now
	t.sends[email] = append(sends, now)
	return c.code, link, why
}

// link finds the live code a login link is for, by its token; t.mu is
// held.
func (t *loginCodes) link(token string, now time.Time) (string, *mailedCode) {
	h := hashSecret(token)
	email, ok := t.links[h]
	if !ok {
		return "", nil
	}
	c := t.codes[email]
	if c == nil || !c.live(now) || !slices.Contains(c.links, h) {
		delete(t.links, h)
		return "", nil
	}
	return email, c
}

// peekLink is the email a login link logs in as, and until when, using
// nothing up; ok false for one that does not work.
func (t *loginCodes) peekLink(token string, now time.Time) (email string, expires time.Time, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	email, c := t.link(token, now)
	if c == nil {
		return "", time.Time{}, false
	}
	return email, c.created.Add(codeLife), true
}

// useLink is the email a login link logs in as, using it up, and with it
// the code and the other links; ok false for one that does not work.
func (t *loginCodes) useLink(token string, now time.Time) (email string, ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	email, c := t.link(token, now)
	if c == nil {
		return "", false
	}
	t.drop(email)
	return email, true
}

// noCode is what a code is compared with when the email has none, so the
// check costs the same; no typed code (digits) matches it.
const noCode = "------"

// check reports whether code is email's live code, and uses it up. A
// wrong one counts against the code: after maxAttempts it is dropped, and
// only a new one works.
func (t *loginCodes) check(email, code string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	c := t.codes[email]
	want := noCode
	if c != nil && c.live(now) {
		want = c.code
	}
	if subtle.ConstantTimeCompare([]byte(code), []byte(want)) == 1 && want != noCode {
		t.drop(email) // its links too
		return true
	}
	if c != nil {
		if c.wrong++; c.wrong >= maxAttempts || !c.live(now) {
			t.drop(email)
		}
	}
	return false
}

// gc drops the codes that ran out and the sends older than the hour, at
// most once a minute.
func (t *loginCodes) gc(now time.Time) {
	if now.Sub(t.swept) < time.Minute && !now.Before(t.swept) {
		return
	}
	t.swept = now
	for e, c := range t.codes {
		if !c.live(now) {
			t.drop(e)
		}
	}
	for e, ss := range t.sends {
		if len(ss) == 0 || now.Sub(ss[len(ss)-1]) >= time.Hour {
			delete(t.sends, e)
		}
	}
}

// loginFrom is where a login step came from, for the log.
type loginFrom struct {
	what string // "login" (a device's login page) or "web login"
	ip   string
	link string // the device's login code, shortened; empty on the Web
	// web: a login begun in a browser, for the Web version, whose mail
	// may carry a login link. A device's waits for its code: a link
	// would log a browser in, not it.
	web bool
}

func (f loginFrom) String() string {
	if f.link != "" {
		return "(code " + f.link + ", from " + f.ip + ")"
	}
	return "from " + f.ip
}

// askCode is a login's first step: someone typed an email. A code goes out
// if the email can log in; the answer is the same either way (nil, or the
// same refusal for everyone).
func (s *Server) askCode(email string, from loginFrom) error {
	if !validEmail(email) {
		return errBadEmail
	}
	now := s.now()
	if !s.asks.take(addressKey(from.ip), now) {
		s.logf("%s: too many codes asked for %s", from.what, from)
		return errTooManyAsks
	}
	_, test := s.testCodes[email]
	p := s.state.Load().w.byEmail[email]
	switch {
	case test:
		s.logf("%s: code asked for %s %s: a test account, its code is the seed's", from.what, s.logEmail(email), from)
	case p == nil && !s.cfg.OpenSignup:
		s.logf("%s: code asked for %s %s: no account, nothing sent", from.what, s.logEmail(email), from)
	default:
		// A login link goes along when the login is a browser's and the
		// Web version, which opens it, is up (-web-url).
		withLink := from.web && s.cfg.WebURL != ""
		code, link, why := s.mailed.issue(email, now, withLink)
		if code != "" {
			if link != "" {
				link = proto.LoginLinkURL(s.cfg.WebURL, link)
			}
			why = s.mailCode(email, code, link, why)
		}
		account := "new"
		if p != nil {
			account = string(p.ID)
		}
		s.logf("%s: code asked for %s %s, account %s: %s", from.what, s.logEmail(email), from, account, why)
	}
	return nil
}

// logEmail is an email as the log says it. One with an account, or a test
// account's, as it is: the operator's support needs it. One with none
// (someone mistyped, or tries emails to see which have accounts) is not
// kept as it is (PRIVACY.md): its domain, which tells a pattern (many made-up
// domains) without telling who, and a short keyed hash, which tells the
// same email asked again from the rest. The key is made at each start and
// never written down, so a hash cannot be matched to an email from the log,
// nor across restarts.
func (s *Server) logEmail(email string) string {
	_, test := s.testCodes[email]
	if test || s.state.Load().w.byEmail[email] != nil {
		return strconv.Quote(email)
	}
	h := hmac.New(sha256.New, s.logKey)
	h.Write([]byte(email))
	return fmt.Sprintf("\"…%s\" #%x", email[strings.LastIndexByte(email, '@'):], h.Sum(nil)[:6])
}

// checkCode is a login's second step: it returns whom the code logs in,
// making their account if sign-up is open and the email has none.
func (s *Server) checkCode(email, code string, from loginFrom) (proto.PersonID, error) {
	if !validEmail(email) {
		return "", errBadEmail
	}
	now := s.now()
	if !s.tries.take(email, now) {
		s.logf("%s: %s is blocked for too many wrong codes %s", from.what, s.logEmail(email), from)
		return "", errTooManyTries
	}
	addr := addressKey(from.ip)
	if !s.wrongs.take(addr, now) {
		s.tries.refund(email)
		s.logf("%s: too many wrong codes %s", from.what, from)
		return "", errTooManyTries
	}
	var ok bool
	if fixed, test := s.testCodes[email]; test {
		ok = subtle.ConstantTimeCompare([]byte(code), []byte(fixed)) == 1
	} else {
		ok = s.mailed.check(email, code, now)
	}
	if !ok {
		s.logf("%s: wrong code for %s %s", from.what, s.logEmail(email), from)
		return "", errWrongCode
	}
	s.tries.reset(email)
	s.wrongs.refund(addr)
	if p := s.state.Load().w.byEmail[email]; p != nil {
		return p.ID, nil
	}
	if !s.cfg.OpenSignup {
		return "", errSignupDisabled
	}
	return s.signUp(email)
}

// signUp makes the account of an email whose code checked out. Its name
// is the email's local part, until its owner renames it.
func (s *Server) signUp(email string) (proto.PersonID, error) {
	var id proto.PersonID
	_, err := s.change("", func(tx *sql.Tx, w *World) error {
		if p := w.byEmail[email]; p != nil {
			id = p.ID
			return errNoChange
		}
		local := email[:strings.LastIndexByte(email, '@')]
		if i := strings.IndexByte(local, '+'); i > 0 {
			local = local[:i]
		}
		display := local
		for utf8.RuneCountInString(display) > 40 {
			_, n := utf8.DecodeLastRuneInString(display)
			display = display[:len(display)-n]
		}
		taken := map[string]bool{}
		for _, p := range w.people {
			taken[p.Name] = true
		}
		base := proto.MakeLabel(local, "user")
		label := base
		for n := 2; taken[label]; n++ {
			label = base + "-" + strconv.Itoa(n)
		}
		id = proto.PersonID(newID("p"))
		_, err := tx.Exec(`INSERT INTO people (id, email, name, display_name, initial, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			id, email, label, display, initialOf(display), ms(s.now()))
		return err
	})
	if err != nil {
		return "", err
	}
	s.logf("sign-up: %s is %s", email, id)
	return id, nil
}

// The mail goes out in the background, so a step that mails a code takes
// as long as one that does not.

// mailJob is a mail waiting to go out: a code (and link), or with deleted
// the notice that an operator deleted the account at its owner's request.
type mailJob struct {
	to, code, link string
	deleted        bool
}

// startMail starts the mail queue, if the server has a mailer.
func (s *Server) startMail() {
	if s.cfg.Mailer == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mailQ = make(chan mailJob, mailQueueSize)
	s.mailStop = cancel
	s.mailDone = make(chan struct{})
	go func() {
		defer close(s.mailDone)
		for {
			select {
			case <-ctx.Done():
				return
			case j := <-s.mailQ:
				sctx, done := context.WithTimeout(ctx, mailTimeout)
				if j.deleted {
					// The account is gone: its email is logged as one
					// with none is.
					if err := s.cfg.Mailer.SendAccountDeleted(sctx, j.to); err != nil {
						s.logf("mail: the notice that the account of %s was deleted did not go out: %v", s.logEmail(j.to), err)
					} else {
						s.logf("mail: the notice that the account was deleted went to %s", s.logEmail(j.to))
					}
					done()
					continue
				}
				err := s.cfg.Mailer.SendCode(sctx, j.to, j.code, j.link)
				done()
				if err != nil {
					s.logf("mail: the login code for %s did not go out: %v", j.to, err)
				} else {
					s.logf("mail: a login code went to %s", j.to)
				}
			}
		}
	}()
}

// stopMail stops the queue; mail still waiting is dropped.
func (s *Server) stopMail() {
	if s.mailStop != nil {
		s.mailStop()
		<-s.mailDone
	}
}

// mailCode queues the code (and link) for email and says what became of it.
func (s *Server) mailCode(email, code, link, why string) string {
	if s.mailQ == nil {
		return "no mailer is set up, nothing sent"
	}
	select {
	case s.mailQ <- mailJob{to: email, code: code, link: link}:
		if link != "" {
			why += ", with a login link"
		}
		return why
	default:
		return "the mail queue is full, nothing sent"
	}
}

// mailDeleted queues the notice that an operator deleted the account of
// email at its owner's request. The account is gone already: a notice
// that does not go out is logged, and that is all.
func (s *Server) mailDeleted(email string) {
	if s.mailQ == nil {
		s.logf("mail: no mailer is set up: the notice that the account of %s was deleted did not go out", s.logEmail(email))
		return
	}
	select {
	case s.mailQ <- mailJob{to: email, deleted: true}:
	default:
		s.logf("mail: the mail queue is full: the notice that the account of %s was deleted did not go out", s.logEmail(email))
	}
}

// Logging in by the link in the mail (design/plan/m7/login-link.md): the
// link's page asks first (peekLink), then logs in (useLink). Both count a
// link that does not work against the address, as a wrong code is.

// errDeadLink is the answer for any link that does not work: used, run
// out, made old by a newer code, or never made.
var errDeadLink = errNotFound("这个登录链接用不了了：已经用过、过期了，或者后来又发了新的验证码。回到登录页再发一封就行。")

// peekLink is what a login link would log in as.
func (s *Server) peekLink(token string, from loginFrom) (string, time.Time, error) {
	now := s.now()
	addr := addressKey(from.ip)
	if !s.wrongs.take(addr, now) {
		s.logf("%s: too many wrong codes or links %s", from.what, from)
		return "", time.Time{}, errTooManyTries
	}
	email, expires, ok := s.mailed.peekLink(token, now)
	if !ok {
		s.logf("%s: a login link that does not work, looked at %s", from.what, from)
		return "", time.Time{}, errDeadLink
	}
	s.wrongs.refund(addr)
	return email, expires, nil
}

// useLink logs in with a login link, as checkCode with the code: it
// returns whom the link logs in, making their account if sign-up is open
// and the email has none.
func (s *Server) useLink(token string, from loginFrom) (proto.PersonID, string, error) {
	now := s.now()
	addr := addressKey(from.ip)
	if !s.wrongs.take(addr, now) {
		s.logf("%s: too many wrong codes or links %s", from.what, from)
		return "", "", errTooManyTries
	}
	email, ok := s.mailed.useLink(token, now)
	if !ok {
		s.logf("%s: a login link that does not work, used %s", from.what, from)
		return "", "", errDeadLink
	}
	s.wrongs.refund(addr)
	s.tries.reset(email)
	if p := s.state.Load().w.byEmail[email]; p != nil {
		return p.ID, email, nil
	}
	if !s.cfg.OpenSignup {
		return "", "", errDeadLink // sign-up closed since the mail went out
	}
	id, err := s.signUp(email)
	return id, email, err
}
