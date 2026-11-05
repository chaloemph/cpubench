package app

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/load"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
)

// slowTarget answers in 1ms and fails every request once failAbove rps is offered.
type slowTarget struct {
	calls atomic.Int64
	fail  atomic.Bool
}

func (s *slowTarget) Do(ctx context.Context, r load.Request) (int, error) {
	s.calls.Add(1)
	time.Sleep(time.Millisecond)
	if s.fail.Load() {
		return 503, nil
	}
	return 200, nil
}

type rateAgent struct {
	t      *slowTarget
	failAt float64
}

func (r rateAgent) Name() string { return "test" }

func (r rateAgent) Run(ctx context.Context, job port.LoadJob) (*port.LoadOutcome, error) {
	r.t.fail.Store(job.Phases[0].Rate >= r.failAt)
	return RunLoadJob(ctx, r.t, job)
}

func scenario(p load.Profile) load.Scenario {
	sc := load.Scenario{Target: "http://sut", Profile: p, Requests: []load.RequestSpec{{Path: "/x"}},
		Ramp: load.RampSpec{Start: 100, Step: 100, Max: 500, StepDuration: 500 * time.Millisecond}, Concurrency: 32,
		Rate: 200, Duration: time.Second}
	sc.Defaults()
	return sc
}

func TestLoadRampFindsKnee(t *testing.T) {
	a := twoNodeApp()
	tg := &slowTarget{}
	f, err := a.Load(context.Background(), LoadOptions{Label: "sut", Scenario: scenario(load.Ramp),
		Agents: []port.LoadAgent{rateAgent{t: tg, failAt: 300}}, Bodies: [][]byte{nil}, Power: constPower(90)})
	if err != nil {
		t.Fatal(err)
	}
	w := f.Web
	if len(w.Steps) != 4 { // 100, 200 pass; 300, 400 fail → stop after 2 fails
		t.Fatalf("steps %+v", w.Steps)
	}
	if w.KneeRPS < 190 || w.KneeRPS > 210 {
		t.Fatalf("knee %v", w.KneeRPS)
	}
	if w.Steps[0].Watts != 90 {
		t.Fatalf("watts %v", w.Steps[0].Watts)
	}
	if got := tg.calls.Load(); got < 450 || got > 560 {
		t.Fatalf("requests sent %d, want ~500", got)
	}
}

func TestLoadTwoAgentsSplitRate(t *testing.T) {
	a := twoNodeApp()
	t1, t2 := &slowTarget{}, &slowTarget{}
	f, err := a.Load(context.Background(), LoadOptions{Label: "sut", Scenario: scenario(load.Constant),
		Agents: []port.LoadAgent{LocalAgent{Target: t1}, LocalAgent{Target: t2}}, Bodies: [][]byte{nil}})
	if err != nil {
		t.Fatal(err)
	}
	if c1, c2 := t1.calls.Load(), t2.calls.Load(); c1 != 100 || c2 != 100 {
		t.Fatalf("calls %d %d", c1, c2)
	}
	if s := f.Web.Steps[0]; s.Requests != 200 || !s.PassSLO || len(f.Web.Timeline) == 0 {
		t.Fatalf("%+v timeline %d", s, len(f.Web.Timeline))
	}
}

func TestAdaptiveRamp(t *testing.T) {
	capacity := 0.0
	var rates []float64
	step := func(r float64) (result.WebStep, error) {
		rates = append(rates, r)
		return result.WebStep{TargetRPS: r, AchievedRPS: r, PassSLO: r <= capacity}, nil
	}
	capacity = 5000
	adaptiveRamp(load.RampSpec{Start: 1000, Max: 1e6}, step)
	if !slices.Equal(rates, []float64{1000, 2000, 4000, 8000, 5000, 6000}) {
		t.Fatalf("up: %v", rates)
	}
	rates, capacity = nil, 300
	adaptiveRamp(load.RampSpec{Start: 1000, Max: 1e6}, step)
	if !slices.Equal(rates, []float64{1000, 500, 250, 312.5}) {
		t.Fatalf("down: %v", rates)
	}
}
