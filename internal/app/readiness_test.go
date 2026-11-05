package app

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/load"
	"github.com/chaloemph/cpubench/internal/domain/scan"
)

type fakeProc struct{ started, stopped atomic.Int32 }

func (p *fakeProc) Start(ctx context.Context, cmd string) (func() error, error) {
	p.started.Add(1)
	return func() error { p.stopped.Add(1); return nil }, nil
}

// readyAfter returns 503 for the first n calls, then 200.
type readyAfter struct{ n, calls atomic.Int32 }

func (r *readyAfter) Do(ctx context.Context, q load.Request) (int, error) {
	if r.calls.Add(1) <= r.n.Load() {
		return 503, nil
	}
	return 200, nil
}

func TestStartup(t *testing.T) {
	a := twoNodeApp()
	p := &fakeProc{}
	h := &readyAfter{}
	h.n.Store(3)
	f, err := a.Startup(context.Background(), StartupOptions{Command: "x", ReadyURL: "http://x/health", Runs: 2,
		Warmup: 3 * time.Second, Timeout: 5 * time.Second, Process: p, HTTP: h})
	if err != nil {
		t.Fatal(err)
	}
	s := f.Startup
	if p.started.Load() != 2 || p.stopped.Load() != 2 || len(s.ReadyMs) != 2 || s.ReadyMs[0] < 25 {
		t.Fatalf("%+v started %d stopped %d", s, p.started.Load(), p.stopped.Load())
	}
	if len(s.Warmup) != 3 || s.WarmupSec < 1 {
		t.Fatalf("warmup %+v %v", s.Warmup, s.WarmupSec)
	}
}

func TestDensity(t *testing.T) {
	a := twoNodeApp()
	f, err := a.Density(context.Background(), DensityOptions{Workload: "int/sort", GroupSize: 1, Iterations: 1, Scale: 0.01})
	if err != nil {
		t.Fatal(err)
	}
	d := f.Density
	if len(d.Rows) != 2 || d.Rows[0].RelPct != 100 || d.Rows[1].Instances != 2 || d.MaxInstances < 1 {
		t.Fatalf("%+v", d)
	}
	if got := densitySteps(40); got[len(got)-1] != 40 || got[7] != 8 || got[8] != 12 {
		t.Fatalf("steps %v", got)
	}
}

type imgs map[string][]string

func (m imgs) Platforms(ctx context.Context, ref string) ([]string, error) {
	p, ok := m[ref]
	if !ok {
		return nil, errors.New("not found")
	}
	return p, nil
}

type bins []scan.Binary

func (b bins) Scan(ctx context.Context, root string) ([]scan.Binary, error) { return b, nil }

func TestScan(t *testing.T) {
	a := twoNodeApp()
	images := imgs{"a": {"linux/amd64", "linux/arm64"}, "b": {"linux/amd64"}, "d": {"linux/arm64"}}
	files := bins{{Path: "x.node", Arch: "amd64"}, {Path: "y.so", Arch: "arm64"}}
	want := map[string][]scan.Status{
		"arm64": {scan.Ready, scan.Blocked, scan.Check, scan.Ready, scan.Blocked, scan.Ready},
		"amd64": {scan.Ready, scan.Ready, scan.Check, scan.Blocked, scan.Ready, scan.Blocked},
	}
	for arch, w := range want {
		rep, err := a.Scan(context.Background(), images, files, arch, []string{"a", "b", "c", "d"}, []string{"/app"})
		if err != nil {
			t.Fatal(err)
		}
		got := []scan.Status{rep.Images[0].Status, rep.Images[1].Status, rep.Images[2].Status, rep.Images[3].Status,
			rep.Binaries[0].Status, rep.Binaries[1].Status}
		if rep.Arch != arch || !slices.Equal(got, w) {
			t.Fatalf("%s: %v want %v", arch, got, w)
		}
	}
	if _, err := a.Scan(context.Background(), images, files, "", nil, nil); err == nil {
		t.Fatal("a scan without a target architecture must fail")
	}
}
