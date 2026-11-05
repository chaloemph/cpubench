package workload

import (
	"fmt"
	"math/rand/v2"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
)

// ---- mem/triad (STREAM-like) ----

// triadWL splits a fixed total working set across threads (strong scaling),
// so more threads means more memory channels busy, not more memory. Each
// thread first-touches its own chunk on its first run, which places pages on
// the thread's NUMA node; the runner's warmup iteration absorbs that cost.
type triadWL struct {
	total   int
	a, b, c [][]float64
	inited  []bool
}

func (*triadWL) Info() benchmark.Info {
	return benchmark.Info{Name: "mem/triad", Category: benchmark.Memory, Kind: benchmark.Throughput,
		Unit: "MB/s", UnitScale: 1e-6, Description: "STREAM triad a=b+s*c over 3x128MB split across threads",
		FootprintMB: 384}
}

func (w *triadWL) Setup(threads int, scale float64) error {
	w.total = scaled(16<<20, scale)
	per := w.total / threads
	w.a, w.b, w.c = make([][]float64, threads), make([][]float64, threads), make([][]float64, threads)
	w.inited = make([]bool, threads)
	for t := range threads {
		w.a[t], w.b[t], w.c[t] = make([]float64, per), make([]float64, per), make([]float64, per)
	}
	return nil
}

func (w *triadWL) RunThread(id int) (float64, uint64) {
	a, b, c := w.a[id], w.b[id], w.c[id]
	if !w.inited[id] {
		for i := range b {
			b[i], c[i] = float64(i%1024), float64(i%7)
		}
		w.inited[id] = true
	}
	const reps, s = 4, 3.0
	for range reps {
		for i := range a {
			a[i] = b[i] + s*c[i]
		}
	}
	return float64(reps * 24 * len(a)), fsum(0, a[len(a)-1])
}

func (w *triadWL) Teardown() { w.a, w.b, w.c = nil, nil, nil }

// ---- mem/copy ----

type copyWL struct {
	src, dst [][]byte
	inited   []bool
}

func (*copyWL) Info() benchmark.Info {
	return benchmark.Info{Name: "mem/copy", Category: benchmark.Memory, Kind: benchmark.Throughput,
		Unit: "MB/s", UnitScale: 1e-6, Description: "memmove of 2x128MB split across threads (read + write bytes)",
		FootprintMB: 256}
}

func (w *copyWL) Setup(threads int, scale float64) error {
	per := scaled(128<<20, scale) / threads
	w.src, w.dst, w.inited = make([][]byte, threads), make([][]byte, threads), make([]bool, threads)
	for t := range threads {
		w.src[t], w.dst[t] = make([]byte, per), make([]byte, per)
	}
	return nil
}

func (w *copyWL) RunThread(id int) (float64, uint64) {
	s, d := w.src[id], w.dst[id]
	if !w.inited[id] {
		for i := range s {
			s[i] = byte(i)
		}
		clear(d)
		w.inited[id] = true
	}
	const reps = 4
	for range reps {
		copy(d, s)
	}
	return float64(reps * 2 * len(s)), uint64(d[len(d)-1])
}

func (w *copyWL) Teardown() { w.src, w.dst = nil, nil }

// ---- mem/latency-<size> (pointer chase) ----

// Chain is a random cyclic pointer chain over cache lines.
type Chain []uint32

const lineWords = 16 // 64-byte lines of uint32

// NewChain builds a single random cycle (Sattolo) visiting every 64-byte line
// of a sizeKB buffer once, defeating hardware prefetchers.
func NewChain(sizeKB int, seed uint64) Chain {
	lines := max(2, sizeKB*1024/64)
	perm := make([]uint32, lines)
	for i := range perm {
		perm[i] = uint32(i)
	}
	r := rand.New(rand.NewPCG(seed, 7))
	for i := lines - 1; i > 0; i-- {
		j := r.IntN(i)
		perm[i], perm[j] = perm[j], perm[i]
	}
	c := make(Chain, lines*lineWords)
	for i := range lines {
		c[perm[i]*lineWords] = perm[(i+1)%lines] * lineWords
	}
	return c
}

// Chase follows the chain n times and returns the final index.
func (c Chain) Chase(n int) uint32 {
	var p uint32
	for range n {
		p = c[p]
	}
	return p
}

type latencyWL struct {
	sizeKB int
	steps  int
	chain  Chain
}

func (w *latencyWL) Info() benchmark.Info {
	return benchmark.Info{Name: fmt.Sprintf("mem/latency-%s", sizeLabel(w.sizeKB)), Category: benchmark.Memory,
		Kind: benchmark.Latency, Unit: "ns", UnitScale: 1, SingleThread: true,
		FootprintMB: float64(w.sizeKB) / 1024, FixedSize: true,
		Description: fmt.Sprintf("dependent random loads over %s (cache/DRAM latency ladder)", sizeLabel(w.sizeKB))}
}

func sizeLabel(kb int) string {
	if kb >= 1024 {
		return fmt.Sprintf("%dM", kb/1024)
	}
	return fmt.Sprintf("%dK", kb)
}

func (w *latencyWL) Setup(threads int, scale float64) error {
	w.steps = scaled(4<<20, scale)
	w.chain = NewChain(w.sizeKB, uint64(w.sizeKB))
	w.chain.Chase(len(w.chain) / lineWords) // touch every line once
	return nil
}

func (w *latencyWL) RunThread(id int) (float64, uint64) {
	return float64(w.steps), uint64(w.chain.Chase(w.steps))
}

func (w *latencyWL) Teardown() { w.chain = nil }
