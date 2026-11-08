package control

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/proto"
)

// Tokens and challenges are stateless: each is a payload and an HMAC under
// the deployment's secret, which lives in the database. They survive a
// restart, and the server keeps no table of them.
type keyring struct {
	secret []byte

	mu   sync.Mutex
	used map[[16]byte]time.Time // challenges answered, until they expire
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

// useChallenge checks a challenge and marks it answered, so a recorded
// answer cannot be played again.
func (k *keyring) useChallenge(mk proto.MachinePublic, c []byte, now time.Time) error {
	if len(c) != 40 || !hmac.Equal(c[24:], k.mac("challenge", mk[:], c[:24])[:16]) {
		return errBadChallenge
	}
	exp := time.Unix(int64(binary.BigEndian.Uint64(c[16:24])), 0)
	if !now.Before(exp) {
		return errBadChallenge
	}
	var id [16]byte
	copy(id[:], c)
	k.mu.Lock()
	defer k.mu.Unlock()
	for u, until := range k.used {
		if !now.Before(until) {
			delete(k.used, u)
		}
	}
	if _, ok := k.used[id]; ok {
		return errBadChallenge
	}
	k.used[id] = exp
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

// attempts limits wrong verification codes per email, so a six-digit code
// cannot be guessed on a public server.
type attempts struct {
	mu   sync.Mutex
	fail map[string][]time.Time
}

const (
	maxAttempts    = 5
	attemptsWindow = 15 * time.Minute
)

func (a *attempts) blocked(key string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.recent(key, now)) >= maxAttempts
}

func (a *attempts) failed(key string, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail == nil {
		a.fail = map[string][]time.Time{}
	}
	a.fail[key] = append(a.recent(key, now), now)
}

func (a *attempts) reset(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.fail, key)
}

func (a *attempts) recent(key string, now time.Time) []time.Time {
	var out []time.Time
	for _, t := range a.fail[key] {
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
	code     string
	machine  proto.MachinePublic
	req      proto.LoginRequest
	remoteIP string
	created  time.Time
	expires  time.Time
	done     chan struct{} // closed once someone logged in on it
}

// loginTTL is how long a login link works. An hour leaves room to log in
// several machines one after another; the engine asks for a new link when
// one runs out.
const loginTTL = time.Hour

type loginTable struct {
	mu        sync.Mutex
	byCode    map[string]*pendingLogin
	byMachine map[proto.MachinePublic]*pendingLogin
}

func newLoginTable() *loginTable {
	return &loginTable{byCode: map[string]*pendingLogin{}, byMachine: map[proto.MachinePublic]*pendingLogin{}}
}

// start returns the machine's pending login, making one (fresh) if it has
// none or its link expired. A second ask keeps the link and takes the newer
// facts.
func (t *loginTable) start(mk proto.MachinePublic, req proto.LoginRequest, ip string, now time.Time) (pl *pendingLogin, fresh bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gc(now)
	if old := t.byMachine[mk]; old != nil {
		old.req, old.remoteIP = req, ip
		return old, false
	}
	pl = &pendingLogin{
		code:     randomCode(12),
		machine:  mk,
		req:      req,
		remoteIP: ip,
		created:  now,
		expires:  now.Add(loginTTL),
		done:     make(chan struct{}),
	}
	t.byCode[pl.code] = pl
	t.byMachine[mk] = pl
	return pl, true
}

func (t *loginTable) byLoginCode(code string, now time.Time) *pendingLogin {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gc(now)
	return t.byCode[strings.ToUpper(strings.TrimSpace(code))]
}

func (t *loginTable) forMachine(mk proto.MachinePublic, now time.Time) *pendingLogin {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gc(now)
	return t.byMachine[mk]
}

// finish drops a login someone completed and wakes whoever waits on it.
func (t *loginTable) finish(pl *pendingLogin) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byCode[pl.code] == pl {
		delete(t.byCode, pl.code)
		delete(t.byMachine, pl.machine)
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
		delete(t.byCode, pl.code)
		delete(t.byMachine, mk)
		close(pl.done)
	}
}

func (t *loginTable) gc(now time.Time) {
	for code, pl := range t.byCode {
		if !now.Before(pl.expires) {
			delete(t.byCode, code)
			if t.byMachine[pl.machine] == pl {
				delete(t.byMachine, pl.machine)
			}
		}
	}
}
