package workload

import (
	"sync"
	"testing"
)

// Every workload must run at small scale and give identical checksums on
// repeated runs and across threads with the same id.
func TestWorkloadsDeterministic(t *testing.T) {
	for _, w := range All() {
		info := w.Info()
		t.Run(info.Name, func(t *testing.T) {
			threads := 2
			if info.SingleThread {
				threads = 1
			}
			if err := w.Setup(threads, 0.02); err != nil {
				t.Fatal(err)
			}
			defer w.Teardown()
			var wg sync.WaitGroup
			sums := make([]uint64, threads)
			for id := range threads {
				wg.Go(func() {
					w.RunThread(id)
					work, s := w.RunThread(id)
					if work <= 0 {
						t.Errorf("no work reported")
					}
					sums[id] = s
				})
			}
			wg.Wait()
			again := make([]uint64, threads)
			for id := range threads {
				_, again[id] = w.RunThread(id)
				if again[id] != sums[id] {
					t.Errorf("thread %d checksum changed: %x -> %x", id, sums[id], again[id])
				}
			}
		})
	}
}

func TestChainVisitsEveryLine(t *testing.T) {
	c := NewChain(64, 1)
	lines := len(c) / lineWords
	seen := map[uint32]bool{}
	var p uint32
	for range lines {
		seen[p] = true
		p = c[p]
	}
	if len(seen) != lines || p != 0 {
		t.Fatalf("visited %d of %d, end %d", len(seen), lines, p)
	}
}

func TestSelect(t *testing.T) {
	if n := len(Select(All(), "crypto")); n != 4 {
		t.Fatalf("crypto: %d", n)
	}
	if n := len(Select(All(), "mem/latency")); n != len(LatencySizesKB) {
		t.Fatalf("latency: %d", n)
	}
	if Find("int/json") == nil || Find("nope") != nil {
		t.Fatal("find")
	}
}
