// Command deneb-web serves the Web version of Deneb (DESIGN.md §14): the
// browser app's static files, and the control server's Web API (/api/v1)
// behind it on the same origin, so that the session cookie and the server's
// cross-origin check see a same-origin request.
//
//	deneb-web -control https://103.42.31.155:47443 -control-pin sha256:…
//	deneb-web -mock          # an in-memory world instead, for development
//
// The browser talks to deneb-web only. /api/ goes to the control server over
// TLS trusted by its pinned key (as devices trust it); everything else is
// the app, with index.html for any path the app routes itself (/i/<token>,
// /lan/…).
package main

import (
	"compress/gzip"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:47620", "address to serve the app on")
	static := flag.String("static", "web/app/dist", "the built app (node web/app/build.mjs)")
	control := flag.String("control", "", "the control server's URL, as https://host:port")
	pin := flag.String("control-pin", "", "the control server's key, sha256:<hex>")
	mock := flag.Bool("mock", false, "answer /api/v1 from an in-memory world instead of a control server")
	flag.Parse()
	log.SetFlags(log.Ltime)

	api, err := apiHandler(*control, *pin, *mock)
	if err != nil {
		log.Fatal(err)
	}
	h := newServer(*static, api)
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	what := "control server " + *control
	if *mock {
		what = "an in-memory world (-mock)"
	}
	log.Printf("deneb-web on http://%s, the app from %s, /api/ from %s", ln.Addr(), *static, what)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// apiHandler is what answers /api/: the control server through the proxy,
// or the mock world.
func apiHandler(control, pin string, mock bool) (http.Handler, error) {
	if mock {
		if control != "" {
			return nil, errors.New("-mock and -control: choose one")
		}
		return newMock().handler(), nil
	}
	if control == "" {
		return nil, errors.New("give -control (with -control-pin) or -mock")
	}
	u, err := url.Parse(control)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("-control %q: want https://host:port", control)
	}
	return newProxy(u, pin)
}

// newServer puts the app and the API on one origin.
func newServer(static string, api http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", api)
	mux.Handle("/", gzipped(appFiles(static)))
	return headers(mux)
}

// headers are the page's protections: no framing, no referrer (an invite
// link's token must not leak to another site through Referer), and the
// app's own scripts only.
func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		}
		next.ServeHTTP(w, r)
	})
}

// appFiles serves the built app. A path that is not a file is one of the
// app's own routes: it gets index.html, never cached, so that a new build
// is picked up at once; the scripts and styles may be cached briefly.
func appFiles(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean("/" + r.URL.Path)
		if p != "/" {
			if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil && !fi.IsDir() {
				w.Header().Set("Cache-Control", "no-cache")
				files.ServeHTTP(w, r)
				return
			}
			if path.Ext(p) != "" && !strings.HasPrefix(p, "/i/") {
				// A missing script or style is an error, not a route.
				http.NotFound(w, r)
				return
			}
		}
		b, err := os.ReadFile(filepath.Join(dir, "index.html"))
		if err != nil {
			http.Error(w, "the app is not built: run node web/app/build.mjs", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	})
}

// gzipped compresses the app's text files for browsers that take gzip: the
// scripts are most of the first screen's bytes (design/plan/m3/web.md §7).
// Never the API: the stream must reach the browser frame by frame.
func gzipped(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.Method == http.MethodHead || r.Header.Get("Range") != "" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Add("Vary", "Accept-Encoding")
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

type gzipWriter struct {
	http.ResponseWriter
	gz      *gzip.Writer
	decided bool
}

// WriteHeader decides, from the status and the type, whether to compress.
func (g *gzipWriter) WriteHeader(code int) {
	if !g.decided {
		g.decided = true
		h := g.Header()
		ct := h.Get("Content-Type")
		text := strings.HasPrefix(ct, "text/") || strings.Contains(ct, "javascript") || strings.Contains(ct, "json") || strings.Contains(ct, "svg")
		if code == http.StatusOK && text && h.Get("Content-Encoding") == "" {
			h.Del("Content-Length")
			h.Set("Content-Encoding", "gzip")
			g.gz = gzip.NewWriter(g.ResponseWriter)
		}
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.decided {
		if g.Header().Get("Content-Type") == "" {
			g.Header().Set("Content-Type", http.DetectContentType(b))
		}
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

func (g *gzipWriter) close() {
	if g.gz != nil {
		_ = g.gz.Close()
	}
}
