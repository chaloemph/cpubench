package app

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
)

type fakeProbe struct{ sys hardware.System }

func (f fakeProbe) System(context.Context) (hardware.System, error) { return f.sys, nil }

type constPower float64

func (c constPower) Source() string                         { return "const" }
func (c constPower) Watts(context.Context) (float64, error) { return float64(c), nil }

func twoNodeApp() *App {
	sys := hardware.System{
		Hostname: "test",
		CPU:      hardware.CPUInfo{Arch: "amd64", Sockets: 1, CoresPerSocket: 2, ThreadsPerCore: 1, LogicalCPUs: 2},
		Topology: hardware.Topology{
			NUMANodes:   []hardware.NUMANode{{ID: 0, CPUs: []int{0}}, {ID: 1, CPUs: []int{1}}},
			CoreGroups:  [][]int{{0}, {1}},
			AllowedCPUs: []int{0, 1},
		},
		Context: hardware.ExecContext{Mode: hardware.ModeNative, EffectiveCPUs: 2},
	}
	return &App{Probe: fakeProbe{sys}, Version: "test"}
}

func TestRunProducesResults(t *testing.T) {
	a := twoNodeApp()
	f, err := a.Run(context.Background(), RunOptions{Label: "t", Filter: "int/sort,mem/latency-32K", Threads: []int{1, 0},
		Iterations: 2, Warmup: 1, Scale: 0.01, NUMANode: -1, NUMAMatrix: true, Power: constPower(100)})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, b := range f.Benchmarks {
		keys = append(keys, b.Key())
		if !b.ChecksumStable || len(b.Samples) != 2 || b.Stats.Median <= 0 {
			t.Errorf("%s: %+v", b.Key(), b)
		}
	}
	if !slices.Equal(keys, []string{"int/sort@1", "int/sort@2", "mem/latency-32K@1"}) {
		t.Fatalf("keys %v", keys)
	}
	if f.NUMA == nil || len(f.NUMA.LatencyNs) != 2 || f.NUMA.LatencyNs[1][0] <= 0 {
		t.Fatalf("numa %+v", f.NUMA)
	}
	if f.Power == nil || f.Power.Avg != 100 {
		t.Fatalf("power %+v", f.Power)
	}
	if f.Schema != result.Schema || f.Machine != "t" {
		t.Fatalf("%+v", f)
	}
}

func TestStressAndPowerCurve(t *testing.T) {
	a := twoNodeApp()
	f, err := a.Stress(context.Background(), StressOptions{Filter: "int/sort", Duration: 1500 * time.Millisecond,
		Interval: 300 * time.Millisecond, Scale: 0.01, Power: constPower(50)})
	if err != nil {
		t.Fatal(err)
	}
	s := f.Stress
	if len(s.Samples) < 3 || s.PeakRate <= 0 || s.SustainedRate <= 0 || s.Errors != 0 || s.AvgWatts != 50 {
		t.Fatalf("%+v", s)
	}
	pc, err := a.PowerCurve(context.Background(), PowerCurveOptions{Workload: "int/sort", Levels: []float64{0, 100},
		PerLevel: 1200 * time.Millisecond, Settle: 100 * time.Millisecond, Power: constPower(80)})
	if err != nil {
		t.Fatal(err)
	}
	lv := pc.PowerCurve.Levels
	if len(lv) != 2 || lv[0].Rate != 0 || lv[1].Rate <= 0 || lv[1].Watts != 80 {
		t.Fatalf("%+v", lv)
	}
}

func TestResolveThreads(t *testing.T) {
	if got := ResolveThreads([]int{1, 0, 4, 64}, 8); !slices.Equal(got, []int{1, 4, 8}) {
		t.Fatal(got)
	}
	if got, _ := ParseThreads("1, 2,max"); !slices.Equal(got, []int{1, 2, 0}) {
		t.Fatal(got)
	}
}

func TestMergeByLabel(t *testing.T) {
	a := &result.File{Label: "x", Command: "run", Benchmarks: make([]benchmark.Result, 1)}
	b := &result.File{Label: "x", Command: "web load", Web: &result.Web{KneeRPS: 5}}
	c := &result.File{Label: "y"}
	out := MergeByLabel([]*result.File{a, b, c})
	if len(out) != 2 || out[0].Web == nil || len(out[0].Benchmarks) != 1 || out[0].Command != "run+web load" {
		t.Fatalf("%+v", out[0])
	}
	if a.Web != nil {
		t.Fatal("input mutated")
	}
}

func TestRunSkipsWhatDoesNotFitMemory(t *testing.T) {
	a := twoNodeApp()
	p := a.Probe.(fakeProbe)
	p.sys.Memory.TotalMB = 64000
	p.sys.Context.Cgroup = &hardware.Cgroup{MemMaxMB: 640} // budget 320 MB
	a.Probe = p
	f, err := a.Run(context.Background(), RunOptions{Filter: "mem/latency-32K,mem/latency-512M,mem/triad", Threads: []int{1},
		Iterations: 1, Scale: 0.01, NUMANode: -1})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range f.Benchmarks {
		names = append(names, b.Name)
	}
	if !slices.Equal(names, []string{"mem/triad", "mem/latency-32K"}) {
		t.Fatalf("ran %v", names)
	}
	if f.Findings[len(f.Findings)-1].ID != "mem-limit" {
		t.Fatalf("%+v", f.Findings)
	}
}
