package localclient

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kittors/deneb/engine/localapi/apitype"
	"github.com/kittors/deneb/engine/localapi/fake"
)

// `deneb ping -c 1000` ends with a summary of every result, one line of
// about 120 KB: it must come through, not "bufio.Scanner: token too long".
func TestPingManyResults(t *testing.T) {
	dir, err := os.MkdirTemp("", "dnb") // short: Unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "e.sock")
	stop, err := fake.New().Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	n := 0
	sum, err := New(path).Ping(context.Background(), "build", PingOptions{N: 1000, Interval: time.Nanosecond}, func(apitype.PingResult) { n++ })
	if err != nil {
		t.Fatal(err)
	}
	if n != 1000 || sum.Sent != 1000 || len(sum.Results) != 1000 {
		t.Fatalf("%d results streamed, summary of %d pings with %d results, want 1000", n, sum.Sent, len(sum.Results))
	}
}
