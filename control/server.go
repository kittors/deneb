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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kittors/deneb/proto"
)

// Config is how a server is set up.
type Config struct {
	// DataDir holds the database, the TLS key and the backups.
	DataDir string
	// PublicURL is how devices and browsers reach the server, such as
	// https://103.42.31.155:47443. Login and invite links start with it.
	PublicURL string
	// Dev turns on what only a development server may have: POST
	// /dev/approve, which completes a device login without a browser.
	Dev bool
	// Seed has the test accounts, their codes and the relays.
	Seed Seed
	// LoginLifetime is how long a device's login lasts without the device
	// coming back; zero means DefaultLoginLifetime. Test deployments make
	// it short to see logins run out.
	LoginLifetime time.Duration
	// Now is the clock; nil means time.Now. Tests set it.
	Now func() time.Time
	// Logf logs; nil means log.Printf.
	Logf func(format string, args ...any)
}

// DefaultLoginLifetime is how long a login lasts, as Tailscale's default
// key expiry: a device that keeps coming back never reaches it.
const DefaultLoginLifetime = 180 * 24 * time.Hour

// Server is a control server. Its Handler serves every interface.
type Server struct {
	cfg  Config
	db   *sql.DB
	keys *keyring
	hub  *hub

	logins *loginTable
	tries  attempts
	codes  map[string]string // verification codes from the seed, by email

	// mu serializes changes. state is swapped whole after each one, so
	// readers never lock.
	mu    sync.Mutex
	state atomic.Pointer[state]
	ops   opCache // guarded by mu

	sweeps sweeper
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
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	if cfg.PublicURL == "" {
		return nil, errors.New("control: PublicURL is required")
	}
	db, err := openDB(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:       cfg,
		db:        db,
		hub:       newHub(cfg.Now),
		logins:    newLoginTable(),
		codes:     map[string]string{},
		keepAlive: proto.StreamKeepAlive,
	}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	s.routes()
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
	return s.applySeed()
}

// load reads the world at the server's now.
func (s *Server) load(q querier, network proto.Network) (*World, error) {
	w, err := loadWorld(q, network, s.cfg.PublicURL, s.now())
	if err != nil {
		return nil, err
	}
	// "需要重新登录" shows two weeks ahead, or a quarter of a short test
	// lifetime ahead.
	w.renewWindow = min(14*24*time.Hour, s.cfg.LoginLifetime/4)
	return w, nil
}

// Close closes the database. Streams end with their requests.
func (s *Server) Close() error { return s.db.Close() }

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
		s.logf("%s %s: %v", r.Method, r.URL.Path, err)
		ae = newErr(http.StatusInternalServerError, proto.ErrInternal, "服务器出了点问题，请稍后再试")
	}
	writeJSON(w, ae.status, proto.ErrorBody{Error: &ae.body})
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
	m.HandleFunc("POST "+proto.PathReport, s.device(s.handleReport, true))
	m.HandleFunc("POST "+proto.PathOps, s.device(s.handleOps, true))
	m.HandleFunc("GET "+proto.PathUserView, s.device(s.handleUserView, true))
	m.HandleFunc("POST "+proto.PathInvite, s.device(s.handleInvite, true))
	m.HandleFunc("/device/", func(w http.ResponseWriter, r *http.Request) {
		s.writeErr(w, r, errNotFound("没有这个接口"))
	})
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
func (s *Server) Handler() http.Handler { return s.mux }
