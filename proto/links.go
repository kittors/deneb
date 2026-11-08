package proto

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// Domain is the one DNS suffix Deneb takes over (multi-lan-tech §3.4). It
// is under .internal, which ICANN keeps for private use.
const Domain = "deneb.internal"

// FQDN is a device's formal name: device.owner.deneb.internal.
func FQDN(device, owner string) string { return device + "." + owner + "." + Domain }

// An invite link is https://<server>/i/<token>: a browser shows the landing
// page, and the client and the command line read the token out of it. The
// landing page hands it to the app as deneb://join/<token>. ParseInvite
// takes either, or the bare token.
const (
	InvitePath   = "/i/"
	JoinLinkBase = "deneb://join/"
)

// InviteURL is the link to send for an invite token.
func InviteURL(publicURL, token string) string {
	return strings.TrimRight(publicURL, "/") + InvitePath + token
}

// JoinLink is the deneb:// link that opens an invite in the app.
func JoinLink(token string) string { return JoinLinkBase + token }

// ParseInvite reads an invite token out of an invite link, a deneb:// join
// link, or the token itself, as someone may paste any of them.
func ParseInvite(s string) (string, error) {
	s = strings.TrimSpace(s)
	token := s
	if strings.Contains(s, "/") {
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("invite link %q: %w", s, err)
		}
		switch {
		case u.Scheme == "deneb" && u.Host == "join":
			token = strings.Trim(u.Path, "/")
		case (u.Scheme == "https" || u.Scheme == "http") && strings.HasPrefix(u.Path, InvitePath):
			token = strings.TrimPrefix(u.Path, InvitePath)
		default:
			return "", fmt.Errorf("%q is not an invite link", s)
		}
	}
	if !validToken(token) {
		return "", fmt.Errorf("%q is not an invite link", s)
	}
	return token, nil
}

// validToken reports whether s could be a token: what the server makes is
// base32 without padding, but any short run of letters and digits passes,
// and the server decides whether it is a real one.
func validToken(s string) bool {
	if len(s) < 8 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// MakeLabel turns a host name or the front of an email address into a DNS
// label: lower case, letters, digits and inner hyphens, at most 63 long.
// What has nothing usable in it ("阿杰的电脑") gives fallback.
func MakeLabel(s, fallback string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, ".local")
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i] // a host name's first label is the name; the rest is its domain
	}
	var b strings.Builder
	hyphen := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if hyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			hyphen = false
			b.WriteRune(r)
		case r == '\'' || r == '’':
			// "Jie's NAS" is "jies-nas", not "jie-s-nas".
		case r == '-' || r == '_' || unicode.IsSpace(r):
			hyphen = true
		default:
			// Letters outside a-z (Chinese, accents) cannot go in a label;
			// they split words like a space would.
			hyphen = true
		}
		if b.Len() >= 63 {
			break
		}
	}
	label := strings.Trim(b.String(), "-")
	if len(label) > 63 {
		label = strings.Trim(label[:63], "-")
	}
	if label == "" {
		return fallback
	}
	return label
}

// ValidLabel reports whether s is a label MakeLabel could have made.
func ValidLabel(s string) bool {
	return s != "" && MakeLabel(s, "") == s
}
