package app

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
	"github.com/chaloemph/cpubench/internal/workload"
)

type PowerCurveOptions struct {
	Label, Machine string
	Workload       string
	Levels         []float64 // utilisation %, e.g. 0,25,50,75,100
	PerLevel       time.Duration
	Settle         time.Duration
	Pin            bool
	Power          port.PowerMeter
}

// PowerCurve holds every CPU at a target utilisation (duty cycle over 100ms
// periods) and records average power at each level. Servers spend most of
// their life at 20-40% load, so idle and low-load watts drive TCO more than
// peak watts do.
func (a *App) PowerCurve(ctx context.Context, o PowerCurveOptions) (*result.File, error) {
	if o.Power == nil {
		return nil, fmt.Errorf("power-curve needs --power")
	}
	f, err := a.newFile(ctx, o.Label, o.Machine, "power-curve")
	if err != nil {
		return nil, err
	}
	w := workload.Find(o.Workload)
	if w == nil || w.Info().SingleThread {
		return nil, fmt.Errorf("unknown or single-thread workload %q", o.Workload)
	}
	cpus := cpuOrder(f.System, -1)
	n := len(cpus)
	if prev := runtime.GOMAXPROCS(0); n > prev {
		runtime.GOMAXPROCS(n)
		defer runtime.GOMAXPROCS(prev)
	}
	// Small kernel calls (a few ms) so the duty cycle is accurate.
	if err := w.Setup(n, 0.02); err != nil {
		return nil, err
	}
	defer w.Teardown()

	pc := &result.PowerCurve{Source: o.Power.Source(), Workload: o.Workload}
	const period = 100 * time.Millisecond
	for _, lvl := range o.Levels {
		duty := min(max(lvl/100, 0), 1)
		lctx, cancel := context.WithTimeout(ctx, o.Settle+o.PerLevel)
		var iters atomic.Int64
		var wg sync.WaitGroup
		if duty > 0 {
			for i := range n {
				wg.Go(func() {
					runtime.LockOSThread()
					if o.Pin && a.Pinner != nil {
						a.Pinner.PinCurrentThread(cpus[i])
					}
					busy := time.Duration(float64(period) * duty)
					for lctx.Err() == nil {
						t0 := time.Now()
						for time.Since(t0) < busy {
							w.RunThread(i)
							iters.Add(1)
						}
						if rest := period - time.Since(t0); rest > 0 {
							time.Sleep(rest)
						}
					}
				})
			}
		}
		select {
		case <-lctx.Done():
		case <-time.After(o.Settle):
		}
		startIters, t0 := iters.Load(), time.Now()
		var sum float64
		var cnt int
		tick := time.NewTicker(time.Second)
		for lctx.Err() == nil {
			select {
			case <-lctx.Done():
			case <-tick.C:
				if watts, err := o.Power.Watts(lctx); err == nil {
					sum += watts
					cnt++
				}
			}
		}
		tick.Stop()
		rate := float64(iters.Load()-startIters) / time.Since(t0).Seconds()
		cancel()
		wg.Wait()
		if err := ctx.Err(); err != nil {
			return f, err
		}
		pl := result.PowerLevel{UtilPct: lvl, Rate: rate}
		if cnt > 0 {
			pl.Watts = sum / float64(cnt)
		}
		pc.Levels = append(pc.Levels, pl)
		a.logf("power-curve %5.0f%%  %7.1f W  %9.1f it/s", lvl, pl.Watts, rate)
	}
	f.PowerCurve = pc
	f.FinishedAt = time.Now().UTC()
	return f, nil
}
