package proto

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ControlEntrances are the ways to Deneb's own control server, the most
// direct first (design/plan/control-reachability-20261116.md §9.3): its
// address, a forward that hands the TLS through untouched (the same key, so
// the same pin), and a CDN that ends the TLS itself (no pin: the system's
// roots and the name). A release carries them (cmd/denebd, deploy/
// package.sh); a signed copy on the download site may come later, taken
// only with a greater Version.
type ControlEntrances struct {
	Version   int               `json:"version"`
	Entrances []ControlEntrance `json:"entrances"`
}

// ControlEntrance is one way in: an https URL, and the server key's pin
// when the TLS reaches the server itself.
type ControlEntrance struct {
	URL string `json:"url"`
	Pin string `json:"pin,omitempty"`
}

// ParseControlEntrances reads a list as JSON, or as base64 of the JSON (as a
// build passes it, in -ldflags), and checks it: at least one entrance,
// https, a pin that reads, and a name to check a certificate by where there
// is no pin.
func ParseControlEntrances(s string) (*ControlEntrances, error) {
	s = strings.TrimSpace(s)
	b := []byte(s)
	if !strings.HasPrefix(s, "{") {
		var err error
		if b, err = base64.StdEncoding.DecodeString(s); err != nil {
			return nil, fmt.Errorf("control entrances: neither JSON nor base64: %w", err)
		}
	}
	var l ControlEntrances
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, fmt.Errorf("control entrances: %w", err)
	}
	if l.Version < 1 || len(l.Entrances) == 0 {
		return nil, errors.New("control entrances: no version, or none")
	}
	for i, e := range l.Entrances {
		u, err := url.Parse(e.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" && u.Path != "/" {
			return nil, fmt.Errorf("control entrance %d: %q is not https://host[:port]", i+1, e.URL)
		}
		l.Entrances[i].URL = strings.TrimRight(e.URL, "/")
		if e.Pin != "" {
			if _, err := PinnedTLSConfig(e.Pin); err != nil {
				return nil, fmt.Errorf("control entrance %d: %w", i+1, err)
			}
		} else if net.ParseIP(u.Hostname()) != nil {
			return nil, fmt.Errorf("control entrance %d: %s has no pin and no name to check a certificate by", i+1, e.URL)
		}
	}
	return &l, nil
}

// Has reports whether u is one of the entrances.
func (l *ControlEntrances) Has(u string) bool {
	u = strings.TrimRight(u, "/")
	for _, e := range l.Entrances {
		if e.URL == u {
			return true
		}
	}
	return false
}
