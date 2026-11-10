package filter

import (
	"net/netip"
	"testing"
)

// The per-packet cost of an established TCP connection, both ways: what
// every data packet pays. -cpu 1,4: alone and under contention.
func BenchmarkEstablishedTCP(b *testing.B) {
	benchEstablished(b, false)
}

// The same just after a policy change cut another connection: the cut
// connections are remembered for cutMemory, and every segment is checked
// against them meanwhile.
func BenchmarkEstablishedTCPAfterCut(b *testing.B) {
	benchEstablished(b, true)
}

func benchEstablished(b *testing.B, recentCut bool) {
	f := newTestFilter()
	f.In(seg(lin, self, 5000, 80, syn, 100, ""))
	f.Out(seg(self, lin, 80, 5000, syn|ack, 500, ""))
	if recentCut {
		f.In(seg(lin, self, 5001, 80, syn, 100, ""))
		f.Out(seg(self, lin, 80, 5001, syn|ack, 500, ""))
		exp := f.now().UnixNano() + int64(cutMemory)
		f.mu.Lock()
		delete(f.flows, flowKey{ProtoTCP, netip.AddrPortFrom(self, 80), netip.AddrPortFrom(lin, 5001)})
		f.cut[flowKey{ProtoTCP, netip.AddrPortFrom(self, 80), netip.AddrPortFrom(lin, 5001)}] = exp
		f.mu.Unlock()
		f.noteCutUntil(exp)
	}
	in := seg(lin, self, 5000, 80, ack, 101, "some data")
	out := seg(self, lin, 80, 5000, ack, 501, "some data")
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if ok, why := f.In(in); !ok {
				b.Fatal(why)
			}
			if ok, why := f.Out(out); !ok {
				b.Fatal(why)
			}
		}
	})
}
