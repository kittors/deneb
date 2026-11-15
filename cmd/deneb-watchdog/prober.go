package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kittors/deneb/control/probe"
	"github.com/kittors/deneb/proto"
)

// The watchdog as the control server's second place (M7,
// design/plan/m7/relay-install-api.md §5): it asks the control server for
// relays to check, checks them from this machine as a device would reach
// them (control/probe), and posts back what it saw, so a relay is not
// called unreachable for a trouble on the control server's own route.

// ProberConfig is the file's "prober": the control server to ask, its
// certificate's pin, and the deployment's probe secret (also
// DENEB_PROBE_SECRET), the one in the control server's data/probe.json.
type ProberConfig struct {
	Control string `json:"control"`
	Pin     string `json:"pin,omitempty"`
	Secret  string `json:"secret,omitempty"`
}

// prober is a running second place.
type prober struct {
	name   string
	base   string
	secret string
	client *http.Client
	logf   func(string, ...any)
	check  func(context.Context, proto.ProbeTarget) proto.RelayProbe
}

// newProber checks the file's prober section; nil if there is none.
func newProber(name string, c *ProberConfig, getenv func(string) string) (*prober, error) {
	if c == nil {
		return nil, nil
	}
	if v := getenv("DENEB_PROBE_SECRET"); v != "" {
		c.Secret = v
	}
	u, err := url.Parse(c.Control)
	if err != nil || u.Host == "" || u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("prober: control %q: want https://host:port", c.Control)
	}
	if len(strings.TrimSpace(c.Secret)) < 24 {
		return nil, errors.New("prober: no secret, or shorter than 24 characters (the control server's data/probe.json)")
	}
	tr := &http.Transport{TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second}
	if u.Scheme == "https" {
		if c.Pin == "" {
			return nil, errors.New("prober: the control server's pin is missing")
		}
		cfg, err := proto.PinnedTLSConfig(c.Pin)
		if err != nil {
			return nil, fmt.Errorf("prober: %w", err)
		}
		tr.TLSClientConfig = cfg
	}
	return &prober{name: name, base: strings.TrimRight(c.Control, "/"), secret: strings.TrimSpace(c.Secret),
		client: &http.Client{Transport: tr, Timeout: 40 * time.Second}, logf: log.Printf, check: probe.Relay}, nil
}

// run asks for work and does it until ctx ends, waiting longer after each
// failure in a row, to a minute.
func (p *prober) run(ctx context.Context) {
	wait := time.Second
	for ctx.Err() == nil {
		var jobs proto.ProbeJobs
		if err := p.call(ctx, proto.PathProbeNext, proto.ProbeNextRequest{Name: p.name}, &jobs); err != nil {
			if ctx.Err() != nil {
				return
			}
			p.logf("prober: %v; asking again in %s", err, wait)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			wait = min(2*wait, time.Minute)
			continue
		}
		wait = time.Second
		for _, j := range jobs.Jobs {
			go p.do(ctx, j)
		}
	}
}

// do checks a job's addresses, all at once, and posts back what it saw.
func (p *prober) do(ctx context.Context, j proto.ProbeJob) {
	results := make([]proto.RelayProbe, len(j.Targets))
	var wg sync.WaitGroup
	for i, t := range j.Targets {
		wg.Go(func() {
			r := p.check(ctx, t)
			r.Family, r.Addr = t.Family, t.Addr
			results[i] = r
		})
	}
	wg.Wait()
	if err := p.call(ctx, proto.PathProbeDone, proto.ProbeDone{ID: j.ID, Results: results}, nil); err != nil && ctx.Err() == nil {
		p.logf("prober: job %s: %v", j.ID, err)
	}
}

// call posts in to the control server and decodes its answer into out.
func (p *prober) call(ctx context.Context, path string, in, out any) error {
	b, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, "POST", p.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var eb proto.ErrorBody
		if json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&eb) == nil && eb.Error != nil {
			return fmt.Errorf("%s: %s", resp.Status, eb.Error.Message)
		}
		return errors.New(resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
