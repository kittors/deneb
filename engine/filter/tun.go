package filter

import (
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"sync/atomic"

	"golang.zx2c4.com/wireguard/tun"
)

// Headroom is the space kept in front of each packet: wireguard-go wants 16
// bytes for its header, and the macOS utun driver writes a 4-byte address
// family header there (multi-lan-tech §6.7, item 5).
const Headroom = 16

// TUN wraps the local stack, a gVisor stack or a kernel interface, and runs
// every packet between it and WireGuard through a Filter. WireGuard sees it
// as its tun.Device.
type TUN struct {
	inner tun.Device
	F     *Filter

	// LocalOut answers packets the local stack addresses to the engine
	// itself (the kernel-mode DNS responder) before the filter sees them:
	// a reply to hand back to the stack, or ok=false to pass the packet on.
	LocalOut func(pkt []byte) (reply []byte, ok bool)
	// ConsumeIn takes inbound packets the engine waits for itself (replies
	// to its own pings), after the filter let them in.
	ConsumeIn func(pkt []byte) bool

	out    chan []byte // from the local stack
	send   chan []byte // made by the engine; filtered like everything else
	inject chan []byte // test builds only: skips the filter on purpose
	reset  chan []byte // resets answering stale TCP (In's StaleTCP); made by the filter, not filtered again
	errc   chan error
	done   chan struct{}
	once   sync.Once
	// trace, when not 0, logs every TCP packet to or from this port, with
	// sequence numbers: to see what TCP does through a path switch (tests).
	trace    atomic.Uint32
	traceLog func(string, ...any)
}

// NewTUN wraps inner. Set the hooks, then call Start.
func NewTUN(inner tun.Device, f *Filter) *TUN {
	t := &TUN{
		inner:  inner,
		F:      f,
		out:    make(chan []byte, 512),
		send:   make(chan []byte, 64),
		inject: make(chan []byte, 16),
		reset:  make(chan []byte, 64),
		errc:   make(chan error, 1),
		done:   make(chan struct{}),
	}
	f.OnReset = t.resetCut
	return t
}

// resetCut sends the resets of connections a policy change cut: to this
// device's stack (off the caller's goroutine: SetPolicy runs under the
// engine's lock) and towards the peers.
func (t *TUN) resetCut(toStack, toPeers [][]byte) {
	if len(toStack) > 0 {
		go func() {
			for _, r := range toStack {
				t.WriteToStack(r)
			}
		}()
	}
	for _, r := range toPeers {
		select {
		case t.reset <- r:
		default:
		}
	}
}

// resetStack answers a segment this device's stack sent on a cut
// connection (CutTCPOut) with a reset to the stack.
func (t *TUN) resetStack(pkt []byte) {
	if rst := ResetFor(pkt); rst != nil {
		go t.WriteToStack(rst)
	}
}

// Start begins reading from the local stack.
func (t *TUN) Start() { go t.readLoop() }

func (t *TUN) readLoop() {
	bs := t.inner.BatchSize()
	if bs < 1 {
		bs = 1
	}
	bufs := make([][]byte, bs)
	for i := range bufs {
		bufs[i] = make([]byte, Headroom+65535)
	}
	sizes := make([]int, bs)
	for {
		n, err := t.inner.Read(bufs, sizes, Headroom)
		if err != nil {
			select {
			case t.errc <- err:
			default:
			}
			return
		}
		for i := 0; i < n; i++ {
			pkt := bufs[i][Headroom : Headroom+sizes[i]]
			if t.LocalOut != nil {
				if reply, ok := t.LocalOut(pkt); ok {
					if len(reply) > 0 {
						t.WriteToStack(reply)
					}
					continue
				}
			}
			select {
			case t.out <- append([]byte(nil), pkt...):
			case <-t.done:
				return
			}
		}
	}
}

// Send queues a packet the engine made (a ping) for WireGuard. It goes
// through the filter like any other.
func (t *TUN) Send(pkt []byte) error {
	select {
	case t.send <- pkt:
		return nil
	case <-t.done:
		return os.ErrClosed
	default:
		return errors.New("send queue full")
	}
}

// InjectUnfiltered queues a packet that skips the filter. Only the test
// build's spoofing endpoint calls it, to show that the receiving peer's
// WireGuard drops a forged source (multi-lan-tech §6.4).
func (t *TUN) InjectUnfiltered(pkt []byte) {
	select {
	case t.inject <- pkt:
	default:
	}
}

// WriteToStack hands a packet made inside the engine to the local stack.
func (t *TUN) WriteToStack(pkt []byte) error {
	buf := make([]byte, Headroom+len(pkt))
	copy(buf[Headroom:], pkt)
	_, err := t.inner.Write([][]byte{buf}, Headroom)
	return err
}

// Read gives wireguard-go the next outbound packets.
func (t *TUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	n := 0
	for n == 0 {
		var pkt []byte
		filtered := true
		select {
		case pkt = <-t.out:
		case pkt = <-t.send:
		case pkt = <-t.inject:
			filtered = false
		case pkt = <-t.reset:
			filtered = false
		case err := <-t.errc:
			return 0, err
		case <-t.done:
			return 0, os.ErrClosed
		}
		if filtered {
			if ok, why := t.F.Out(pkt); !ok {
				t.traced("drop-out:"+why, pkt)
				if why == CutTCPOut {
					t.resetStack(pkt)
				}
				continue
			}
		}
		t.traced("out", pkt)
		sizes[n] = copy(bufs[n][offset:], pkt)
		n++
	}
	// Hand over whatever else is already waiting, without blocking.
	for n < len(bufs) {
		var pkt []byte
		select {
		case pkt = <-t.out:
		case pkt = <-t.send:
		default:
			return n, nil
		}
		if ok, why := t.F.Out(pkt); !ok {
			t.traced("drop-out:"+why, pkt)
			if why == CutTCPOut {
				t.resetStack(pkt)
			}
			continue
		}
		t.traced("out", pkt)
		sizes[n] = copy(bufs[n][offset:], pkt)
		n++
	}
	return n, nil
}

// SetTrace logs TCP packets to or from port (0 stops it).
func (t *TUN) SetTrace(port uint16, logf func(string, ...any)) {
	t.traceLog = logf
	t.trace.Store(uint32(port))
}

func (t *TUN) traced(dir string, b []byte) {
	port := uint16(t.trace.Load())
	if port == 0 || len(b) < 20 || b[0]>>4 != 4 || b[9] != ProtoTCP {
		return
	}
	ihl := int(b[0]&0x0f) * 4
	if len(b) < ihl+20 {
		return
	}
	tcp := b[ihl:]
	sport, dport := binary.BigEndian.Uint16(tcp[0:]), binary.BigEndian.Uint16(tcp[2:])
	if sport != port && dport != port {
		return
	}
	flags := ""
	for i, c := range "FSRPA" {
		if tcp[13]&(1<<i) != 0 {
			flags += string(c)
		}
	}
	plen := int(binary.BigEndian.Uint16(b[2:])) - ihl - int(tcp[12]>>4)*4
	t.traceLog("[trace] %s %d->%d seq=%d ack=%d len=%d %s", dir, sport, dport,
		binary.BigEndian.Uint32(tcp[4:]), binary.BigEndian.Uint32(tcp[8:]), plen, flags)
}

// Write takes inbound packets from wireguard-go.
func (t *TUN) Write(bufs [][]byte, offset int) (int, error) {
	keep := bufs[:0:0]
	for _, b := range bufs {
		pkt := b[offset:]
		if ok, why := t.F.In(pkt); !ok {
			t.traced("drop-in:"+why, pkt)
			if why == StaleTCP || why == CutTCP {
				if rst := ResetFor(pkt); rst != nil {
					select {
					case t.reset <- rst:
					default: // a burst: the next segment draws one again
					}
				}
			}
			continue
		}
		t.traced("in", pkt)
		if t.ConsumeIn != nil && t.ConsumeIn(pkt) {
			continue
		}
		keep = append(keep, b)
	}
	if len(keep) > 0 {
		if _, err := t.inner.Write(keep, offset); err != nil {
			return 0, err
		}
	}
	return len(bufs), nil
}

func (t *TUN) File() *os.File           { return nil }
func (t *TUN) MTU() (int, error)        { return t.inner.MTU() }
func (t *TUN) Name() (string, error)    { return t.inner.Name() }
func (t *TUN) Events() <-chan tun.Event { return t.inner.Events() }
func (t *TUN) BatchSize() int           { return 16 }

func (t *TUN) Close() error {
	var err error
	t.once.Do(func() {
		close(t.done)
		err = t.inner.Close()
	})
	return err
}
