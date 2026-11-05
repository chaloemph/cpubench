package workload

import (
	"sync"
	"sync/atomic"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
)

// ---- sync/atomic-contended ----

type atomicContendedWL struct {
	ops     int
	counter atomic.Uint64
}

func (*atomicContendedWL) Info() benchmark.Info {
	return benchmark.Info{Name: "sync/atomic-contended", Category: benchmark.Concurrency, Kind: benchmark.Throughput,
		Unit: "Mops/s", UnitScale: 1e-6, Description: "all threads atomically add to one counter (cache-line ping-pong, LSE on arm64)"}
}

func (w *atomicContendedWL) Setup(threads int, scale float64) error {
	w.ops = scaled(1<<20, scale)
	return nil
}

func (w *atomicContendedWL) RunThread(id int) (float64, uint64) {
	for range w.ops {
		w.counter.Add(1)
	}
	return float64(w.ops), uint64(w.ops)
}

func (w *atomicContendedWL) Teardown() {}

// ---- sync/atomic-local ----

type paddedCounter struct {
	v atomic.Uint64
	_ [120]byte // 128-byte stride: some arm64 cores keep coherence at 128B, where 64B padding false-shares
}

type atomicLocalWL struct {
	ops      int
	counters []paddedCounter
}

func (*atomicLocalWL) Info() benchmark.Info {
	return benchmark.Info{Name: "sync/atomic-local", Category: benchmark.Concurrency, Kind: benchmark.Throughput,
		Unit: "Mops/s", UnitScale: 1e-6, Description: "each thread atomically adds to its own 128-byte slot (uncontended baseline)"}
}

func (w *atomicLocalWL) Setup(threads int, scale float64) error {
	w.ops = scaled(8<<20, scale)
	w.counters = make([]paddedCounter, threads)
	return nil
}

func (w *atomicLocalWL) RunThread(id int) (float64, uint64) {
	c := &w.counters[id].v
	for range w.ops {
		c.Add(1)
	}
	return float64(w.ops), uint64(w.ops)
}

func (w *atomicLocalWL) Teardown() { w.counters = nil }

// ---- sync/mutex ----

type mutexWL struct {
	ops int
	mu  sync.Mutex
	n   uint64
}

func (*mutexWL) Info() benchmark.Info {
	return benchmark.Info{Name: "sync/mutex", Category: benchmark.Concurrency, Kind: benchmark.Throughput,
		Unit: "Mops/s", UnitScale: 1e-6, Description: "shared sync.Mutex around a counter (lock contention)"}
}

func (w *mutexWL) Setup(threads int, scale float64) error {
	w.ops = scaled(256<<10, scale)
	return nil
}

func (w *mutexWL) RunThread(id int) (float64, uint64) {
	for range w.ops {
		w.mu.Lock()
		w.n++
		w.mu.Unlock()
	}
	return float64(w.ops), uint64(w.ops)
}

func (w *mutexWL) Teardown() {}

// ---- sync/pingpong ----

type pingPongWL struct{ trips int }

func (*pingPongWL) Info() benchmark.Info {
	return benchmark.Info{Name: "sync/pingpong", Category: benchmark.Concurrency, Kind: benchmark.Throughput,
		Unit: "Ktrips/s", UnitScale: 1e-3, Description: "round trips between a pinned thread and a goroutine (OS thread wake-up latency)"}
}

func (w *pingPongWL) Setup(threads int, scale float64) error {
	w.trips = scaled(100_000, scale)
	return nil
}

func (w *pingPongWL) RunThread(id int) (float64, uint64) {
	ping, pong := make(chan int), make(chan int)
	go func() {
		for v := range ping {
			pong <- v + 1
		}
		close(pong)
	}()
	var sum uint64
	for i := range w.trips {
		ping <- i
		sum += uint64(<-pong)
	}
	close(ping)
	return float64(w.trips), sum
}

func (w *pingPongWL) Teardown() {}
