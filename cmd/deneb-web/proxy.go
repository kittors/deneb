package main

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/kittors/deneb/proto"
)

// newProxy passes /api/ on to the control server at u, over TLS trusted by
// its pinned key alone (proto.PinnedTLSConfig, as devices trust it). The
// browser's cookies and Sec-Fetch-Site go along unchanged, so the server's
// session and its cross-origin check work as for a page it served itself.
// The stream is passed on line by line as it comes.
func newProxy(u *url.URL, pin string) (http.Handler, error) {
	tr := &http.Transport{
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
	}
	switch {
	case u.Scheme == "https":
		tc, err := proto.PinnedTLSConfig(pin)
		if err != nil {
			return nil, fmt.Errorf("-control-pin: %w", err)
		}
		tr.TLSClientConfig = tc
	case u.Scheme == "http" && pin == "" && loopback(u.Hostname()):
		// deneb-control -dev, on this machine, speaks plain HTTP.
	default:
		return nil, fmt.Errorf("-control %s: the control server is reached over https with -control-pin (plain http only on this machine, for -dev)", u)
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(u)
			r.Out.Host = u.Host
			// Not a proxy the server should know of: no X-Forwarded-*.
			r.Out.Header.Del("X-Forwarded-For")
		},
		Transport:     tr,
		FlushInterval: -1, // the NDJSON stream, frame by frame
		ModifyResponse: func(resp *http.Response) error {
			stripSecure(resp.Header)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, `{"error":{"code":"internal","message":"连不上控制服务器"}}`)
		},
	}
	return rp, nil
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// stripSecure drops the Secure attribute from the server's cookies: the app
// is served over plain HTTP on this machine, where a browser would drop a
// Secure cookie from any host but localhost. (Behind TLS in a deployment,
// the cookie keeps it.)
func stripSecure(h http.Header) {
	cs := h.Values("Set-Cookie")
	if len(cs) == 0 {
		return
	}
	h.Del("Set-Cookie")
	for _, c := range cs {
		parts := strings.Split(c, ";")
		kept := parts[:1]
		for _, p := range parts[1:] {
			if !strings.EqualFold(strings.TrimSpace(p), "secure") {
				kept = append(kept, p)
			}
		}
		h.Add("Set-Cookie", strings.Join(kept, ";"))
	}
}
