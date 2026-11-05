package app

import (
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/doctor"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
	"github.com/chaloemph/cpubench/internal/workload"
)

type RunOptions struct {
	Label, Machine string
	Filter         string
	Threads        []int // 0 = all usable CPUs
	Iterations     int
	Warmup         int
	Scale          float64
	Pin            bool
	NUMANode       int // -1 = whole machine
	NUMAMatrix     bool
	Power          port.PowerMeter // optional
}

// Run executes the micro-benchmark suite.
func (a *App) Run(ctx context.Context, o RunOptions) (*result.File, error) {
	f, err := a.newFile(ctx, o.Label, o.Machine, "run")
	if err != nil {
		return nil, err
	}
	ws := workload.Select(workload.All(), o.Filter)
	if len(ws) == 0 {
		return nil, fmt.Errorf("no workload matches %q", o.Filter)
	}
	cpus := cpuOrder(f.System, o.NUMANode)
	if len(cpus) == 0 {
		return nil, fmt.Errorf("no usable CPUs (numa node %d)", o.NUMANode)
	}
	threads := ResolveThreads(o.Threads, len(cpus))
	f.Config = result.RunConfig{Iterations: o.Iterations, Warmup: o.Warmup, Scale: o.Scale, Threads: threads, Pinned: o.Pin}
	if o.NUMANode >= 0 {
		n := o.NUMANode
		f.Config.NUMANode = &n
	}
	for _, w := range ws {
		f.Config.Workloads = append(f.Config.Workloads, w.Info().Name)
	}

	stopPower := a.tracePower(ctx, o.Power, f)
	defer stopPower()

	budget := MemoryBudgetMB(f.System)
	var skipped []string
	for _, w := range ws {
		info := w.Info()
		tl := threads
		if info.SingleThread {
			tl = []int{1}
		}
		for _, t := range tl {
			if err := ctx.Err(); err != nil {
				return f, err
			}
			if need := info.MemoryMB(t, o.Scale); budget > 0 && need > budget {
				a.logf("%-24s %4d thr  skipped: needs ~%.0f MB, memory budget %.0f MB", info.Name, t, need, budget)
				skipped = append(skipped, fmt.Sprintf("%s@%d", info.Name, t))
				continue
			}
			r, err := a.measure(w, t, cpus[:t], o)
			if err != nil {
				return f, fmt.Errorf("%s@%d: %w", info.Name, t, err)
			}
			mark := ""
			if br, ok := w.(benchmark.BackendReporter); ok {
				if r.Backend = br.Backend(f.System.CPU); r.Backend != nil {
					mark += "  " + r.Backend.String()
				}
			}
			if r.Stats.Noisy() {
				mark += "  noisy"
			}
			if !r.ChecksumStable {
				mark += "  CHECKSUM UNSTABLE"
			}
			a.logf("%-24s %4d thr  %12.2f %-11s cv %4.1f%%%s", info.Name, t, r.Stats.Median, info.Unit, r.Stats.CV, mark)
			f.Benchmarks = append(f.Benchmarks, r)
		}
	}

	if len(skipped) > 0 {
		f.Findings = append(f.Findings, doctor.Finding{ID: "mem-limit", Severity: doctor.Warn,
			Message: fmt.Sprintf("skipped %d run(s) that need more than the %.0f MB memory budget: %s", len(skipped), budget, strings.Join(skipped, ", ")),
			Fix:     "raise the pod/container memory limit (2-4 GiB covers the whole suite) to compare these too"})
	}

	if o.NUMAMatrix && len(f.System.Topology.NUMANodes) > 1 && (budget == 0 || budget > 256) {
		m, err := a.NUMAMatrix(ctx, f.System, 128, o.Pin)
		if err != nil {
			return f, err
		}
		f.NUMA = m
	}
	f.FinishedAt = time.Now().UTC()
	return f, nil
}

func (a *App) measure(w benchmark.Workload, threads int, cpus []int, o RunOptions) (benchmark.Result, error) {
	info := w.Info()
	if err := w.Setup(threads, o.Scale); err != nil {
		return benchmark.Result{}, err
	}
	defer func() {
		w.Teardown()
		debug.FreeOSMemory() // hand freed heap back before the next workload (matters under a cgroup limit)
	}()
	prev := runtime.GOMAXPROCS(0)
	if threads > prev {
		runtime.GOMAXPROCS(threads)
		defer runtime.GOMAXPROCS(prev)
	}

	r := benchmark.Result{Name: info.Name, Category: info.Category, Kind: info.Kind, Unit: info.Unit,
		Threads: threads, ChecksumStable: true}
	if o.Pin {
		r.CPUs = cpus
	}
	var first string
	for i := range o.Warmup + o.Iterations {
		runtime.GC()
		v, sum, err := a.iterate(w, info, cpus, o.Pin)
		if err != nil {
			return r, err
		}
		if i == 0 {
			first = sum
		} else if sum != first {
			r.ChecksumStable = false
		}
		if i >= o.Warmup {
			r.Samples = append(r.Samples, v)
		}
	}
	r.Checksum = first
	r.Stats = benchmark.Summarize(r.Samples)
	return r, nil
}

// rawIteration is one barrier-released run of a workload on a set of CPUs.
type rawIteration struct {
	works []float64
	sums  []uint64
	durs  []time.Duration
	wall  time.Duration
}

// iterateRaw runs one iteration: one goroutine per CPU, each locked to its OS
// thread and pinned, released together by a barrier. Pinned threads exit
// still locked so the Go runtime discards them instead of reusing a thread
// with a narrowed affinity mask.
func (a *App) iterateRaw(w benchmark.Workload, cpus []int, pin bool) (rawIteration, error) {
	n := len(cpus)
	r := rawIteration{works: make([]float64, n), sums: make([]uint64, n), durs: make([]time.Duration, n)}
	errs := make([]error, n)
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	done.Add(n)
	for i := range n {
		go func() {
			defer done.Done()
			runtime.LockOSThread()
			if pin && a.Pinner != nil {
				errs[i] = a.Pinner.PinCurrentThread(cpus[i])
			}
			ready.Done()
			<-start
			t0 := time.Now()
			r.works[i], r.sums[i] = w.RunThread(i)
			r.durs[i] = time.Since(t0)
		}()
	}
	ready.Wait()
	t0 := time.Now()
	close(start)
	done.Wait()
	r.wall = time.Since(t0)
	for _, err := range errs {
		if err != nil {
			return r, fmt.Errorf("pin: %w", err)
		}
	}
	return r, nil
}

// iterate reduces one iteration to the workload's reported value and a checksum.
func (a *App) iterate(w benchmark.Workload, info benchmark.Info, cpus []int, pin bool) (float64, string, error) {
	r, err := a.iterateRaw(w, cpus, pin)
	if err != nil {
		return 0, "", err
	}
	var h uint64
	var total, lat float64
	for i := range cpus {
		h ^= r.sums[i] + uint64(i)*0x9e3779b97f4a7c15
		total += r.works[i]
		lat += float64(r.durs[i].Nanoseconds()) / r.works[i]
	}
	var v float64
	if info.Kind == benchmark.Latency {
		v = lat / float64(len(cpus)) * info.UnitScale
	} else {
		v = total / r.wall.Seconds() * info.UnitScale
	}
	return v, fmt.Sprintf("%016x", h), nil
}

// tracePower samples the meter every second into f.Power until stopped.
func (a *App) tracePower(ctx context.Context, m port.PowerMeter, f *result.File) func() {
	if m == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	trace := &result.PowerTrace{Source: m.Source()}
	wg.Go(func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			if w, err := m.Watts(ctx); err == nil {
				trace.Samples = append(trace.Samples, result.PowerSample{At: time.Now().UTC(), Watts: w})
			} else if ctx.Err() == nil {
				a.logf("power: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	})
	return func() {
		cancel()
		wg.Wait()
		for _, s := range trace.Samples {
			trace.Avg += s.Watts
			trace.Max = max(trace.Max, s.Watts)
		}
		if len(trace.Samples) > 0 {
			trace.Avg /= float64(len(trace.Samples))
			f.Power = trace
		}
	}
}

// MemoryBudgetMB is half of the smaller of RAM and the cgroup memory limit,
// leaving room for the Go runtime, GC headroom and the pinned threads (0 = unknown).
func MemoryBudgetMB(sys hardware.System) float64 {
	m := sys.Memory.TotalMB
	if cg := sys.Context.Cgroup; cg != nil && cg.MemMaxMB > 0 && (m == 0 || cg.MemMaxMB < m) {
		m = cg.MemMaxMB
	}
	return float64(m) * 0.5
}

// ParseThreads parses "1,2,4,max".
func ParseThreads(s string) ([]int, error) {
	var out []int
	for p := range strings.SplitSeq(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == "max" || p == "all" {
			out = append(out, 0)
			continue
		}
		var n int
		if _, err := fmt.Sscan(p, &n); err != nil || n < 0 {
			return nil, fmt.Errorf("bad thread count %q", p)
		}
		out = append(out, n)
	}
	return out, nil
}
