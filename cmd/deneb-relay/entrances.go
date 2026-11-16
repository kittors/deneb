package main

import (
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kittors/deneb/proto"
)

// builtinEntrances are the ways to Deneb's own control server, as base64 of
// proto.ControlEntrances' JSON, as cmd/denebd's: a release sets them at
// build time (deploy/relay/release.sh), whose addresses past the first stay
// out of the repository. A relay whose join code names one of them goes by
// all of them (design/plan/control-reachability-20261116.md §9.3).
var builtinEntrances string

// entranceProbeEvery is how long a later way in is kept before the ones
// before it are tried first again.
var entranceProbeEvery = 30 * time.Minute

// controlEntrances are the built-in ways in; none in a development build.
func controlEntrances() []proto.ControlEntrance {
	if builtinEntrances == "" {
		return nil
	}
	l, err := proto.ParseControlEntrances(builtinEntrances)
	if err != nil {
		log.Printf("the built-in control entrances do not read (%v): the join code's address alone", err)
		return nil
	}
	return l.Entrances
}

// entranceTransport sends the requests the relay writes for the control
// server at base by the way in that got through last: their origin is
// swapped for that way's. One whose connection does not get through
// (connect, TLS) tries the others in the list's order; the first that gets
// through is the one from then on. A later one is kept entranceProbeEvery,
// then the ones before it come first again.
type entranceTransport struct {
	base  string
	ways  []entranceWay
	cur   atomic.Int32
	since atomic.Int64 // when the way in use became a later one (Unix nanoseconds)
	logf  func(string, ...any)
}

type entranceWay struct {
	u  *url.URL
	rt http.RoundTripper
}

// newEntranceTransport is the ways to base's server when base is one of
// ents, by transports newTr makes for each way's TLS config; nil when base
// is someone's own server.
func newEntranceTransport(base string, ents []proto.ControlEntrance, newTr func(*tls.Config) *http.Transport, logf func(string, ...any)) (*entranceTransport, error) {
	l := proto.ControlEntrances{Version: 1, Entrances: ents}
	if len(ents) < 2 || !l.Has(base) {
		return nil, nil
	}
	t := &entranceTransport{base: base, logf: logf}
	for _, e := range ents {
		u, err := url.Parse(e.URL)
		if err != nil {
			return nil, err
		}
		cfg := &tls.Config{MinVersion: tls.VersionTLS12} // a CDN's: the system's roots and the name
		if e.Pin != "" {
			if cfg, err = proto.PinnedTLSConfig(e.Pin); err != nil {
				return nil, err
			}
		}
		t.ways = append(t.ways, entranceWay{u: u, rt: newTr(cfg)})
	}
	return t, nil
}

func (t *entranceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.HasPrefix(req.URL.String(), t.base) {
		return t.ways[t.cur.Load()].rt.RoundTrip(req)
	}
	cur := t.cur.Load()
	if cur > 0 && time.Since(time.Unix(0, t.since.Load())) >= entranceProbeEvery {
		t.since.Store(time.Now().UnixNano())
		cur = 0
	}
	order := []int32{cur}
	for i := range int32(len(t.ways)) {
		if i != cur {
			order = append(order, i)
		}
	}
	var lastErr error
	for _, i := range order {
		w := t.ways[i]
		r := req.Clone(req.Context())
		if req.Body != nil && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			r.Body = body
		}
		r.URL.Scheme, r.URL.Host, r.Host = w.u.Scheme, w.u.Host, ""
		resp, err := w.rt.RoundTrip(r)
		if err == nil {
			if from := t.cur.Swap(i); from != i {
				if i > from {
					t.since.Store(time.Now().UnixNano())
				}
				t.logf("control server: through %s now", w.u.Host)
			}
			return resp, nil
		}
		lastErr = err
		if req.Context().Err() != nil || !unreached(err) {
			break // it may have reached the server: not sent again
		}
	}
	return nil, lastErr
}

// unreached: err is a connection that did not get through, to the server
// or past its TLS: the request did not reach it.
func unreached(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "TLS handshake") || strings.Contains(s, "tls: ") || strings.Contains(s, "x509: ") ||
		strings.Contains(s, "is not the pinned")
}
