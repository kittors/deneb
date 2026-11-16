package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kittors/deneb/proto"
)

// siteDir is the app with the site built beside it.
func siteDir(t *testing.T) string {
	d := appDir(t)
	for name, title := range map[string]string{
		"site/index.html": "home", "site/download.html": "download", "site/404.html": "not found",
		"site/docs/install.html": "install", "site/docs/faq.html": "faq",
	} {
		p := filepath.Join(d, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`<!doctype html><html lang="zh-CN"><title>`+title+`</title>`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(d, "site", "site.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

// fetch gets path, with the session cookie or not: the status and the body.
func fetch(t *testing.T, url string, signedIn bool) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if signedIn {
		req.AddCookie(&http.Cookie{Name: proto.WebSessionCookie, Value: "tok"})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

// The site's pages for everyone, / too (design/plan/m7/site-entry.md): signed
// in, the pages know it, and their top bar offers the app; /lan, the app's
// way in, and the rest are the app's, as before.
func TestSitePages(t *testing.T) {
	web := httptest.NewServer(newServer(siteDir(t), newMock().handler()))
	defer web.Close()
	for _, c := range []struct {
		path     string
		signedIn bool
		status   int
		title    string
	}{
		{"/", false, 200, "home"},
		{"/", true, 200, "home"},
		{"/lan", true, 200, "app"},
		{"/lan", false, 200, "app"},
		{"/download", false, 200, "download"},
		{"/download", true, 200, "download"},
		{"/download/", false, 200, "download"},
		{"/docs", false, 200, "install"},
		{"/docs/", false, 200, "install"},
		{"/docs/faq", false, 200, "faq"},
		{"/docs/faq", true, 200, "faq"},
		{"/docs/nope", false, 404, "not found"},
		{"/docs/FAQ", false, 404, "not found"},
		{"/docs/faq.html", false, 404, "not found"},
		{"/docs/../download", false, 200, "download"},
		{"/login", false, 200, "app"},
		{"/lan/l_weekend", true, 200, "app"},
		{"/i/k3xQ9", false, 200, "app"},
	} {
		status, body, h := fetch(t, web.URL+c.path, c.signedIn)
		if status != c.status || !strings.Contains(body, "<title>"+c.title+"</title>") {
			t.Errorf("GET %s (signed in %v): %d %q, want %d %q", c.path, c.signedIn, status, body, c.status, c.title)
			continue
		}
		site := c.title != "app"
		if got := strings.Contains(body, "data-signed-in"); site && got != c.signedIn {
			t.Errorf("GET %s (signed in %v): the page says signed in %v", c.path, c.signedIn, got)
		}
		vary := strings.Join(h.Values("Vary"), ", ")
		if site && (h.Get("Cache-Control") != "no-cache" || !strings.Contains(vary, "Cookie")) {
			t.Errorf("GET %s: Cache-Control %q, Vary %q: a page that differs by the cookie must not be cached as one", c.path, h.Get("Cache-Control"), vary)
		}
		if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'self'") {
			t.Errorf("GET %s: no Content-Security-Policy", c.path)
		}
	}
	if status, body, _ := fetch(t, web.URL+"/site/site.css", false); status != 200 || body != "body{}" {
		t.Errorf("GET /site/site.css: %d %q", status, body)
	}
}

// Without the site built, / is the app as before, and its pages say how to
// build it.
func TestSiteNotBuilt(t *testing.T) {
	web := httptest.NewServer(newServer(appDir(t), newMock().handler()))
	defer web.Close()
	if status, body, _ := fetch(t, web.URL+"/", false); status != 200 || !strings.Contains(body, "<title>app</title>") {
		t.Errorf("GET /: %d %q, want the app", status, body)
	}
	if status, body, _ := fetch(t, web.URL+"/download", false); status != 503 || !strings.Contains(body, "build.mjs") {
		t.Errorf("GET /download: %d %q, want 503 saying how to build", status, body)
	}
}
