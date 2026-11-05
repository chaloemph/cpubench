package app

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/doctor"
	"github.com/chaloemph/cpubench/internal/domain/load"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
)

type LoadOptions struct {
	Label, Machine string
	Scenario       load.Scenario
	Bodies         [][]byte
	Agents         []port.LoadAgent
	SUT            port.SUTStats   // optional: `cpubench web serve` stats endpoint
	Power          port.PowerMeter // optional: usually the SUT's BMC
}

// LocalAgent runs jobs in this process against a target.
type LocalAgent struct {
	Target port.LoadTarget
}

func (LocalAgent) Name() string { return "local" }

func (l LocalAgent) Run(ctx context.Context, job port.LoadJob) (*port.LoadOutcome, error) {
	return RunLoadJob(ctx, l.Target, job)
}

// RunLoadJob executes an open-model (constant arrival rate) load job. Each
// request has an intended send time; latency is measured from that time, so
// queueing inside an overloaded generator or server is counted instead of
// hidden (no coordinated omission).
func RunLoadJob(ctx context.Context, target port.LoadTarget, job port.LoadJob) (*port.LoadOutcome, error) {
	if d := time.Until(job.StartAt); d > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d):
		}
	}
	var total time.Duration
	for _, p := range job.Phases {
		total += p.Duration
	}
	window := time.Duration(max(job.WindowSec, 1) * float64(time.Second))
	nw := int(total/window) + 2
	windows := make([]*atomicAgg, nw)
	for i := range windows {
		windows[i] = newAtomicAgg()
	}
	out := &port.LoadOutcome{}
	jobStart := time.Now()
	offset := time.Duration(0)
	sc := job.Scenario
	workersMax := max(1, int(math.Ceil(float64(sc.Concurrency)*job.Share)))
	for pi, ph := range job.Phases {
		phaseStart := jobStart.Add(offset)
		offset += ph.Duration
		agg := runPhase(ctx, target, sc, job, pi, ph, phaseStart, workersMax, func(intended time.Time) *atomicAgg {
			i := int(intended.Sub(jobStart) / window)
			return windows[min(max(i, 0), nw-1)]
		})
		out.Phases = append(out.Phases, agg.Compact())
		if err := ctx.Err(); err != nil {
			return out, err
		}
	}
	for _, w := range windows {
		out.Windows = append(out.Windows, w.snapshot().Compact())
	}
	return out, nil
}

func runPhase(ctx context.Context, target port.LoadTarget, sc load.Scenario, job port.LoadJob, pi int, ph load.Phase,
	start time.Time, workersMax int, windowOf func(time.Time) *atomicAgg) *load.Agg {
	rate := ph.Rate * job.Share
	if rate <= 0 {
		time.Sleep(time.Until(start.Add(ph.Duration)))
		return load.NewAgg()
	}
	n := int64(rate * ph.Duration.Seconds())
	interval := float64(time.Second) / rate
	deadline := start.Add(ph.Duration + max(2*time.Second, ph.Duration/10))
	workers := int(min(int64(workersMax), max(n, 1)))
	var next atomic.Int64
	aggs := make([]*load.Agg, workers)
	var lastDone atomic.Int64
	var wg sync.WaitGroup
	for w := range workers {
		aggs[w] = load.NewAgg()
		wg.Go(func() {
			a := aggs[w]
			b := load.NewBuilder(sc.Target, sc.Requests, job.Bodies, job.Seed*1_000_003+uint64(pi)*7919+uint64(w))
			for ctx.Err() == nil {
				i := next.Add(1) - 1
				if i >= n {
					return
				}
				intended := start.Add(time.Duration(float64(i) * interval))
				win := windowOf(intended)
				if time.Now().After(deadline) {
					a.Dropped++
					win.dropped.Add(1)
					continue
				}
				if d := time.Until(intended); d > 0 {
					time.Sleep(d)
				}
				status, err := target.Do(ctx, b.Next())
				now := time.Now()
				lat := now.Sub(intended)
				failed := err != nil || status >= 400
				a.Hist.Record(lat)
				a.Requests++
				win.record(lat, failed)
				if failed {
					a.Errors++
				}
				lastDone.Store(max(lastDone.Load(), now.UnixNano()))
			}
		})
	}
	wg.Wait()
	agg := load.NewAgg()
	for _, a := range aggs {
		agg.Merge(a)
	}
	end := time.Unix(0, lastDone.Load())
	agg.Elapsed = max(end.Sub(start), ph.Duration)
	return agg
}

// atomicAgg is a lock-free Agg for per-window timelines shared by workers.
type atomicAgg struct {
	counts   []atomic.Uint64
	requests atomic.Int64
	errors   atomic.Int64
	dropped  atomic.Int64
	maxNs    atomic.Int64
}

func newAtomicAgg() *atomicAgg {
	return &atomicAgg{counts: make([]atomic.Uint64, len(load.NewHistogram().Counts))}
}

func (a *atomicAgg) record(d time.Duration, failed bool) {
	i := load.BucketOf(d)
	a.counts[i].Add(1)
	a.requests.Add(1)
	if failed {
		a.errors.Add(1)
	}
	ns := d.Nanoseconds()
	for {
		m := a.maxNs.Load()
		if ns <= m || a.maxNs.CompareAndSwap(m, ns) {
			break
		}
	}
}

func (a *atomicAgg) snapshot() *load.Agg {
	out := load.NewAgg()
	for i := range a.counts {
		c := a.counts[i].Load()
		out.Hist.Counts[i] = c
		out.Hist.Total += c
	}
	out.Hist.MaxNs = a.maxNs.Load()
	out.Requests, out.Errors, out.Dropped = a.requests.Load(), a.errors.Load(), a.dropped.Load()
	return out
}

// sample is one reading of SUT CPU and power during a load test.
type sample struct {
	at     time.Time
	cpuSec float64
	ncpu   int
	watts  float64
}

func (a *App) sampleLoop(ctx context.Context, o LoadOptions) (stop func() []sample) {
	if o.SUT == nil && o.Power == nil {
		return func() []sample { return nil }
	}
	ctx, cancel := context.WithCancel(ctx)
	var mu sync.Mutex
	var out []sample
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			s := sample{at: time.Now()}
			if o.SUT != nil {
				s.cpuSec, s.ncpu, _ = o.SUT.CPUSeconds(ctx)
			}
			if o.Power != nil {
				s.watts, _ = o.Power.Watts(ctx)
			}
			mu.Lock()
			out = append(out, s)
			mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	})
	return func() []sample {
		cancel()
		wg.Wait()
		return out
	}
}

// spanStats returns SUT CPU % and average watts between t0 and t1.
func spanStats(ss []sample, t0, t1 time.Time) (cpuPct, watts float64) {
	var first, last *sample
	var wsum float64
	var wn int
	for i := range ss {
		s := &ss[i]
		if s.at.Before(t0) || s.at.After(t1) {
			continue
		}
		if first == nil {
			first = s
		}
		last = s
		if s.watts > 0 {
			wsum += s.watts
			wn++
		}
	}
	if first != nil && last != first && first.ncpu > 0 && first.cpuSec > 0 {
		cpuPct = (last.cpuSec - first.cpuSec) / (last.at.Sub(first.at).Seconds() * float64(first.ncpu)) * 100
	}
	if wn > 0 {
		watts = wsum / float64(wn)
	} else {
		// span shorter than the sampling interval: use the nearest reading
		mid := t0.Add(t1.Sub(t0) / 2)
		best := time.Duration(math.MaxInt64)
		for _, s := range ss {
			if d := s.at.Sub(mid).Abs(); s.watts > 0 && d < best {
				best, watts = d, s.watts
			}
		}
	}
	return cpuPct, watts
}

// Load runs a web load test with the scenario's profile and returns a result
// file describing the SUT (when it runs `cpubench web serve`).
func (a *App) Load(ctx context.Context, o LoadOptions) (*result.File, error) {
	sc := o.Scenario
	if len(o.Agents) == 0 {
		return nil, fmt.Errorf("no load agents")
	}
	f, err := a.newFile(ctx, o.Label, o.Machine, "web load")
	if err != nil {
		return nil, err
	}
	lg := &result.LoadGen{Hostname: f.System.Hostname, Arch: f.System.CPU.Arch, CPU: f.System.CPU.Model,
		LogicalCPUs: f.System.CPU.LogicalCPUs, Agents: len(o.Agents)}
	if o.SUT != nil {
		if sys, err := o.SUT.System(ctx); err == nil {
			f.System = sys
			f.Findings = doctor.Check(sys)
			if o.Label == "" {
				f.Label, f.Machine = sys.Hostname, sys.Hostname
			}
		} else {
			o.SUT = nil
			a.logf("SUT stats unavailable (%v): not a cpubench target, SUT CPU will not be reported", err)
		}
	}
	if o.Power == nil && o.SUT != nil {
		if w, err := o.SUT.Watts(ctx); err == nil && w > 0 {
			o.Power = sutPower{o.SUT}
			a.logf("power: using the server's own meter (%.0f W now)", w)
		}
	}
	if o.SUT == nil {
		f.Findings = append(f.Findings, doctor.Finding{ID: "sut-unknown", Severity: doctor.Info,
			Message: "target is not `cpubench web serve`: system info describes the load generator, not the server"})
	}
	web := &result.Web{Target: sc.Target, Scenario: sc.Name, Profile: string(sc.Profile), SLO: sc.SLO,
		Concurrency: sc.Concurrency, Agents: len(o.Agents), KeepAlive: *sc.KeepAlive, LoadGen: lg}
	if o.Power != nil {
		web.PowerSource = o.Power.Source()
	}
	f.Web = web

	if sc.Warmup > 0 {
		rate := sc.Rate
		if sc.Profile == load.Ramp {
			rate = sc.Ramp.Start
		}
		a.logf("warmup %s at %.0f rps", sc.Warmup, rate)
		if _, _, err := a.distribute(ctx, o, []load.Phase{{Name: "warmup", Rate: rate, Duration: sc.Warmup}}, 1); err != nil {
			return nil, err
		}
	}

	stop := a.sampleLoop(ctx, o)
	var spans []span
	if sc.Profile == load.Ramp {
		runStep := func(rate float64) (result.WebStep, error) {
			ph := load.Phase{Name: fmt.Sprintf("%.0f", rate), Rate: rate, Duration: sc.Ramp.StepDuration}
			out, sp, err := a.distribute(ctx, o, []load.Phase{ph}, 1)
			if err != nil {
				return result.WebStep{}, err
			}
			spans = append(spans, sp...)
			step := out.Phases[0].Step(rate, sc.SLO)
			web.Steps = append(web.Steps, step)
			a.logStep(step)
			return step, nil
		}
		var err error
		if sc.Ramp.Adaptive {
			err = adaptiveRamp(sc.Ramp, runStep)
		} else {
			fails := 0
			for _, rate := range sc.RampRates() {
				var step result.WebStep
				if step, err = runStep(rate); err != nil {
					break
				}
				if step.PassSLO {
					fails = 0
				} else if fails++; fails >= sc.Ramp.StopAfterFails {
					break
				}
			}
		}
		if err != nil {
			stop()
			return f, err
		}
	} else {
		phases := sc.Phases()
		var total time.Duration
		for _, p := range phases {
			total += p.Duration
		}
		win := max(1, math.Round(total.Seconds()/600))
		out, sp, err := a.distribute(ctx, o, phases, win)
		if err != nil {
			stop()
			return f, err
		}
		spans = sp
		for i, p := range phases {
			step := out.Phases[i].Step(p.Rate, sc.SLO)
			web.Steps = append(web.Steps, step)
			a.logStep(step)
		}
		for i, w := range out.Windows {
			if w.Requests == 0 && w.Dropped == 0 {
				continue
			}
			web.Timeline = append(web.Timeline, result.WebSecond{T: float64(i+1) * win,
				RPS: float64(w.Requests) / win, P99Ms: load.Ms(w.Hist.Quantile(0.99)), Errors: w.Errors + w.Dropped})
		}
		switch sc.Profile {
		case load.Stress:
			web.RecoverySec = load.Recovery(web.Timeline, phases[0].Duration.Seconds(), win, sc.SLO)
		case load.Spike:
			web.RecoverySec = load.Recovery(web.Timeline, (phases[0].Duration + phases[1].Duration).Seconds(), win, sc.SLO)
		}
	}
	samples := stop()
	if sc.Profile == load.Ramp {
		// spans were recorded in run order; attach CPU/power before sorting by rate
		for i := range web.Steps {
			if i < len(spans) && len(samples) > 0 {
				web.Steps[i].SUTCPUPct, web.Steps[i].Watts = spanStats(samples, spans[i].t0, spans[i].t1)
			}
		}
		slices.SortStableFunc(web.Steps, func(x, y result.WebStep) int { return cmp.Compare(x.TargetRPS, y.TargetRPS) })
		samples = nil
	}
	for i := range web.Steps {
		if i < len(spans) && len(samples) > 0 {
			web.Steps[i].SUTCPUPct, web.Steps[i].Watts = spanStats(samples, spans[i].t0, spans[i].t1)
		}
	}
	web.KneeRPS = load.Knee(web.Steps)
	f.FinishedAt = time.Now().UTC()
	return f, nil
}

func (a *App) logStep(s result.WebStep) {
	verdict := "pass"
	if !s.PassSLO {
		verdict = "FAIL"
	}
	a.logf("web  target %8.0f  achieved %8.0f rps  p50 %7.2f  p99 %8.2f ms  err %d drop %d  %s",
		s.TargetRPS, s.AchievedRPS, s.P50Ms, s.P99Ms, s.Errors, s.Dropped, verdict)
}

// adaptiveRamp doubles the rate until a step fails the SLO, then tries three
// rates between the last pass and the failure. If even the start rate fails it
// halves the rate (down to 1/16 of start) to find a level the server can hold.
func adaptiveRamp(r load.RampSpec, step func(float64) (result.WebStep, error)) error {
	refine := func(pass, fail float64) error {
		for k := 1; k <= 3; k++ {
			s, err := step(pass + float64(k)*(fail-pass)/4)
			if err != nil || !s.PassSLO {
				return err
			}
		}
		return nil
	}
	pass := 0.0
	for rate := r.Start; rate <= r.Max; rate *= 2 {
		s, err := step(rate)
		if err != nil {
			return err
		}
		if s.PassSLO {
			pass = rate
			continue
		}
		if pass > 0 {
			return refine(pass, rate)
		}
		for low := rate / 2; low >= r.Start/16; low /= 2 {
			s, err := step(low)
			if err != nil {
				return err
			}
			if s.PassSLO {
				return refine(low, low*2)
			}
		}
		return nil
	}
	return nil
}

// sutPower reads watts through the SUT's stats endpoint.
type sutPower struct{ sut port.SUTStats }

func (s sutPower) Source() string                             { return "server under test" }
func (s sutPower) Watts(ctx context.Context) (float64, error) { return s.sut.Watts(ctx) }

// span is the wall-clock time of one phase, for CPU/power attribution.
type span struct{ t0, t1 time.Time }

// distribute sends the phases to every agent with an equal share of the
// rate, a common start time, and merges their outcomes.
func (a *App) distribute(ctx context.Context, o LoadOptions, phases []load.Phase, windowSec float64) (*port.LoadOutcome, []span, error) {
	startAt := time.Now().Add(500 * time.Millisecond)
	if len(o.Agents) > 1 {
		startAt = time.Now().Add(2 * time.Second)
	}
	outs := make([]*port.LoadOutcome, len(o.Agents))
	errs := make([]error, len(o.Agents))
	var wg sync.WaitGroup
	for i, ag := range o.Agents {
		job := port.LoadJob{Scenario: o.Scenario, Phases: phases, Share: 1 / float64(len(o.Agents)), StartAt: startAt,
			WindowSec: windowSec, Seed: uint64(i + 1), Bodies: o.Bodies}
		wg.Go(func() { outs[i], errs[i] = ag.Run(ctx, job) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return nil, nil, fmt.Errorf("agent %s: %w", o.Agents[i].Name(), err)
		}
	}
	merged := &port.LoadOutcome{}
	for _, out := range outs {
		for i, p := range out.Phases {
			if i >= len(merged.Phases) {
				merged.Phases = append(merged.Phases, load.NewAgg())
			}
			merged.Phases[i].Merge(p)
		}
		for i, w := range out.Windows {
			if i >= len(merged.Windows) {
				merged.Windows = append(merged.Windows, load.NewAgg())
			}
			merged.Windows[i].Merge(w)
		}
	}
	var spans []span
	t := startAt
	for _, p := range phases {
		spans = append(spans, span{t, t.Add(p.Duration)})
		t = t.Add(p.Duration)
	}
	return merged, spans, nil
}
