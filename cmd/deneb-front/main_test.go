package main

import (
	"bufio"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/kittors/deneb/control/front"
)

// TestPass: a connection comes out at the control server behind a PROXY
// header with the device's address, and what is said goes both ways as it
// is. When the control server is gone, the device's connection closes.
func TestPass(t *testing.T) {
	control, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	from := make(chan netip.AddrPort, 1)
	go func() {
		c, err := control.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		a, err := front.ReadHeader(br)
		if err != nil {
			t.Errorf("header: %v", err)
			return
		}
		from <- a
		line, _ := br.ReadString('\n')
		io.WriteString(c, "heard "+line)
	}()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go newEntrance(control.Addr().String(), t.Logf).serve(ln)

	d, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(d, "hello\n")
	b, err := io.ReadAll(d)
	if err != nil || string(b) != "heard hello\n" {
		t.Fatalf("back: %q %v", b, err)
	}
	if a := <-from; a.String() != d.LocalAddr().String() {
		t.Fatalf("the header said %s, the device is %s", a, d.LocalAddr())
	}

	// No control server: the device hears its connection close.
	control.Close()
	d2, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer d2.Close()
	d2.SetDeadline(time.Now().Add(15 * time.Second))
	if b, err := io.ReadAll(d2); err != nil || len(b) != 0 {
		t.Fatalf("with no control server: %q %v", b, err)
	}
}

func TestWarnOncePerMinute(t *testing.T) {
	var lines []string
	e := newEntrance("x", func(f string, a ...any) { lines = append(lines, f) })
	for range 3 {
		e.warn("dial", io.EOF)
	}
	e.warn("full", io.EOF)
	if len(lines) != 2 || !strings.Contains(strings.Join(lines, ";"), "deneb-front") {
		t.Fatalf("%q", lines)
	}
}
