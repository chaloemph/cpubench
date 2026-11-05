package app

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
	"github.com/chaloemph/cpubench/internal/workload"
)

type StressOptions struct {
	Label, Machine string
	Filter         string
	Threads        int // 0 = all usable
	Duration       time.Duration
	Interval       time.Duration
	Scale          float64
	Pin            bool
	Power          port.PowerMeter
}

// DefaultStressMix is a web-server-like blend of kernels.
const DefaultStressMix = "int/compress,int/json,int/hashmap,float/matmul,crypto/aes-gcm,crypto/ecdsa-p256"

// Stress loads every thread continuously and samples throughput, frequency,
// temperature, power and cgroup throttling, to show sustained vs peak
// performance and catch instability (checksum errors, kernel hardware errors).
func (a *App) Stress(ctx context.Context, o StressOptions) (*result.File, error) {
	f, err := a.newFile(ctx, o.Label, o.Machine, "stress")
	if err != nil {
		return nil, err
	}
	ws := workload.Select(workload.All(), o.Filter)
	ws = slices.DeleteFunc(ws, func(w benchmark.Workload) bool { return w.Info().SingleThread })
	if len(ws) == 0 {
		return nil, fmt.Errorf("no multi-thread workload matches %q", o.Filter)
	}
	cpus := cpuOrder(f.System, -1)
	n := o.Threads
	if n <= 0 || n > len(cpus) {
		n = len(cpus)
	}
	cpus = cpus[:n]
	if prev := runtime.GOMAXPROCS(0); n > prev {
		runtime.GOMAXPROCS(n)
		defer runtime.GOMAXPROCS(prev)
	}
	st := &result.Stress{Threads: n, DurationSec: o.Duration.Seconds(), IntervalSec: o.Interval.Seconds()}
	for _, w := range ws {
		st.Workloads = append(st.Workloads, w.Info().Name)
		if err := w.Setup(n, o.Scale); err != nil {
			return nil, err
		}
		defer w.Teardown()
	}
	debug.FreeOSMemory()
	errsBefore := len(a.sensorKernelErrors())

	ctx, cancel := context.WithTimeout(ctx, o.Duration)
	defer cancel()
	iters := make([]atomic.Int64, n)
	var bad atomic.Int64
	var wg sync.WaitGroup
	for i := range n {
		w := ws[i%len(ws)]
		wg.Go(func() {
			runtime.LockOSThread()
			if o.Pin && a.Pinner != nil {
				a.Pinner.PinCurrentThread(cpus[i])
			}
			_, want := w.RunThread(i)
			for ctx.Err() == nil {
				if _, sum := w.RunThread(i); sum != want {
					bad.Add(1)
				}
				iters[i].Add(1)
			}
		})
	}

	_, thr0, _ := a.throttle()
	start := time.Now()
	var last int64
	lastT := start
	tick := time.NewTicker(o.Interval)
	for done := false; !done; {
		select {
		case <-ctx.Done():
			done = true
		case <-tick.C:
		}
		now := time.Now()
		if done && now.Sub(lastT) < o.Interval/2 {
			break // a sliver after the last tick would read as a fake drop
		}
		var total int64
		for i := range iters {
			total += iters[i].Load()
		}
		s := result.StressSample{T: now.Sub(start).Seconds(), Rate: float64(total-last) / now.Sub(lastT).Seconds(), Errors: bad.Load()}
		last, lastT = total, now
		if a.Sensors != nil {
			s.MHz, _ = a.Sensors.AvgMHz(cpus)
			s.TempC, _ = a.Sensors.MaxTempC()
		}
		if _, usec, ok := a.throttle(); ok {
			s.ThrottledUsec = usec - thr0
		}
		if o.Power != nil {
			pctx, pc := context.WithTimeout(context.Background(), 5*time.Second)
			s.Watts, _ = o.Power.Watts(pctx)
			pc()
		}
		if s.T > 0.5*o.Interval.Seconds() {
			st.Samples = append(st.Samples, s)
			a.logf("stress t=%5.0fs  rate %9.1f it/s  %6.0f MHz  %5.1f C  %6.1f W  errors %d", s.T, s.Rate, s.MHz, s.TempC, s.Watts, s.Errors)
		}
	}
	tick.Stop()
	wg.Wait()

	summarizeStress(st)
	st.Errors = bad.Load()
	if ke := a.sensorKernelErrors(); len(ke) > errsBefore {
		st.KernelErrors = ke[errsBefore:]
	}
	if o.Power != nil {
		st.PowerSource = o.Power.Source()
	}
	f.Stress = st
	f.FinishedAt = time.Now().UTC()
	return f, nil
}

func (a *App) throttle() (int64, int64, bool) {
	if a.Sensors == nil {
		return 0, 0, false
	}
	return a.Sensors.CgroupThrottle()
}

func (a *App) sensorKernelErrors() []string {
	if a.Sensors == nil {
		return nil
	}
	return a.Sensors.KernelErrors()
}

// summarizeStress: peak = best of the first 20% of samples, sustained = mean
// of the last 20%.
func summarizeStress(st *result.Stress) {
	n := len(st.Samples)
	if n == 0 {
		return
	}
	k := max(1, n/5)
	for _, s := range st.Samples[:k] {
		st.PeakRate = max(st.PeakRate, s.Rate)
	}
	var sum, watts float64
	var wn int
	for _, s := range st.Samples[n-k:] {
		sum += s.Rate
	}
	st.SustainedRate = sum / float64(k)
	if st.PeakRate > 0 {
		st.DegradationPct = (st.SustainedRate/st.PeakRate - 1) * 100
	}
	st.MinMHz = math.Inf(1)
	for _, s := range st.Samples {
		if s.MHz > 0 {
			st.MinMHz = min(st.MinMHz, s.MHz)
			st.MaxMHz = max(st.MaxMHz, s.MHz)
		}
		st.MaxTempC = max(st.MaxTempC, s.TempC)
		st.ThrottledUsec = max(st.ThrottledUsec, s.ThrottledUsec)
		if s.Watts > 0 {
			watts += s.Watts
			wn++
		}
	}
	if math.IsInf(st.MinMHz, 1) {
		st.MinMHz = 0
	}
	if wn > 0 {
		st.AvgWatts = watts / float64(wn)
	}
}
