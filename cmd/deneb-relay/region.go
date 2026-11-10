package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// A relay registering with a join code tells the control server where it
// is, so its card reads "我的服务器（香港）" with nothing typed in. It
// reports a clue, not a name: the server keeps the table of places
// (control/relayregion.go). In order:
//
//  1. -region, as the owner put it: "name:香港".
//  2. With -region auto (the install command's), the cloud's instance
//     metadata, which says the region of a cloud server:
//     "aliyun:cn-hongkong", "tencent:ap-hongkong", "aws:ap-east-1",
//     "gcp:asia-east2-a", "azure:eastasia". Each is one HTTP request to
//     the cloud's own address, and all of them together take half a
//     second at most; elsewhere they just fail. Only on asking: one of
//     them, Aliyun's 100.100.100.200, is in the range VPNs such as
//     Tailscale use, so a relay run by hand on a developer's machine
//     asks nothing.
//  3. $TZ, if it is set and not UTC: "tz:Asia/Hong_Kong".
//
// None of it is needed: with no clue the relay is named after its address.

// As -region: regionAuto asks the cloud too; regionOff looks nowhere.
const (
	regionAuto = "auto"
	regionOff  = "off"
)

// regionIDRE is what a cloud's region or zone id looks like.
var regionIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,40}$`)

// detectRegion is the relay's region clue, or "".
func detectRegion(ctx context.Context, flagValue string) string {
	switch v := strings.TrimSpace(flagValue); v {
	case regionOff:
		return ""
	case regionAuto:
		if c := probeClouds(ctx); c != "" {
			return c
		}
	case "":
	default:
		return "name:" + v
	}
	if tz := strings.TrimSpace(os.Getenv("TZ")); tz != "" && tz != "UTC" && tz != "Etc/UTC" && !strings.HasPrefix(tz, ":") {
		return "tz:" + tz
	}
	return ""
}

// probeClouds is cloudRegion; tests, which must not send anything to
// link-local addresses from a developer's machine, replace it.
var probeClouds = cloudRegion

// cloudProbe asks one cloud's metadata service for its region.
type cloudProbe struct {
	cloud string
	ask   func(ctx context.Context, c *http.Client) string
}

var cloudProbes = []cloudProbe{
	{"aliyun", func(ctx context.Context, c *http.Client) string {
		return metaGet(ctx, c, "http://100.100.100.200/latest/meta-data/region-id", nil)
	}},
	{"tencent", func(ctx context.Context, c *http.Client) string {
		return metaGet(ctx, c, "http://169.254.0.23/latest/meta-data/placement/region", nil)
	}},
	{"aws", func(ctx context.Context, c *http.Client) string {
		// IMDSv2: a session token first.
		req, err := http.NewRequestWithContext(ctx, "PUT", "http://169.254.169.254/latest/api/token", nil)
		if err != nil {
			return ""
		}
		req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "60")
		tok := metaDo(c, req)
		if tok == "" {
			return ""
		}
		return metaGet(ctx, c, "http://169.254.169.254/latest/meta-data/placement/region", map[string]string{"X-aws-ec2-metadata-token": tok})
	}},
	{"gcp", func(ctx context.Context, c *http.Client) string {
		// "projects/123/zones/asia-east2-a": the zone, which the server
		// takes back to its region.
		z := metaGet(ctx, c, "http://169.254.169.254/computeMetadata/v1/instance/zone", map[string]string{"Metadata-Flavor": "Google"})
		return z[strings.LastIndexByte(z, '/')+1:]
	}},
	{"azure", func(ctx context.Context, c *http.Client) string {
		return metaGet(ctx, c, "http://169.254.169.254/metadata/instance/compute/location?api-version=2021-02-01&format=text", map[string]string{"Metadata": "true"})
	}},
}

// cloudRegion asks every cloud at once, and takes the first answer in the
// list's order.
func cloudRegion(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	c := &http.Client{
		Transport: &http.Transport{
			Proxy:       nil, // a proxy cannot reach a link-local address anyway
			DialContext: (&net.Dialer{Timeout: 300 * time.Millisecond}).DialContext,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	answers := make([]chan string, len(cloudProbes))
	for i, p := range cloudProbes {
		answers[i] = make(chan string, 1)
		go func() { answers[i] <- p.ask(ctx, c) }()
	}
	for i, p := range cloudProbes {
		if v := strings.ToLower(<-answers[i]); regionIDRE.MatchString(v) {
			return p.cloud + ":" + v
		}
	}
	return ""
}

func metaGet(ctx context.Context, c *http.Client, url string, header map[string]string) string {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return ""
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return metaDo(c, req)
}

// metaDo is the body of a metadata answer, or "".
func metaDo(c *http.Client, req *http.Request) string {
	resp, err := c.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
