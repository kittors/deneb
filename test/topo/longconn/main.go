// Command longconn holds one TCP connection to a peer's test web page for
// minutes, the way an SSH session or a game is used: a request ten times a
// second for a while, then nothing for longer than the engines' idle time
// (their heartbeat stops), then requests again (traffic wakes the path).
// It prints one JSON line: how long the slowest answer took, how long the
// first answer after each pause took, and whether the connection broke.
//
//	longconn [-socks 127.0.0.1:41080] [-active 20s] [-pause 12s] [-total 200s] host[:port]
//
// With -socks it goes through a user-space engine's SOCKS5 proxy (the
// colima topology); with -socks "" it dials directly (kernel mode). Any
// engine version serves the page (port 80), and any version's proxy works,
// so M1 and M2 engines can sit on either end.
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

type slow struct {
	At time.Time `json:"at"`
	Ms int64     `json:"ms"`
}

type result struct {
	Host     string  `json:"host"`
	Requests int     `json:"requests"`
	MaxMs    int64   `json:"maxMs"`   // the slowest answer
	WakesMs  []int64 `json:"wakesMs"` // the first answer after each pause
	Pauses   int     `json:"pauses"`
	Broken   bool    `json:"broken"`
	Err      string  `json:"err,omitempty"`
	// Slow are the answers over 300 ms, with wall-clock times to line up
	// with the engines' logs.
	Slow      []slow    `json:"slow,omitempty"`
	Started   time.Time `json:"started"`
	ConnectMs int64     `json:"connectMs"`
}

func main() {
	socks := flag.String("socks", "127.0.0.1:41080", "the engine's SOCKS5 proxy (\"\" to dial directly)")
	active := flag.Duration("active", 20*time.Second, "how long each busy stretch lasts")
	pause := flag.Duration("pause", 12*time.Second, "how long each silence lasts (longer than the engines' idle time)")
	total := flag.Duration("total", 200*time.Second, "how long to hold the connection")
	every := flag.Duration("every", 100*time.Millisecond, "how often to ask while busy")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: longconn [flags] host[:port]")
		os.Exit(2)
	}
	target := flag.Arg(0)
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		host, port = target, "80"
	}
	r := result{Host: host, WakesMs: []int64{}, Started: time.Now()}
	defer func() {
		b, _ := json.Marshal(r)
		fmt.Println(string(b))
	}()
	c, err := dial(*socks, host, port)
	r.ConnectMs = time.Since(r.Started).Milliseconds()
	if err != nil {
		r.Broken, r.Err = true, "connect: "+err.Error()
		return
	}
	defer c.Close()
	br := bufio.NewReader(c)
	end := r.Started.Add(*total)
	phaseEnd := time.Now().Add(*active)
	afterPause := false
	for i := 0; time.Now().Before(end); i++ {
		if time.Now().After(phaseEnd) && time.Now().Add(*pause+*every).Before(end) {
			time.Sleep(*pause) // nothing at all goes over the connection now
			r.Pauses++
			afterPause = true
			phaseEnd = time.Now().Add(*active)
		}
		t0 := time.Now()
		ms, err := ask(c, br, host, i)
		if err != nil {
			r.Broken, r.Err = true, fmt.Sprintf("request %d: %v", i, err)
			return
		}
		r.Requests++
		r.MaxMs = max(r.MaxMs, ms)
		if ms > 300 && len(r.Slow) < 100 {
			r.Slow = append(r.Slow, slow{At: t0, Ms: ms})
		}
		if afterPause {
			r.WakesMs = append(r.WakesMs, ms)
			afterPause = false
		}
		time.Sleep(time.Until(t0.Add(*every)))
	}
}

// ask sends one request on the open connection and reads the whole answer.
func ask(c net.Conn, br *bufio.Reader, host string, i int) (int64, error) {
	t0 := time.Now()
	c.SetDeadline(t0.Add(10 * time.Second))
	if _, err := fmt.Fprintf(c, "GET /?n=%d HTTP/1.1\r\nHost: %s\r\n\r\n", i, host); err != nil {
		return 0, err
	}
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return 0, err
	}
	_, err = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if err != nil {
		return 0, err
	}
	if resp.Close {
		return 0, errors.New("the server closed the connection")
	}
	return time.Since(t0).Milliseconds(), nil
}

// dial connects to host:port, through a SOCKS5 proxy if one is given.
func dial(socks, host, port string) (net.Conn, error) {
	if socks == "" {
		return net.DialTimeout("tcp", net.JoinHostPort(host, port), 10*time.Second)
	}
	c, err := net.DialTimeout("tcp", socks, 5*time.Second)
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(10 * time.Second))
	p, err := strconv.Atoi(port)
	if err != nil || len(host) > 255 {
		c.Close()
		return nil, fmt.Errorf("bad address %s:%s", host, port)
	}
	req := []byte{5, 1, 0} // version 5, one method: no authentication
	if _, err := c.Write(req); err != nil {
		c.Close()
		return nil, err
	}
	b := make([]byte, 2)
	if _, err := io.ReadFull(c, b); err != nil || b[0] != 5 || b[1] != 0 {
		c.Close()
		return nil, fmt.Errorf("socks greeting: %v %v", b, err)
	}
	req = append([]byte{5, 1, 0, 3, byte(len(host))}, host...)
	req = binary.BigEndian.AppendUint16(req, uint16(p))
	if _, err := c.Write(req); err != nil {
		c.Close()
		return nil, err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(c, head); err != nil || head[1] != 0 {
		c.Close()
		return nil, fmt.Errorf("socks connect refused: %v %v", head, err)
	}
	var skip int
	switch head[3] {
	case 1:
		skip = 4 + 2
	case 4:
		skip = 16 + 2
	case 3:
		l := make([]byte, 1)
		if _, err := io.ReadFull(c, l); err != nil {
			c.Close()
			return nil, err
		}
		skip = int(l[0]) + 2
	}
	if _, err := io.ReadFull(c, make([]byte, skip)); err != nil {
		c.Close()
		return nil, err
	}
	c.SetDeadline(time.Time{})
	return c, nil
}
