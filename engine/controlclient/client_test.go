package controlclient

import "testing"

// TestPlainHTTPOnlyForThisMachine: a release engine talks plain http to a
// control server on this machine only; anywhere else a network in between
// could rewrite the network map (M7 review). The test topology's builds
// may (PlainHTTPAnywhere).
func TestPlainHTTPOnlyForThisMachine(t *testing.T) {
	for _, c := range []struct {
		url      string
		anywhere bool
		ok       bool
	}{
		{"http://127.0.0.1:47443", false, true},
		{"http://localhost:47443", false, true},
		{"http://[::1]:47443", false, true},
		{"http://203.0.113.5:47443", false, false},
		{"http://192.168.1.10:47443", false, false},
		{"http://control.example:80", false, false},
		{"http://203.0.113.5:47443", true, true},
		{"https://203.0.113.5:47443", false, false}, // no pin
	} {
		_, err := New(Options{URL: c.url, PlainHTTPAnywhere: c.anywhere})
		if (err == nil) != c.ok {
			t.Errorf("New(%s, anywhere=%v): %v", c.url, c.anywhere, err)
		}
	}
}
