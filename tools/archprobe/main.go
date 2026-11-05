// Command archprobe is a diagnostic companion to cpubench (it is not part of the benchmark suite).
// It isolates per-thread atomics and memory behaviour so that surprising cpubench numbers on VMs and
// NUMA hosts can be traced to a cause, in particular to the physical placement of memory pages.
//
//	go build -o archprobe ./tools/archprobe && tools/archprobe/run-all.sh ./archprobe
package main

import (
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

var (
	mode    = flag.String("mode", "atomic", "atomic | plain | cas | triad | lat")
	threads = flag.Int("threads", 1, "threads")
	stride  = flag.Int("stride", 128, "bytes between per-thread counters")
	dur     = flag.Duration("dur", 2*time.Second, "duration for counter modes")
	cpu0    = flag.Int("cpu0", 0, "thread i is pinned to cpu0+i")
	thp     = flag.Bool("thp", false, "madvise(MADV_HUGEPAGE) big buffers before first touch")
	mb      = flag.Int("mb", 384, "triad: total MB over 3 arrays; lat: buffer MB")
	heap    = flag.Bool("heap", false, "burst: counters in a Go-heap slice instead of mmap")
	ops     = flag.Int("ops", 8<<20, "burst: ops per thread per iteration")
	gc      = flag.Bool("gc", false, "burst: runtime.GC() before each iteration, like cpubench")
	iters   = flag.Int("iters", 7, "burst: iterations (first is warmup)")
	glob    = flag.Bool("glob", false, "burst: read the op count from a global every iteration")
)

type slot struct {
	v uint64
	_ [120]byte
}

// burst mimics cpubench's harness: fresh pinned goroutines per iteration released by a gate,
// fixed work per thread, rate = total ops / wall, median of iterations.
func burst() {
	var heapCounters []slot
	var buf []byte
	if *heap {
		heapCounters = make([]slot, *threads)
	} else {
		buf = alloc(*threads**stride + 8192)
	}
	var rates []float64
	for it := 0; it < *iters; it++ {
		if *gc {
			runtime.GC()
		}
		gate := make(chan struct{})
		var ready, done sync.WaitGroup
		ready.Add(*threads)
		done.Add(*threads)
		for i := 0; i < *threads; i++ {
			go func() {
				defer done.Done()
				runtime.LockOSThread()
				pin(*cpu0 + i)
				var p *uint64
				if *heap {
					p = &heapCounters[i].v
				} else {
					p = (*uint64)(unsafe.Pointer(&buf[i**stride]))
				}
				n := *ops
				ready.Done()
				<-gate
				if *glob {
					for k := 0; k < *ops; k++ {
						atomic.AddUint64(p, 1)
					}
				} else {
					for k := 0; k < n; k++ {
						atomic.AddUint64(p, 1)
					}
				}
			}()
		}
		ready.Wait()
		t0 := time.Now()
		close(gate)
		done.Wait()
		if it > 0 {
			rates = append(rates, float64(*threads**ops)/time.Since(t0).Seconds()/1e6)
		}
	}
	sort.Float64s(rates)
	fmt.Printf("RESULT %.1f %.1f\n", rates[len(rates)/2], rates[0])
}

func pin(cpu int) {
	var s unix.CPUSet
	s.Set(cpu)
	if err := unix.SchedSetaffinity(0, &s); err != nil {
		panic(err)
	}
}

func alloc(n int) []byte {
	b, err := unix.Mmap(-1, 0, n, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_ANON|unix.MAP_PRIVATE)
	if err != nil {
		panic(err)
	}
	if *thp {
		_ = unix.Madvise(b, unix.MADV_HUGEPAGE)
	}
	return b
}

func hugeKB() int {
	b, _ := os.ReadFile("/proc/self/smaps_rollup")
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "AnonHugePages:") {
			var kb int
			fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(l, "AnonHugePages:")), "%d", &kb)
			return kb
		}
	}
	return -1
}

func counters() {
	buf := alloc(*threads**stride + 8192)
	var wg, start sync.WaitGroup
	var stop atomic.Bool
	ops := make([]uint64, *threads)
	start.Add(1)
	for i := 0; i < *threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runtime.LockOSThread()
			pin(*cpu0 + i)
			p := (*uint64)(unsafe.Pointer(&buf[i**stride]))
			start.Wait()
			var n uint64
			for !stop.Load() {
				switch *mode {
				case "atomic":
					for k := 0; k < 4096; k++ {
						atomic.AddUint64(p, 1)
					}
				case "plain":
					for k := 0; k < 4096; k++ {
						*p++
					}
				case "cas":
					for k := 0; k < 4096; k++ {
						for {
							old := atomic.LoadUint64(p)
							if atomic.CompareAndSwapUint64(p, old, old+1) {
								break
							}
						}
					}
				}
				n += 4096
			}
			ops[i] = n
		}()
	}
	time.Sleep(100 * time.Millisecond)
	t0 := time.Now()
	start.Done()
	time.Sleep(*dur)
	stop.Store(true)
	wg.Wait()
	el := time.Since(t0).Seconds()
	var total uint64
	for _, n := range ops {
		total += n
	}
	m := float64(total) / el / 1e6
	fmt.Printf("RESULT %.1f %.1f\n", m, m/float64(*threads))
}

func triad() {
	per := (*mb << 20) / 3 / *threads / 8 // float64 elements per array per thread
	ready := make(chan struct{}, *threads)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < *threads; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runtime.LockOSThread()
			pin(*cpu0 + i)
			mk := func() []float64 {
				b := alloc(per * 8)
				return unsafe.Slice((*float64)(unsafe.Pointer(&b[0])), per)
			}
			a, b, c := mk(), mk(), mk()
			for k := range b {
				b[k], c[k] = float64(k%1024), float64(k%7)
			}
			run := func() {
				for k := range a {
					a[k] = b[k] + 3*c[k]
				}
			}
			run()
			ready <- struct{}{}
			<-gate
			for r := 0; r < 6; r++ {
				run()
			}
		}()
	}
	for i := 0; i < *threads; i++ {
		<-ready
	}
	t0 := time.Now()
	close(gate)
	wg.Wait()
	el := time.Since(t0).Seconds()
	gbs := float64(6*24) * float64(per) * float64(*threads) / el / 1e9
	fmt.Printf("RESULT %.1f %d\n", gbs, hugeKB())
}

func lat() {
	lines := (*mb << 20) / 64
	b := alloc(lines * 64)
	c := unsafe.Slice((*uint32)(unsafe.Pointer(&b[0])), lines*16)
	perm := make([]uint32, lines)
	for i := range perm {
		perm[i] = uint32(i)
	}
	r := rand.New(rand.NewPCG(1, 7))
	for i := lines - 1; i > 0; i-- {
		j := r.IntN(i)
		perm[i], perm[j] = perm[j], perm[i]
	}
	for i := range lines {
		c[perm[i]*16] = perm[(i+1)%lines] * 16
	}
	done := make(chan float64)
	go func() {
		runtime.LockOSThread()
		pin(*cpu0)
		var p uint32
		for i := 0; i < 2<<20; i++ {
			p = c[p]
		}
		const steps = 8 << 20
		t0 := time.Now()
		for i := 0; i < steps; i++ {
			p = c[p]
		}
		ns := float64(time.Since(t0).Nanoseconds()) / steps
		if p == 1<<31 {
			fmt.Println(p)
		}
		done <- ns
	}()
	fmt.Printf("RESULT %.1f %d\n", <-done, hugeKB())
}

// series: one pinned thread, atomic add, rate per 100 ms wall-clock window (aligned to absolute
// time so concurrent processes on different vCPUs can be compared window by window).
func series() {
	buf := alloc(8192)
	p := (*uint64)(unsafe.Pointer(&buf[0]))
	runtime.LockOSThread()
	pin(*cpu0)
	end := time.Now().Add(*dur)
	win := time.Now().UnixMilli() / 100
	var n uint64
	for time.Now().Before(end) {
		for k := 0; k < 1024; k++ {
			atomic.AddUint64(p, 1)
		}
		n += 1024
		if w := time.Now().UnixMilli() / 100; w != win {
			fmt.Printf("W %d %.1f\n", win, float64(n)/0.1/1e6)
			win, n = w, 0
		}
	}
}

// pages: one pinned thread measures private-atomic speed on 512 different 4 KB pages (20 ms each),
// twice. If speed is a property of the page, the same pages are slow in both passes.
func pages() {
	const N = 512
	buf := alloc(N * 4096)
	for i := 0; i < N; i++ {
		buf[i*4096] = 1
	}
	runtime.LockOSThread()
	pin(*cpu0)
	rate := func(i int) float64 {
		p := (*uint64)(unsafe.Pointer(&buf[i*4096]))
		t0 := time.Now()
		n := 0
		for time.Since(t0) < 20*time.Millisecond {
			for k := 0; k < 1024; k++ {
				atomic.AddUint64(p, 1)
			}
			n += 1024
		}
		return float64(n) / time.Since(t0).Seconds() / 1e6
	}
	cls := func(v float64) int {
		switch {
		case v < 20:
			return 0
		case v < 50:
			return 1
		case v < 80:
			return 2
		}
		return 3
	}
	r1, r2 := make([]int, N), make([]int, N)
	var h1, h2 [4]int
	for i := 0; i < N; i++ {
		r1[i] = cls(rate(i))
		h1[r1[i]]++
	}
	for i := 0; i < N; i++ {
		r2[i] = cls(rate(i))
		h2[r2[i]]++
	}
	same, slowBoth, slowAny := 0, 0, 0
	for i := 0; i < N; i++ {
		if r1[i] == r2[i] {
			same++
		}
		if r1[i] < 2 && r2[i] < 2 {
			slowBoth++
		}
		if r1[i] < 2 || r2[i] < 2 {
			slowAny++
		}
	}
	fmt.Printf("pages=%d  bins[<20 | 20-50 | 50-80 | >=80 Mops/s]  pass1=%v pass2=%v  same-bin-in-both=%d/%d  slow(<50): both=%d any=%d\n",
		N, h1, h2, same, N, slowBoth, slowAny)
}

// classlat: classify every 4 KB page of a big buffer by private-atomic speed (1 ms each), then run a
// random pointer chase confined to fast pages only, to slow pages only, and to a mix.
func classlat() {
	const pg = 4096
	n := (*mb << 20) / pg
	buf := alloc(n * pg)
	for i := 0; i < n; i++ {
		buf[i*pg] = 1
	}
	runtime.LockOSThread()
	pin(*cpu0)
	var fast, slow []int
	for i := 0; i < n; i++ {
		p := (*uint64)(unsafe.Pointer(&buf[i*pg]))
		t0 := time.Now()
		cnt := 0
		for time.Since(t0) < time.Millisecond {
			for k := 0; k < 256; k++ {
				atomic.AddUint64(p, 1)
			}
			cnt += 256
		}
		if float64(cnt)/time.Since(t0).Seconds()/1e6 >= 50 {
			fast = append(fast, i)
		} else {
			slow = append(slow, i)
		}
	}
	words := unsafe.Slice((*uint32)(unsafe.Pointer(&buf[0])), len(buf)/4)
	chase := func(pages []int) float64 {
		lines := len(pages) * 64
		ids := make([]uint32, lines)
		for pi, p := range pages {
			for l := 0; l < 64; l++ {
				ids[pi*64+l] = uint32(p*64 + l)
			}
		}
		perm := make([]int, lines)
		for i := range perm {
			perm[i] = i
		}
		r := rand.New(rand.NewPCG(3, 9))
		for i := lines - 1; i > 0; i-- {
			j := r.IntN(i)
			perm[i], perm[j] = perm[j], perm[i]
		}
		for i := 0; i < lines; i++ {
			words[ids[perm[i]]*16] = ids[perm[(i+1)%lines]] * 16
		}
		p := ids[perm[0]] * 16
		for i := 0; i < 2<<20; i++ {
			p = words[p]
		}
		const steps = 4 << 20
		t0 := time.Now()
		for i := 0; i < steps; i++ {
			p = words[p]
		}
		ns := float64(time.Since(t0).Nanoseconds()) / steps
		if p == 1<<31 {
			fmt.Println(p)
		}
		return ns
	}
	k := min(len(fast), len(slow))
	mix := make([]int, 0, 2*k)
	for i := 0; i < k; i++ {
		mix = append(mix, fast[i], slow[i])
	}
	fmt.Printf("pages=%d fast=%d (%.0f%%) slow=%d (%.0f%%); chase over %d pages (%d MB) each:\n", n, len(fast), 100*float64(len(fast))/float64(n), len(slow), 100*float64(len(slow))/float64(n), k, k*pg>>20)
	fmt.Printf("  fast pages only: %6.1f ns/load\n", chase(fast[:k]))
	fmt.Printf("  slow pages only: %6.1f ns/load\n", chase(slow[:k]))
	fmt.Printf("  50/50 mix      : %6.1f ns/load\n", chase(mix))
}

func main() {
	flag.Parse()
	switch *mode {
	case "atomic", "plain", "cas":
		counters()
	case "burst":
		burst()
	case "series":
		series()
	case "pages":
		pages()
	case "classlat":
		classlat()
	case "triad":
		triad()
	case "lat":
		lat()
	default:
		fmt.Println("unknown mode")
		os.Exit(2)
	}
}
