package app

import (
	"context"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
)

// RecordPower samples a power meter for a while and stores the trace in a
// result file. Use it from a controller machine when the benchmark runs in a
// VM or container that cannot see the host BMC: give it the same --label as
// the run, and `compare` merges the two.
func (a *App) RecordPower(ctx context.Context, label, machine string, m port.PowerMeter, d, every time.Duration) (*result.File, error) {
	f, err := a.newFile(ctx, label, machine, "power")
	if err != nil {
		return nil, err
	}
	trace := &result.PowerTrace{Source: m.Source()}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		w, err := m.Watts(ctx)
		if err == nil {
			trace.Samples = append(trace.Samples, result.PowerSample{At: time.Now().UTC(), Watts: w})
			trace.Avg += w
			trace.Max = max(trace.Max, w)
			a.logf("power %7.1f W", w)
		} else if ctx.Err() == nil {
			a.logf("power: %v", err)
		}
		select {
		case <-ctx.Done():
			if n := len(trace.Samples); n > 0 {
				trace.Avg /= float64(n)
			}
			f.Power = trace
			f.FinishedAt = time.Now().UTC()
			return f, nil
		case <-tick.C:
		}
	}
}
