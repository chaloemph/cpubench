package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/build"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
)

func run(label, machine, mode, command string, at int, medians ...float64) *result.File {
	f := &result.File{Label: label, ID: label, Machine: machine, Command: command,
		CreatedAt: time.Date(2026, 11, 5, at, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 11, 5, at, 30, 0, 0, time.UTC),
		Build: &build.Info{Version: "v1", Commit: "abc", GoVersion: "go1.26.3", GOARCH: "amd64", ArchLevel: "GOAMD64=v1"}}
	f.System.Context.Mode = hardware.Mode(mode)
	if mode == "vm" {
		f.System.Platform.Virt = "kvm"
	}
	for i, m := range medians {
		f.Benchmarks = append(f.Benchmarks, benchmark.Result{Name: []string{"int/json", "mem/triad"}[i], Threads: 8,
			Checksum: "c" + strings.Repeat("x", i), ChecksumStable: true, Stats: benchmark.Stats{N: 5, Median: m, CV: 0.5}})
	}
	return f
}

func TestGroupRepeatsFoldsRunsOfOneMachine(t *testing.T) {
	files := []*result.File{
		run("x86-01-r2", "x86-01", "vm", "auto standard", 3, 110, 50),
		run("x86-01-r1", "x86-01", "vm", "auto standard", 1, 100, 40),
		run("x86-01-r3", "x86-01", "vm", "auto standard", 5, 90, 60),
		run("arm-01", "arm-01", "vm", "auto standard", 2, 80, 70),
	}
	out, groups := GroupRepeats(files)
	if len(out) != 2 || out[0].Label != "x86-01" || out[1].Label != "arm-01" {
		t.Fatalf("columns %v", labels(out))
	}
	if len(groups) != 1 || !slices.Equal(groups[0].Members, []string{"x86-01-r1", "x86-01-r2", "x86-01-r3"}) {
		t.Fatalf("groups %+v (members must be oldest first)", groups)
	}
	g := out[0]
	if !slices.Equal(g.Runs, groups[0].Members) || !g.CreatedAt.Equal(files[1].CreatedAt) || !g.FinishedAt.Equal(files[2].FinishedAt) {
		t.Fatalf("identity: runs %v created %v finished %v", g.Runs, g.CreatedAt, g.FinishedAt)
	}
	j := g.Benchmarks[0]
	if j.Stats.N != 3 || j.Stats.Median != 100 || !slices.Equal(j.Samples, []float64{100, 110, 90}) || !j.ChecksumStable {
		t.Fatalf("int/json folded wrong: %+v", j)
	}
	// The spread is now between runs: 90..110 around 100 is far above the
	// 0.5% each run had on its own.
	if j.Stats.CV < 5 || j.Stats.Min != 90 || j.Stats.Max != 110 {
		t.Fatalf("spread between runs: %+v", j.Stats)
	}
	if out[1] != files[3] {
		t.Fatal("a machine with one run must pass through unchanged")
	}
	if files[0].Label != "x86-01-r2" || len(files[0].Runs) != 0 {
		t.Fatal("GroupRepeats changed its input")
	}
}

func TestGroupRepeatsKeepsLayersAndProfilesApart(t *testing.T) {
	files := []*result.File{
		run("arm-01-native", "arm-01", "native", "auto standard", 1, 100),
		run("arm-01-vm", "arm-01", "vm", "auto standard", 2, 90),
		run("arm-01-full", "arm-01", "native", "auto full", 3, 100),
	}
	out, groups := GroupRepeats(files)
	if len(out) != 3 || len(groups) != 0 {
		t.Fatalf("different layers or commands are not repeats: %v %+v", labels(out), groups)
	}
}

func TestGroupRepeatsFlagsDifferentChecksumsAndBuilds(t *testing.T) {
	a := run("x-1", "x", "vm", "auto standard", 1, 100)
	b := run("x-2", "x", "vm", "auto standard", 2, 100)
	b.Benchmarks[0].Checksum = "other"
	b.Build.Commit = "def"
	b.System.OS.Kernel = "6.9"
	out, _ := GroupRepeats([]*result.File{a, b})
	if len(out) != 1 || out[0].Label != "x" {
		t.Fatalf("columns %v", labels(out))
	}
	if out[0].Benchmarks[0].ChecksumStable {
		t.Error("runs with different checksums did not do the same work")
	}
	var ids []string
	for _, f := range out[0].Findings {
		ids = append(ids, f.ID)
	}
	if !slices.Contains(ids, "build-version") {
		t.Errorf("runs from different commits must be flagged, got %v", ids)
	}
	if len(ids) < 2 {
		t.Errorf("a different kernel between runs must be flagged too, got %v", ids)
	}
}

func TestGroupLabel(t *testing.T) {
	for _, c := range []struct {
		labels []string
		want   string
	}{
		{[]string{"x86-01-r1", "x86-01-r2"}, "x86-01"},
		{[]string{"arm-std-1", "arm-std-2"}, "arm-std"},
		{[]string{"a-10", "a-11"}, "a"},
		{[]string{"vm_run1", "vm_run2"}, "vm"},
		{[]string{"alpha", "beta"}, ""},
	} {
		var fs []*result.File
		for _, l := range c.labels {
			fs = append(fs, &result.File{Label: l})
		}
		if got := groupLabel(fs); got != c.want {
			t.Errorf("groupLabel(%v) = %q, want %q", c.labels, got, c.want)
		}
	}
	// A label nobody shares falls back to the machine, and never collides.
	used := map[string]bool{"x86-01": true}
	if got := uniqueLabel("", "x86-01", used); got != "x86-01-2" {
		t.Errorf("uniqueLabel = %q", got)
	}
	if got := uniqueLabel("", "box", map[string]bool{}); got != "box" {
		t.Errorf("uniqueLabel fallback = %q", got)
	}
}

func labels(fs []*result.File) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Label)
	}
	return out
}
