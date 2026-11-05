package app

import (
	"context"
	"fmt"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/load"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
)

// Profile sizes every stage of an automatic run.
type Profile struct {
	Name         string
	Scale        float64
	Iterations   int
	Threads      func(physical int) []int
	Stress       time.Duration
	PowerLevels  []float64
	PowerPer     time.Duration
	PowerSettle  time.Duration
	DensityGroup int // 0 = skip
	WebStep      time.Duration
	NUMAMatrix   bool
}

var Profiles = map[string]Profile{
	"smoke": {Name: "smoke", Scale: 0.05, Iterations: 2, Threads: func(int) []int { return []int{1, 0} },
		Stress: 15 * time.Second, PowerLevels: []float64{0, 100}, PowerPer: 5 * time.Second, PowerSettle: 2 * time.Second,
		WebStep: 3 * time.Second},
	"quick": {Name: "quick", Scale: 0.3, Iterations: 3, Threads: func(int) []int { return []int{1, 0} },
		Stress: 2 * time.Minute, PowerLevels: []float64{0, 50, 100}, PowerPer: 20 * time.Second, PowerSettle: 5 * time.Second,
		WebStep: 10 * time.Second},
	"standard": {Name: "standard", Scale: 1, Iterations: 5, Threads: func(p int) []int { return []int{1, p, 0} },
		Stress: 10 * time.Minute, PowerLevels: []float64{0, 25, 50, 75, 100}, PowerPer: 30 * time.Second, PowerSettle: 10 * time.Second,
		DensityGroup: 8, WebStep: 20 * time.Second, NUMAMatrix: true},
	"full": {Name: "full", Scale: 1, Iterations: 5, Threads: doubling,
		Stress: 30 * time.Minute, PowerLevels: []float64{0, 25, 50, 75, 100}, PowerPer: time.Minute, PowerSettle: 10 * time.Second,
		DensityGroup: 8, WebStep: 30 * time.Second, NUMAMatrix: true},
}

func doubling(physical int) []int {
	out := []int{1}
	for t := 2; t < physical; t *= 2 {
		out = append(out, t)
	}
	return append(out, physical, 0)
}

type AutoOptions struct {
	Label, Machine string
	Profile        string
	Power          port.PowerMeter // optional
	// Web, when set, also load tests this target (Scenario/Agents/SUT filled
	// by the caller); the ramp step length comes from the profile.
	Web *LoadOptions
	// Stage is called when each stage starts, for progress reporting.
	Stage func(name string)
}

// Auto runs every stage that applies to this machine and returns one
// merged result: the "one command" test.
func (a *App) Auto(ctx context.Context, o AutoOptions) (*result.File, error) {
	p, ok := Profiles[o.Profile]
	if !ok {
		return nil, fmt.Errorf("unknown profile %q (smoke, quick, standard, full)", o.Profile)
	}
	stage := func(n string) {
		a.logf("==== %s ====", n)
		if o.Stage != nil {
			o.Stage(n)
		}
	}
	sys, err := a.Probe.System(ctx)
	if err != nil {
		return nil, err
	}
	physical := max(1, min(sys.CPU.PhysicalCores(), sys.Context.EffectiveCPUs))

	stage("micro")
	f, err := a.Run(ctx, RunOptions{Label: o.Label, Machine: o.Machine, Filter: "all", Threads: p.Threads(physical),
		Iterations: p.Iterations, Warmup: 1, Scale: p.Scale, Pin: true, NUMANode: -1, NUMAMatrix: p.NUMAMatrix, Power: o.Power})
	if err != nil {
		return f, err
	}
	f.Command = "auto " + p.Name

	stage("stress")
	st, err := a.Stress(ctx, StressOptions{Label: f.Label, Machine: f.Machine, Filter: DefaultStressMix, Duration: p.Stress,
		Interval: 5 * time.Second, Scale: 0.25, Pin: true, Power: o.Power})
	if err != nil {
		return f, err
	}
	f.Stress = st.Stress

	if o.Power != nil {
		stage("power-curve")
		pc, err := a.PowerCurve(ctx, PowerCurveOptions{Label: f.Label, Machine: f.Machine, Workload: "int/json",
			Levels: p.PowerLevels, PerLevel: p.PowerPer, Settle: p.PowerSettle, Pin: true, Power: o.Power})
		if err != nil {
			return f, err
		}
		f.PowerCurve = pc.PowerCurve
	}

	if p.DensityGroup > 0 && physical >= 2*p.DensityGroup {
		stage("density")
		d, err := a.Density(ctx, DensityOptions{Label: f.Label, Machine: f.Machine, Workload: "int/json",
			GroupSize: p.DensityGroup, Iterations: 2, Scale: 0.3, Pin: true})
		if err != nil {
			return f, err
		}
		f.Density = d.Density
	}

	if o.Web != nil {
		stage("web")
		w := *o.Web
		w.Label, w.Machine = f.Label, f.Machine
		w.Scenario.Ramp.StepDuration = p.WebStep
		if w.Scenario.Profile == "" {
			w.Scenario.Profile = load.Ramp
		}
		wf, err := a.Load(ctx, w)
		if err != nil {
			return f, err
		}
		f.Web = wf.Web
	}
	f.FinishedAt = time.Now().UTC()
	return f, nil
}
