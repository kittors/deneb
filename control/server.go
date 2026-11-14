// Package control is the control server (implementation.md §2.2): accounts,
// LANs, members, invites, devices and their addresses, services and who may
// use them, relays. It computes each device's network map and each person's
// view, and pushes them over the device interface. It never touches
// traffic and never holds a device's private key.
package control

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kittors/deneb/control/alert"
	"github.com/kittors/deneb/proto"
)

// Config is how a server is set up.
type Config struct {
	// DataDir holds the database, the TLS key and the backups.
	DataDir string
	// PublicURL is how devices and browsers reach the server, such as
	// https://103.42.31.155:47443. Login and invite links start with it.
	PublicURL string
	// WebURL is the Web version's address (deneb-web behind a certificate
	// browsers trust, design/plan/m7/web-deploy.md), such as
	// https://deneb.example.com. Set, invite links open it rather than this
	// server, this server's own invite links redirect there, and its
	// browser API takes requests from it as its own. Empty, as before.
	WebURL string
	// TrustProxies are where deneb-web runs, behind nginx: a request from
	// one of them takes its address from X-Real-IP, the address nginx saw,
	// which deneb-web passes on. Without it every browser behind them looks
	// like that machine, and shares the per-address limits on login codes.
	// From anywhere else the header counts for nothing.
	TrustProxies []netip.Prefix
	// Dev turns on what only a development server may have: POST
	// /dev/approve, which completes a device login without a browser.
	Dev bool
	// Seed has the accounts, the test accounts' fixed codes and the
	// relays.
	Seed Seed
	// Mailer mails the login codes (M7); nil mails none, and only the
	// seed's test accounts, with their fixed codes, can log in.
	Mailer Mailer
	// LogDir is where the log files are (LogFiles), for deneb-control
	// logs; empty when the log goes to the standard error only.
	LogDir string
	// Alerts is where the server's alerts go (design/plan/m7/control-ops.md
	// §3); nil logs them only. Name is who they say they are from; empty,
	// the public URL's host.
	Alerts alert.Sender
	Name   string
	// OpenSignup lets an email with no account log in: it gets a code, and
	// the account is made when the code checks out. Off, only the seed's
	// accounts and those made before can log in; either way the answers do
	// not tell who has an account.
	OpenSignup bool
	// LoginLifetime is how long a device's login lasts without the device
	// coming back; zero means DefaultLoginLifetime. Test deployments make
	// it short to see logins run out.
	LoginLifetime time.Duration
	// SeatLimit is how many people each person's LANs and shares may hold,
	// themselves included (pricing-and-relays §1.4). Zero means
	// DefaultSeatLimit; below zero, no limit (a deployment of one's own).
	SeatLimit int
	// Pin is the server's own certificate pin, which relay join codes
	// carry so a relay trusts it (M4); empty for plain HTTP.
	Pin string
	// RelayImage is the Docker image a relay's install command runs;
	// empty means DefaultRelayImage.
	RelayImage string
	// Now is the clock; nil means time.Now. Tests set it.
	Now func() time.Time
	// Logf logs; nil means log.Printf.
	Logf func(format string, args ...any)
}

// DefaultLoginLifetime is how long a login lasts, as Tailscale's default
// key expiry: a device that keeps coming back never reaches it.
const DefaultLoginLifetime = 180 * 24 * time.Hour

// DefaultRelayImage is the relay image an install command runs, until
// there is a public registry: built on the server with
// deploy/relay/build.sh.
const DefaultRelayImage = "deneb-relay:v0"

// DefaultSeatLimit is the free seats per person (pricing-and-relays §1.4).
const DefaultSeatLimit = 6

// randomKey is 32 random bytes.
func randomKey() []byte {
	b := make([]byte, 32)
	rand.Read(b)
	return b
}

// Server is a control server. Its Handler serves every interface.
type Server struct {
	cfg  Config
	db   *sql.DB
	keys *keyring
	hub  *hub
	// started is when New made it: the console's "已经运行 30 天".
	started time.Time

	logins *loginTable
	// testCodes are the seed's test accounts' fixed codes, by email; every
	// other login gets a code by mail (mailed).
	testCodes map[string]string
	mailed    *loginCodes
	tries     attempts // wrong codes per email
	wrongs    attempts // wrong codes per address
	asks      attempts // codes asked for per address
	mailQ     chan mailJob
	mailStop  context.CancelFunc
	mailDone  chan struct{}

	alerts *alert.Alerter
	health *health
	// logKey keys the hashes the log writes for emails with no account
	// (logEmail); made at each start, never stored.
	logKey []byte

	// presence gathers devices coming and going (markOnline);
	// presenceDelay is how long (tests shorten it).
	presence      presence
	presenceDelay time.Duration

	// mu serializes changes. state is swapped whole after each one, so
	// readers never lock.
	mu    sync.Mutex
	state atomic.Pointer[state]
	ops   opCache // guarded by mu

	sweeps sweeper
	// relayLive is which relays checked in lately (M4).
	relayLive     *relayLive
	deviceSamples deviceSamples
	// versions wakes the relays' watches at every change (M4).
	versions *versionWaiter
	// keepAlive is how often a stream sends a keep-alive frame
	// (proto.StreamKeepAlive; tests make it short).
	keepAlive time.Duration

	mux *http.ServeMux
}

// state is the world and the version it is at.
type state struct {
	w *World
	v uint64
}

// New opens (or creates) the server's data in cfg.DataDir and applies the
// seed.
func New(cfg Config) (*Server, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.LoginLifetime <= 0 {
		cfg.LoginLifetime = DefaultLoginLifetime
	}
	if cfg.SeatLimit == 0 {
		cfg.SeatLimit = DefaultSeatLimit
	}
	if cfg.RelayImage == "" {
		cfg.RelayImage = DefaultRelayImage
	}
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	if cfg.PublicURL == "" {
		return nil, errors.New("control: PublicURL is required")
	}
	cfg.WebURL = strings.TrimRight(cfg.WebURL, "/")
	if cfg.WebURL != "" {
		u, err := url.Parse(cfg.WebURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
			return nil, fmt.Errorf("control: WebURL %q: want https://host, with no path", cfg.WebURL)
		}
	}
	db, err := openDB(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	// The data directory holds the signing key, the TLS key and the
	// backups: no one but the server may read it (M7 review). The files in
	// it are made without regard to that; the directory's mode guards them.
	if fi, err := os.Stat(cfg.DataDir); err == nil && fi.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(cfg.DataDir, 0o700); err != nil {
			db.Close()
			return nil, fmt.Errorf("data directory %s is open to others (%v) and cannot be closed: %w", cfg.DataDir, fi.Mode().Perm(), err)
		}
		cfg.Logf("data directory %s was open to others (%v): now 0700", cfg.DataDir, fi.Mode().Perm())
	}
	s := &Server{
		cfg:       cfg,
		db:        db,
		hub:       newHub(cfg.Now),
		logins:    newLoginTable(),
		testCodes: map[string]string{},
		mailed:    newLoginCodes(),
		wrongs:    attempts{limit: maxWrongPerAddress},
		asks:      attempts{limit: maxAsksPerAddress, window: time.Hour},
		health:    newHealth(),

		presenceDelay: defaultPresenceDelay,
		logKey:        randomKey(),
		keepAlive:     proto.StreamKeepAlive,
		relayLive:     newRelayLive(),
		versions:      newVersionWaiter(),
	}
	s.started = s.now()
	name := cfg.Name
	if name == "" {
		if u, err := url.Parse(cfg.PublicURL); err == nil {
			name = u.Hostname()
		}
	}
	s.alerts = &alert.Alerter{Source: "deneb-control（" + name + "）", Sender: cfg.Alerts, Logf: cfg.Logf, Now: cfg.Now}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	s.routes()
	s.startMail()
	return s, nil
}

// init reads or makes the deployment's settings, applies the seed and
// loads the world.
func (s *Server) init() error {
	secretHex, ok, err := getSetting(s.db, "secret")
	if err != nil {
		return err
	}
	if !ok {
		b := make([]byte, 32)
		rand.Read(b)
		secretHex = hex.EncodeToString(b)
		if err := setSetting(s.db, "secret", secretHex); err != nil {
			return err
		}
	}
	secret, err := hex.DecodeString(secretHex)
	if err != nil {
		return fmt.Errorf("settings.secret: %w", err)
	}
	s.keys = newKeyring(secret)

	var network proto.Network
	netJSON, ok, err := getSetting(s.db, "network")
	if err != nil {
		return err
	}
	if ok {
		if err := json.Unmarshal([]byte(netJSON), &network); err != nil {
			return fmt.Errorf("settings.network: %w", err)
		}
	} else {
		// Picked once, at the first start: changing it later would
		// renumber every device.
		if network, err = newNetwork(s.cfg.Seed.Network.IPv4, s.cfg.Seed.Network.IPv6); err != nil {
			return err
		}
		b, _ := json.Marshal(network)
		if err := setSetting(s.db, "network", string(b)); err != nil {
			return err
		}
	}

	v, err := getVersion(s.db)
	if err != nil {
		return err
	}
	// Devices from before logins expired (M1) start their lifetime now.
	if _, err := s.db.Exec(`UPDATE devices SET expires_at = ? WHERE expires_at = 0`, ms(s.now().Add(s.cfg.LoginLifetime))); err != nil {
		return err
	}
	w, err := s.load(s.db, network)
	if err != nil {
		return err
	}
	s.state.Store(&state{w: w, v: v})
	s.sweeps.last = s.now()
	for id, m := range w.relayMeta {
		if m.SecretHash != "" {
			s.relayLive.presume(id, s.now())
		}
	}
	return s.applySeed()
}

// load reads the world at the server's now.
func (s *Server) load(q querier, network proto.Network) (*World, error) {
	w, err := loadWorld(q, network, s.inviteBase(), s.now())
	if err != nil {
		return nil, err
	}
	// "需要重新登录" shows two weeks ahead, or a quarter of a short test
	// lifetime ahead.
	w.renewWindow = min(14*24*time.Hour, s.cfg.LoginLifetime/4)
	w.seatLimit = max(s.cfg.SeatLimit, 0)
	w.relayStatus = s.relayLive.status
	w.relayOffline, w.relayFull = s.relayLive.offline, s.relayLive.isFull
	return w, nil
}

// Close closes the database. Streams end with their requests.
func (s *Server) Close() error {
	s.stopPresence()
	s.stopMail()
	return s.db.Close()
}

// Network is the deployment's address space.
func (s *Server) Network() proto.Network { return s.state.Load().w.Network }

// Version is the current version.
func (s *Server) Version() uint64 { return s.state.Load().v }

func (s *Server) now() time.Time { return s.cfg.Now() }

func (s *Server) logf(format string, args ...any) { s.cfg.Logf(format, args...) }

// errNoChange tells change that the request found nothing to do: it gets
// the current version, and nothing is published.
var errNoChange = errors.New("no change")

// change runs fn in a transaction against the current world. If fn
// succeeds, the version goes up by one, the world is reloaded and every
// stream gets its new frame. force names a device that hears of the new
// version even if its frame did not change.
func (s *Server) change(force proto.DeviceID, fn func(tx *sql.Tx, w *World) error) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.changeLocked(force, fn)
}

func (s *Server) changeLocked(force proto.DeviceID, fn func(tx *sql.Tx, w *World) error) (uint64, error) {
	st := s.state.Load()
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := fn(tx, st.w); err != nil {
		if errors.Is(err, errNoChange) {
			return st.v, nil
		}
		return 0, err
	}
	v := st.v + 1
	if err := setSetting(tx, "version", strconv.FormatUint(v, 10)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	w, err := s.load(s.db, st.w.Network)
	if err != nil {
		// The change is in the database but not in memory. Keep the
		// version, so the next change (or a restart) catches up.
		s.state.Store(&state{w: st.w, v: v})
		return 0, fmt.Errorf("reload after change: %w", err)
	}
	s.state.Store(&state{w: w, v: v})
	s.hub.publish(w, v, force)
	s.versions.changed()
	return v, nil
}

// apiError is a refusal with its HTTP status.
type apiError struct {
	status int
	body   proto.Error
}

func (e *apiError) Error() string { return e.body.Error() }

func newErr(status int, code proto.ErrorCode, msg string) *apiError {
	return &apiError{status: status, body: proto.Error{Code: code, Message: msg}}
}

func errInvalid(msg string) *apiError   { return newErr(http.StatusBadRequest, proto.ErrInvalid, msg) }
func errForbidden(msg string) *apiError { return newErr(http.StatusForbidden, proto.ErrForbidden, msg) }
func errNotFound(msg string) *apiError  { return newErr(http.StatusNotFound, proto.ErrNotFound, msg) }
func errConflict(msg string) *apiError  { return newErr(http.StatusConflict, proto.ErrConflict, msg) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeErr answers with err: a refusal as it is, anything else as an
// internal error, logged here and not shown.
func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	if !errors.As(err, &ae) {
		s.logf("%s %s: %v", r.Method, logPath(r.URL.Path), err)
		ae = newErr(http.StatusInternalServerError, proto.ErrInternal, "服务器出了点问题，请稍后再试")
	}
	writeJSON(w, ae.status, proto.ErrorBody{Error: &ae.body})
}

// logPath is a request's path for the log, less what is a secret in it:
// an invite link's token (M7 review). Query strings, where a login link's
// code is, never go into the log.
func logPath(p string) string {
	for _, prefix := range []string{proto.InvitePath, proto.PathWebInvites} {
		if rest, ok := strings.CutPrefix(p, prefix); ok && rest != "" {
			return prefix + "…"
		}
	}
	return p
}

const maxBody = 1 << 20

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(v); err != nil {
		return errInvalid("请求的格式不对：" + err.Error())
	}
	return nil
}

func (s *Server) routes() {
	m := http.NewServeMux()
	m.HandleFunc("POST "+proto.PathAuthChallenge, s.handleChallenge)
	m.HandleFunc("POST "+proto.PathAuthToken, s.handleToken)
	m.HandleFunc("POST "+proto.PathLogin, s.device(s.handleLogin, false))
	m.HandleFunc("POST "+proto.PathLoginWait, s.device(s.handleLoginWait, false))
	m.HandleFunc("POST "+proto.PathLogout, s.device(s.handleLogout, true))
	m.HandleFunc("GET "+proto.PathStream, s.device(s.handleStream, true))
	m.HandleFunc("GET "+proto.PathDeviceMetrics, s.device(s.handleDeviceMetrics, true))
	m.HandleFunc("POST "+proto.PathDeviceMetrics, s.device(s.handleDeviceMetrics, true))
	m.HandleFunc("POST "+proto.PathReport, s.device(s.handleReport, true))
	m.HandleFunc("POST "+proto.PathOps, s.device(s.handleOps, true))
	m.HandleFunc("GET "+proto.PathUserView, s.device(s.handleUserView, true))
	m.HandleFunc("POST "+proto.PathInvite, s.device(s.handleInvite, true))
	m.HandleFunc("GET "+proto.PathRelayUsage, s.device(s.handleRelayUsage, true))
	m.HandleFunc("/device/", func(w http.ResponseWriter, r *http.Request) {
		s.writeErr(w, r, errNotFound("没有这个接口"))
	})
	s.relayRoutes(m)
	s.webAPIRoutes(m)
	s.webRoutes(m)
	if s.cfg.Dev {
		m.HandleFunc("POST /dev/approve", s.handleDevApprove)
	}
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.Version()})
	})
	s.mux = m
}

// Handler serves the device interface, the two web pages and /healthz.
func (s *Server) Handler() http.Handler {
	if len(s.cfg.TrustProxies) > 0 {
		return trustProxies(s.mux, s.cfg.TrustProxies)
	}
	return s.mux
}

// inviteBase is where invite links point: the Web version, if there is
// one, else this server.
func (s *Server) inviteBase() string {
	if s.cfg.WebURL != "" {
		return s.cfg.WebURL
	}
	return s.cfg.PublicURL
}

// trustProxies takes a request's address from X-Real-IP when the request
// comes from one of proxies: deneb-web, passing on what nginx saw. From
// anywhere else the header counts for nothing, so no one else can choose
// the address the limits and the log see.
func trustProxies(next http.Handler, proxies []netip.Prefix) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("X-Real-IP"); v != "" {
			peer, err := netip.ParseAddrPort(r.RemoteAddr)
			from, err2 := netip.ParseAddr(strings.TrimSpace(v))
			if err == nil && err2 == nil && slices.ContainsFunc(proxies, func(p netip.Prefix) bool { return p.Contains(peer.Addr().Unmap()) }) {
				r = r.WithContext(r.Context())
				r.RemoteAddr = netip.AddrPortFrom(from.Unmap(), 0).String()
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ParseProxies reads -trust-proxy: addresses or prefixes, comma-separated,
// as 198.51.100.4,127.0.0.1 or 2001:db8::/64.
func ParseProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if p, err := netip.ParsePrefix(f); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, fmt.Errorf("-trust-proxy: %q is no address", f)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}
