package main

// The website (DESIGN.md §14.16): the home page, the download page and the
// docs, built with the app into <static>/site (node web/app/build.mjs). The
// home page is / for everyone (design/plan/m7/site-entry.md): signed in, as
// the session cookie says, the site's pages are marked so, and their top
// bar offers the app (/lan) in place of the login; the app checks the
// session itself and goes to its login when it is over.

import (
	"bytes"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/kittors/deneb/proto"
)

// docName is what may follow /docs/: one of the docs' file names.
var docName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// sitePages answers the site's paths from dir/site and everything else
// with app.
func sitePages(dir string, app http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := r.Cookie(proto.WebSessionCookie)
		signedIn := err == nil
		var page string
		switch p := path.Clean("/" + r.URL.Path); {
		case p == "/":
			page = "index.html"
		case p == "/download":
			page = "download.html"
		case p == "/docs":
			page = "docs/install.html"
		case strings.HasPrefix(p, "/docs/"):
			page = "docs/" + strings.TrimPrefix(p, "/docs/") + ".html"
			if !docName.MatchString(strings.TrimPrefix(p, "/docs/")) {
				page = ""
			}
		default:
			app.ServeHTTP(w, r)
			return
		}
		status := http.StatusOK
		b, err := readSite(dir, page)
		if err != nil && page == "index.html" {
			// No site built: / is the app, as it was before the site.
			app.ServeHTTP(w, r)
			return
		}
		if err != nil {
			if _, unbuilt := readSite(dir, "index.html"); unbuilt != nil {
				http.Error(w, "the site is not built: run node web/app/build.mjs", http.StatusServiceUnavailable)
				return
			}
			// A doc that is not there: the site's own "not found".
			status = http.StatusNotFound
			if b, err = readSite(dir, "404.html"); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		if signedIn {
			b = bytes.Replace(b, []byte("<html "), []byte("<html data-signed-in "), 1)
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		h.Add("Vary", "Cookie")
		w.WriteHeader(status)
		_, _ = w.Write(b)
	})
}

// readSite reads the built site's page, "" for none.
func readSite(dir, page string) ([]byte, error) {
	if page == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(filepath.Join(dir, "site", filepath.FromSlash(page)))
}
