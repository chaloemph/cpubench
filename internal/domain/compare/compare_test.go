package compare

import (
	"math"
	"testing"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/build"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
)

func file(label, machine string, mode hardware.Mode, arch string, bs ...benchmark.Result) *result.File {
	f := &result.File{Label: label, Machine: machine, Benchmarks: bs}
	f.System.CPU.Arch = arch
	f.System.Context.Mode = mode
	return f
}

func br(name string, kind benchmark.Kind, threads int, v float64, sum string) benchmark.Result {
	return benchmark.Result{Name: name, Category: benchmark.Integer, Kind: kind, Threads: threads,
		Stats: benchmark.Stats{Median: v}, Checksum: sum}
}

func TestBuildSpeedupAndChecksum(t *testing.T) {
	x86 := file("x86", "x86", hardware.ModeNative, "amd64",
		br("a", benchmark.Throughput, 1, 100, "1"), br("lat", benchmark.Latency, 1, 100, "9"))
	arm := file("arm", "arm", hardware.ModeNative, "arm64",
		br("a", benchmark.Throughput, 1, 150, "1"), br("lat", benchmark.Latency, 1, 50, "8"))
	rep := Build([]*result.File{x86, arm}, "x86")
	for _, r := range rep.Rows {
		if r.Speedup["arm"] != 1.5 && r.Name == "a" {
			t.Fatalf("a speedup %v", r.Speedup)
		}
		if r.Name == "lat" {
			if r.Speedup["arm"] != 2 {
				t.Fatalf("latency speedup %v", r.Speedup)
			}
			if !r.ChecksumMismatch {
				t.Fatal("checksum mismatch not detected")
			}
		}
	}
	if g := rep.Overall["arm"]; math.Abs(g-math.Sqrt(3)) > 1e-9 {
		t.Fatalf("overall %v", g)
	}
}

func TestBuildAndBackendFindings(t *testing.T) {
	sha := func(threads int, kind benchmark.BackendKind, detail string) benchmark.Result {
		r := br("crypto/sha256", benchmark.Throughput, threads, 100, "1")
		r.Backend = &benchmark.Backend{Kind: kind, Detail: detail}
		return r
	}
	x86 := file("x86", "x86", hardware.ModeNative, "amd64", sha(1, benchmark.SIMD, "AVX2, no SHA-NI"), sha(8, benchmark.SIMD, "AVX2, no SHA-NI"))
	x86.Build = &build.Info{Version: "v1", GOARCH: "amd64", ArchLevel: "GOAMD64=v1"}
	arm := file("arm", "arm", hardware.ModeNative, "arm64", sha(1, benchmark.Hardware, "ARMv8 SHA2"), sha(8, benchmark.Hardware, "ARMv8 SHA2"))
	arm.Build = &build.Info{Version: "v2", GOARCH: "arm64", ArchLevel: "GOARM64=v8.0"}
	old := file("old", "old", hardware.ModeNative, "arm64", sha(1, benchmark.Hardware, "ARMv8 SHA2"))

	rep := Build([]*result.File{x86, arm, old}, "x86")
	count := map[string]int{}
	for _, f := range rep.Findings {
		count[f.ID]++
	}
	if count["backend-differs"] != 1 || count["build-version"] != 1 || count["build-unknown"] != 1 {
		t.Fatalf("findings %v", rep.Findings)
	}
	for _, r := range rep.Rows {
		if !r.BackendDiffers || r.Backends["arm"].Kind != benchmark.Hardware {
			t.Errorf("row %s: differs %v backends %v", r.Key, r.BackendDiffers, r.Backends)
		}
	}
}

func TestLayers(t *testing.T) {
	nat := file("ts-native", "ts", hardware.ModeNative, "arm64", br("a", benchmark.Throughput, 4, 100, "1"))
	vm := file("ts-vm", "ts", hardware.ModeVM, "arm64", br("a", benchmark.Throughput, 4, 90, "1"))
	rep := Build([]*result.File{nat, vm}, "")
	if len(rep.Layers) != 1 || math.Abs(rep.Layers[0].MicroPct["ts-vm"]+10) > 1e-9 {
		t.Fatalf("%+v", rep.Layers)
	}
}

func TestLayersFallBackToVM(t *testing.T) {
	vm := file("wsl", "wsl", hardware.ModeVM, "amd64", br("a", benchmark.Throughput, 2, 100, "1"))
	dk := file("wsl-docker", "wsl", hardware.ModeContainer, "amd64", br("a", benchmark.Throughput, 2, 95, "1"))
	only := file("pod", "k8s-only", hardware.ModeK8s, "amd64", br("a", benchmark.Throughput, 2, 95, "1"))
	only2 := file("pod2", "k8s-only", hardware.ModeContainer, "amd64", br("a", benchmark.Throughput, 2, 95, "1"))
	rep := Build([]*result.File{dk, vm, only, only2}, "")
	if len(rep.Layers) != 1 || rep.Layers[0].Base != "wsl" || rep.Layers[0].BaseMode != hardware.ModeVM ||
		math.Abs(rep.Layers[0].MicroPct["wsl-docker"]+5) > 1e-9 {
		t.Fatalf("%+v", rep.Layers)
	}
}

func TestLoadGenDiffers(t *testing.T) {
	web := func(host, cpu string) *result.Web {
		return &result.Web{KneeRPS: 100, LoadGen: &result.LoadGen{Hostname: host, CPU: cpu, LogicalCPUs: 32, Agents: 1}}
	}
	bi := &build.Info{Version: "v1"}
	x := &result.File{Label: "x86", Web: web("lg-dc1", "Xeon 6430"), Build: bi}
	a := &result.File{Label: "arm", Web: web("lg-dc2", "EPYC 9354"), Build: bi}
	rep := Build([]*result.File{x, a}, "x86")
	if len(rep.Findings) != 1 || rep.Findings[0].ID != "loadgen-differs" || rep.Findings[0].Severity != "warn" {
		t.Fatalf("%+v", rep.Findings)
	}
	a.Web = web("lg-dc1", "Xeon 6430")
	if rep := Build([]*result.File{x, a}, "x86"); len(rep.Findings) != 0 {
		t.Fatalf("same generator flagged: %+v", rep.Findings)
	}
}

func TestTCO(t *testing.T) {
	x := &result.File{Label: "x86", Web: &result.Web{KneeRPS: 10000, Steps: []result.WebStep{{TargetRPS: 10000, AchievedRPS: 10000, PassSLO: true, Watts: 400}}}}
	a := &result.File{Label: "arm", Web: &result.Web{KneeRPS: 15000, Steps: []result.WebStep{{TargetRPS: 15000, AchievedRPS: 15000, PassSLO: true, Watts: 300}}}}
	tco := ComputeTCO([]*result.File{x, a}, TCOInput{TargetRPS: 20000, Redundancy: 1, Servers: map[string]result.Cost{
		"x86": {ServerPrice: 400000, LifespanYears: 5, PowerRate: 4.5, PUE: 1.5},
		"arm": {ServerPrice: 450000, LifespanYears: 5, PowerRate: 4.5, PUE: 1.5},
	}})
	if tco.Rows[0].Servers != 3 || tco.Rows[1].Servers != 3 {
		t.Fatalf("%+v", tco.Rows)
	}
	wantEnergy := 3 * 400 * 1.5 * 8760 / 1000 * 4.5
	if math.Abs(tco.Rows[0].EnergyPerYr-wantEnergy) > 1e-6 {
		t.Fatalf("energy %v want %v", tco.Rows[0].EnergyPerYr, wantEnergy)
	}
	if tco.Rows[1].RPSPerWatt != 50 {
		t.Fatal(tco.Rows[1].RPSPerWatt)
	}
}

func TestWholeMachine(t *testing.T) {
	// Current machine: 2 × 16 cores at 400 W; candidate: 2 × 32 cores at 600 W.
	machine := func(label string, cores int, rate, watts float64) *result.File {
		f := file(label, label, hardware.ModeNative, "amd64",
			br("int/json", benchmark.Throughput, 1, 10, "1"),
			br("int/json", benchmark.Throughput, cores*2, rate, "1"),
			br("sync/mutex", benchmark.Throughput, cores*2, 1, "2"),
			br("mem/latency-32KB", benchmark.Latency, 1, 1, "3"))
		f.System.CPU = hardware.CPUInfo{Arch: "amd64", Sockets: 2, CoresPerSocket: cores, ThreadsPerCore: 1}
		f.System.Context.EffectiveCPUs = cores * 2
		f.Stress = &result.Stress{AvgWatts: watts}
		return f
	}
	cur, cand := machine("current", 16, 1000, 400), machine("vendora", 32, 1800, 600)
	cand.Benchmarks[2].Stats.Median = 0.5 // contended: must not count
	w := Build([]*result.File{cur, cand}, "current").Whole
	if w == nil {
		t.Fatal("no whole-machine comparison")
	}
	near := func(b Basis, want float64) {
		t.Helper()
		if got := w.Ratio[b]["vendora"]; math.Abs(got-want) > 1e-9 {
			t.Errorf("%s: got %v want %v", b, got, want)
		}
	}
	near(PerMachine, 1.8)
	near(PerCore, 0.9)
	near(PerSocket, 1.8)
	near(PerWatt, 1.8*400/600)
	if w.Workloads["vendora"] != 1 {
		t.Errorf("workloads %v: only int/json at max threads counts", w.Workloads)
	}
	w.AddPrices("current", map[string]result.Cost{
		"current": {ServerPrice: 1_000_000, Currency: "THB"},
		"vendora": {ServerPrice: 1_500_000, Currency: "THB"},
	})
	near(PerPrice, 1.2)

	// A VM has no physical sockets to divide by.
	cand.System.Context.InVM = true
	if w := Build([]*result.File{cur, cand}, "current").Whole; w.Ratio[PerSocket]["vendora"] != 0 {
		t.Errorf("VM sockets used: %v", w.Ratio[PerSocket])
	}
}
