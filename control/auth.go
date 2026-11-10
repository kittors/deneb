package control

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kittors/deneb/proto"
)

// Tokens and challenges are stateless: each is a payload and an HMAC under
// the deployment's secret, which lives in the database. They survive a
// restart, and the server keeps no table of them.
type keyring struct {
	secret []byte

	mu    sync.Mutex
	used  map[[16]byte]time.Time // challenges answered, until they expire
	swept time.Time              // when used was last rid of expired ones
}

const (
	challengeTTL = time.Minute
	tokenTTL     = time.Hour
)

var b64 = base64.RawURLEncoding

func newKeyring(secret []byte) *keyring {
	return &keyring{secret: secret, used: map[[16]byte]time.Time{}}
}

// mac signs parts under purpose; length prefixes keep two different splits
// of the same bytes from signing the same.
func (k *keyring) mac(purpose string, parts ...[]byte) []byte {
	h := hmac.New(sha256.New, k.secret)
	for _, p := range append([][]byte{[]byte(purpose)}, parts...) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(p)))
		h.Write(n[:])
		h.Write(p)
	}
	return h.Sum(nil)
}

func unixBytes(t time.Time) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(t.Unix()))
	return b[:]
}

// newChallenge is 16 random bytes, an expiry, and a MAC tying both to the
// machine key that asked.
func (k *keyring) newChallenge(mk proto.MachinePublic, now time.Time) ([]byte, time.Time) {
	exp := now.Add(challengeTTL)
	c := make([]byte, 16, 40)
	rand.Read(c)
	c = append(c, unixBytes(exp)...)
	return append(c, k.mac("challenge", mk[:], c)[:16]...), exp
}

var errBadChallenge = errors.New("bad or expired challenge")

// checkChallenge reports whether c is a challenge this server made for
// mk that has not run out. It writes nothing: only an answer whose
// signature checks out is then marked used (useChallenge), so anyone may
// ask for challenges and send garbage without filling the table.
func (k *keyring) checkChallenge(mk proto.MachinePublic, c []byte, now time.Time) error {
	if len(c) != 40 || !hmac.Equal(c[24:], k.mac("challenge", mk[:], c[:24])[:16]) {
		return errBadChallenge
	}
	if !now.Before(challengeExpiry(c)) {
		return errBadChallenge
	}
	return nil
}

func challengeExpiry(c []byte) time.Time {
	return time.Unix(int64(binary.BigEndian.Uint64(c[16:24])), 0)
}

// useChallenge checks a challenge and marks it answered, so a recorded
// answer cannot be played again. Call it once the signature checked out.
func (k *keyring) useChallenge(mk proto.MachinePublic, c []byte, now time.Time) error {
	if err := k.checkChallenge(mk, c, now); err != nil {
		return err
	}
	var id [16]byte
	copy(id[:], c)
	k.mu.Lock()
	defer k.mu.Unlock()
	// Forget the answers that ran out, at most once a challenge's life:
	// sweeping on every answer would cost the whole table each time.
	if now.Sub(k.swept) >= challengeTTL {
		k.swept = now
		for u, until := range k.used {
			if !now.Before(until) {
				delete(k.used, u)
			}
		}
	}
	if _, ok := k.used[id]; ok {
		return errBadChallenge
	}
	k.used[id] = challengeExpiry(c)
	return nil
}

// newToken is "d1.<machine key and expiry>.<mac>".
func (k *keyring) newToken(mk proto.MachinePublic, now time.Time) (string, time.Time) {
	exp := now.Add(tokenTTL)
	payload := append(mk[:], unixBytes(exp)...)
	return "d1." + b64.EncodeToString(payload) + "." + b64.EncodeToString(k.mac("token", payload)), exp
}

var errBadToken = errors.New("bad or expired token")

func (k *keyring) checkToken(tok string, now time.Time) (proto.MachinePublic, error) {
	var mk proto.MachinePublic
	payload, err := k.open("d1.", "token", tok)
	if err != nil || len(payload) != 40 {
		return mk, errBadToken
	}
	if !now.Before(time.Unix(int64(binary.BigEndian.Uint64(payload[32:])), 0)) {
		return mk, errBadToken
	}
	copy(mk[:], payload)
	return mk, nil
}

func (k *keyring) open(prefix, purpose, s string) ([]byte, error) {
	rest, ok := strings.CutPrefix(s, prefix)
	if !ok {
		return nil, errBadToken
	}
	p, m, ok := strings.Cut(rest, ".")
	if !ok {
		return nil, errBadToken
	}
	payload, err1 := b64.DecodeString(p)
	sum, err2 := b64.DecodeString(m)
	if err1 != nil || err2 != nil || !hmac.Equal(sum, k.mac(purpose, payload)) {
		return nil, errBadToken
	}
	return payload, nil
}

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// randomCode is n letters of base32 (A–Z, 2–7): easy to read aloud, and
// nothing in it needs escaping in a URL.
func randomCode(n int) string {
	b := make([]byte, (n*5+7)/8)
	rand.Read(b)
	return b32.EncodeToString(b)[:n]
}

// newID is an id with a kind prefix: "p_", "l_", "d_", "i_".
func newID(prefix string) string {
	return prefix + "_" + strings.ToLower(randomCode(10))
}

// attempts limits tries of a verification code per email, so a six-digit
// code cannot be guessed on a public server.
type attempts struct {
	mu    sync.Mutex
	tries map[string][]time.Time
	swept time.Time
}

const (
	maxAttempts    = 5
	attemptsWindow = 15 * time.Minute
)

// take counts a try at key and reports whether it may go ahead: at most
// maxAttempts in attemptsWindow. Checking and counting are one step under
// the lock, so guesses sent all at once cannot each pass the check before
// any is counted (M7 review). A right code calls reset, so only wrong ones
// add up.
func (a *attempts) take(key string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tries == nil {
		a.tries = map[string][]time.Time{}
	}
	// Emails nobody tried lately go, so made-up ones do not pile up.
	if now.Sub(a.swept) >= attemptsWindow {
		a.swept = now
		for k := range a.tries {
			if len(a.recent(k, now)) == 0 {
				delete(a.tries, k)
			}
		}
	}
	recent := a.recent(key, now)
	if len(recent) >= maxAttempts {
		return false
	}
	a.tries[key] = append(recent, now)
	return true
}

func (a *attempts) reset(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.tries, key)
}

func (a *attempts) recent(key string, now time.Time) []time.Time {
	var out []time.Time
	for _, t := range a.tries[key] {
		if now.Sub(t) < attemptsWindow {
			out = append(out, t)
		}
	}
	return out
}

// pendingLogin is a device waiting for someone to log in on it in a
// browser. They live in memory: after a restart the engine asks again and
// gets a new link.
type pendingLogin struct {
	code    string
	machine proto.MachinePublic
	from    string // the address that first asked, for the per-address limit
	created time.Time
	expires time.Time
	done    chan struct{} // closed once someone logged in on it
	// facts are what the engine said last. A second ask replaces them
	// while a browser may be reading them, so they are swapped whole.
	facts atomic.Pointer[loginFacts]
}

// loginFacts are what an engine asking to log in says, and where from.
type loginFacts struct {
	req      proto.LoginRequest
	remoteIP string
}

// loginTTL is how long a login link works. An hour leaves room to log in
// several machines one after another; the engine asks for a new link when
// one runs out.
const loginTTL = time.Hour

// The logins waiting for a browser are bounded: each holds memory for an
// hour, and asking for one takes only a machine key, which anyone can
// make (M7 review). A household, an office or a phone network's shared
// address has a few at a time.
const (
	maxPendingLogins = 20000
	maxPendingPerIP  = 50
)

var errTooManyLogins = newErr(http.StatusTooManyRequests, proto.ErrConflict, "等着登录的设备太多了，请过一会儿再试")

type loginTable struct {
	mu        sync.Mutex
	byCode    map[string]*pendingLogin
	byMachine map[proto.MachinePublic]*pendingLogin
	perIP     map[string]int
	swept     time.Time
}

func newLoginTable() *loginTable {
	return &loginTable{byCode: map[string]*pendingLogin{}, byMachine: map[proto.MachinePublic]*pendingLogin{}, perIP: map[string]int{}}
}

// start returns the machine's pending login, making one (fresh) if it has
// none or its link expired. A second ask keeps the link and takes the newer
// facts.
func (t *loginTable) start(mk proto.MachinePublic, req proto.LoginRequest, ip string, now time.Time) (pl *pendingLogin, fresh bool, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gc(now)
	if old := t.byMachine[mk]; old != nil && now.Before(old.expires) {
		old.facts.Store(&loginFacts{req: req, remoteIP: ip})
		return old, false, nil
	}
	if len(t.byCode) >= maxPendingLogins || t.perIP[ip] >= maxPendingPerIP {
		return nil, false, errTooManyLogins
	}
	pl = &pendingLogin{
		code:    randomCode(12),
		machine: mk,
		from:    ip,
		created: now,
		expires: now.Add(loginTTL),
		done:    make(chan struct{}),
	}
	pl.facts.Store(&loginFacts{req: req, remoteIP: ip})
	if old := t.byMachine[mk]; old != nil {
		t.drop(old)
	}
	t.byCode[pl.code] = pl
	t.byMachine[mk] = pl
	t.perIP[ip]++
	return pl, true, nil
}

func (t *loginTable) byLoginCode(code string, now time.Time) *pendingLogin {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gc(now)
	if pl := t.byCode[strings.ToUpper(strings.TrimSpace(code))]; pl != nil && now.Before(pl.expires) {
		return pl
	}
	return nil
}

func (t *loginTable) forMachine(mk proto.MachinePublic, now time.Time) *pendingLogin {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gc(now)
	if pl := t.byMachine[mk]; pl != nil && now.Before(pl.expires) {
		return pl
	}
	return nil
}

// finish drops a login someone completed and wakes whoever waits on it.
func (t *loginTable) finish(pl *pendingLogin) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byCode[pl.code] == pl {
		t.drop(pl)
		close(pl.done)
	}
}

// finishMachine ends the machine's pending login, if it has one, and wakes
// whoever waits on it: the device came back some other way (its owner
// renewed it).
func (t *loginTable) finishMachine(mk proto.MachinePublic) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if pl := t.byMachine[mk]; pl != nil {
		t.drop(pl)
		close(pl.done)
	}
}

// drop takes pl out of the table.
func (t *loginTable) drop(pl *pendingLogin) {
	delete(t.byCode, pl.code)
	if t.byMachine[pl.machine] == pl {
		delete(t.byMachine, pl.machine)
	}
	if t.perIP[pl.from]--; t.perIP[pl.from] <= 0 {
		delete(t.perIP, pl.from)
	}
}

// gc drops the logins that ran out, at most every few seconds: the
// lookups check the expiry themselves, and a sweep costs the whole table.
func (t *loginTable) gc(now time.Time) {
	if now.Sub(t.swept) < 10*time.Second && !now.Before(t.swept) {
		return
	}
	t.swept = now
	for _, pl := range t.byCode {
		if !now.Before(pl.expires) {
			t.drop(pl)
		}
	}
}
