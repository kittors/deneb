package control

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The install command (M7, design/plan/m7/relay-install.md §3, §4): one
// line that downloads the install script from the download site, which
// downloads the relay program for the machine's CPU, checks it against the
// hash the line carries, and installs it as a service. The site's address
// is the deployment's (RelayDownload, never in the repository); which
// version to install, and the hash of its SHA256SUMS, the server reads from
// the site itself, every 10 minutes:
//
//	<RelayDownload>latest                       the version, a line
//	<RelayDownload><version>/SHA256SUMS         sha256sum's lines
//	<RelayDownload><version>/deneb-relay-linux-<arch>
//
// One hash covers every CPU's program: the script checks SHA256SUMS against
// it, then the program against SHA256SUMS.

// relayBuildEvery is how often the site is read; relayBuildRetry, how soon
// again when it could not be; relayBuildFirst, how long New waits for the
// first read, so the commands carry the hash from the start.
const (
	relayBuildEvery = 10 * time.Minute
	relayBuildRetry = time.Minute
	relayBuildFirst = 5 * time.Second
)

// relayBuild is what the site said last: the version to install and the
// hash of its SHA256SUMS.
type relayBuild struct {
	mu      sync.Mutex
	version string
	sum     string
	at      time.Time // when it was last read, or tried
	err     error
}

// versionRE is a version as a command may carry it.
var versionRE = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,31}$`)

// installBuild is the version and hash the install command carries now;
// "" before the site was read.
func (s *Server) installBuild() (version, sum string) {
	b := &s.relayBuild
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.version, b.sum
}

// refreshRelayBuild reads the site again if it was read longer ago than
// relayBuildEvery, or could not be read longer ago than relayBuildRetry.
// New calls it first; then the sweep, outside any lock: a slow site never
// holds an op up.
func (s *Server) refreshRelayBuild(ctx context.Context) {
	if s.cfg.RelayDownload == "" {
		return
	}
	b := &s.relayBuild
	b.mu.Lock()
	since := s.now().Sub(b.at)
	due := b.at.IsZero() || since >= relayBuildEvery || b.err != nil && since >= relayBuildRetry
	if due {
		b.at = s.now()
	}
	b.mu.Unlock()
	if !due {
		return
	}
	version, sum, err := readRelayBuild(ctx, s.relayHTTP(), s.cfg.RelayDownload)
	b.mu.Lock()
	defer b.mu.Unlock()
	if err != nil {
		if b.err == nil || b.err.Error() != err.Error() {
			if b.version == "" {
				s.logf("relay install: reading %s: %v; until it is read, the install command carries no hash (again in %s)", s.cfg.RelayDownload, err, relayBuildRetry)
			} else {
				s.logf("relay install: reading %s: %v; the command keeps %s", s.cfg.RelayDownload, err, b.version)
			}
		}
		b.err = err
		return
	}
	if version != b.version || sum != b.sum {
		s.logf("relay install: the command installs deneb-relay %s (SHA256SUMS %s…)", version, sum[:12])
	}
	b.version, b.sum, b.err = version, sum, nil
}

// relayHTTP is the client the site is read with: the system's CAs, a
// short timeout. Tests replace it.
func (s *Server) relayHTTP() *http.Client {
	if s.relayClient != nil {
		return s.relayClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// readRelayBuild reads the version to install and hashes its SHA256SUMS.
func readRelayBuild(ctx context.Context, c *http.Client, base string) (version, sum string, err error) {
	get := func(u string, max int64) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s: %s", u, resp.Status)
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
		if err != nil {
			return nil, err
		}
		if int64(len(b)) > max {
			return nil, fmt.Errorf("%s: longer than %d bytes", u, max)
		}
		return b, nil
	}
	b, err := get(base+"latest", 64)
	if err != nil {
		return "", "", err
	}
	version = strings.TrimSpace(string(b))
	if !versionRE.MatchString(version) {
		return "", "", fmt.Errorf("latest says %q, not a version", version)
	}
	sums, err := get(base+version+"/SHA256SUMS", 64<<10)
	if err != nil {
		return "", "", err
	}
	if !strings.Contains(string(sums), "deneb-relay") {
		return "", "", errors.New("SHA256SUMS names no deneb-relay")
	}
	h := sha256.Sum256(sums)
	return version, hex.EncodeToString(h[:]), nil
}

// relayScript is the install script's address: RelayScript, or the
// download site's /relay.
func (s *Server) relayScript() string {
	if s.cfg.RelayScript != "" {
		return s.cfg.RelayScript
	}
	u, err := url.Parse(s.cfg.RelayDownload)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/relay"
}

// installCommands are the lines that install a relay with join code code:
// by curl, by wget, by Docker. With no download site (a development
// server) the first is Docker's, as before M7.
func (s *Server) installCommands(code string) (curl, wget, docker, version, sum string) {
	docker = s.dockerCommand(code)
	if s.cfg.RelayDownload == "" {
		return docker, "", docker, "", ""
	}
	// As the install script takes them (deploy/relay/install.sh): the
	// code, then <version>:<sha256>.
	version, sum = s.installBuild()
	args := " | sh -s -- " + code
	if version != "" {
		args += " " + version + ":" + sum
	}
	script := s.relayScript()
	return "curl -fsSL " + script + args, "wget -qO- " + script + args, docker, version, sum
}

// dockerCommand is the one line that sets a relay up on a server with
// Docker: host network, its data in a volume that outlives the container,
// back up by itself after a reboot.
func (s *Server) dockerCommand(code string) string {
	// The log is kept to 3 files of 10 MB: it has the addresses that
	// connect, and on the owner's machine it would otherwise grow for as
	// long as the relay runs (PRIVACY.md).
	return "docker run -d --name deneb-relay --restart unless-stopped --network host " +
		"--log-opt max-size=10m --log-opt max-file=3 -v deneb-relay:/data " +
		s.cfg.RelayImage + " --join " + code + " --region auto"
}

// checkDownloadURL checks the download site's folder: https, ending in /,
// nothing a shell would read as more than a word.
func checkDownloadURL(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if !strings.HasSuffix(raw, "/") {
		raw += "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" ||
		strings.ContainsAny(raw, " \t\n'\"`$\\;&|<>()") {
		return "", fmt.Errorf("RelayDownload %q: want https://<site>/<folder>/", raw)
	}
	return raw, nil
}

// sameSecret compares two secrets in constant time.
func sameSecret(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// ProbeConfigFile is the probe secret's file in the data directory
// (Config.ProbeSecret): {"secret": "…"}, 0600, never in the repository.
const ProbeConfigFile = "probe.json"

// minProbeSecret is how long a probe secret must be at least.
const minProbeSecret = 24

// LoadProbeSecret reads the probe secret: DENEB_PROBE_SECRET, else
// probe.json in dir; "" if neither has one. A file open to others is
// closed.
func LoadProbeSecret(dir string, getenv func(string) string) (string, error) {
	secret := getenv("DENEB_PROBE_SECRET")
	path := filepath.Join(dir, ProbeConfigFile)
	if b, err := os.ReadFile(path); err == nil {
		var c struct {
			Secret string `json:"secret"`
		}
		if err := json.Unmarshal(b, &c); err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}
		if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
			os.Chmod(path, 0o600)
		}
		if secret == "" {
			secret = c.Secret
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	secret = strings.TrimSpace(secret)
	if secret != "" && len(secret) < minProbeSecret {
		return "", fmt.Errorf("the probe secret is %d characters: at least %d (openssl rand -hex 24)", len(secret), minProbeSecret)
	}
	return secret, nil
}
