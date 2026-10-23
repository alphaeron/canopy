package parallel

import (
	"sync/atomic"
	"testing"
)

func TestFor(t *testing.T) {
	tests := []struct {
		name string
		n    int
	}{
		{name: "no calls", n: 0},
		{name: "fewer calls than the limit", n: Limit - 1},
		{name: "more calls than the limit", n: 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var running, peak atomic.Int32
			got := make([]int, tt.n)
			For(tt.n, func(i int) {
				now := running.Add(1)
				for p := peak.Load(); now > p && !peak.CompareAndSwap(p, now); p = peak.Load() {
				}
				got[i] = i + 1
				running.Add(-1)
			})
			for i, v := range got {
				if v != i+1 {
					t.Errorf("call %d did not run", i)
				}
			}
			if want := int32(Limit); peak.Load() > want {
				t.Errorf("peak concurrency = %d, want at most %d", peak.Load(), want)
			}
		})
	}
}
