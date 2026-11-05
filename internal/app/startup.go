package app

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/load"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/domain/scan"
	"github.com/chaloemph/cpubench/internal/port"
)

type StartupOptions struct {
	Label, Machine string
	Command        string
	ReadyURL       string
	WarmURL        string // measured after ready; defaults to ReadyURL
	Runs           int
	Warmup         time.Duration
	Timeout        time.Duration
	Process        port.ProcessRunner
	HTTP           port.LoadTarget
}

// Startup measures time from process start to the first 200 on ReadyURL, and
// how long the app takes to reach steady-state latency (JIT warmup). Slower
// cores stretch both, which matters for autoscaling and rolling deploys.
func (a *App) Startup(ctx context.Context, o StartupOptions) (*result.File, error) {
	f, err := a.newFile(ctx, o.Label, o.Machine, "startup")
	if err != nil {
		return nil, err
	}
	if o.WarmURL == "" {
		o.WarmURL = o.ReadyURL
	}
	st := &result.Startup{Command: o.Command, ReadyURL: o.ReadyURL}
	for run := range o.Runs {
		t0 := time.Now()
		stop, err := o.Process.Start(ctx, o.Command)
		if err != nil {
			return nil, fmt.Errorf("start %q: %w", o.Command, err)
		}
		ready := false
		for time.Since(t0) < o.Timeout && ctx.Err() == nil {
			if code, err := o.HTTP.Do(ctx, load.Request{Method: "GET", URL: o.ReadyURL}); err == nil && code == 200 {
				ready = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !ready {
			stop()
			return nil, fmt.Errorf("not ready within %s", o.Timeout)
		}
		ms := float64(time.Since(t0).Microseconds()) / 1000
		st.ReadyMs = append(st.ReadyMs, ms)
		a.logf("startup run %d: ready in %.0f ms", run+1, ms)
		if run == o.Runs-1 && o.Warmup > 0 {
			st.Warmup, st.WarmupSec = a.warmupCurve(ctx, o)
		}
		stop()
		time.Sleep(time.Second)
	}
	sorted := slices.Clone(st.ReadyMs)
	slices.Sort(sorted)
	st.ReadyMedianMs = benchmark.Median(sorted)
	f.Startup = st
	f.FinishedAt = time.Now().UTC()
	return f, nil
}

// warmupCurve sends sequential requests for o.Warmup and returns the median
// latency per second plus the first second within 10% of the final level.
func (a *App) warmupCurve(ctx context.Context, o StartupOptions) ([]result.WarmupSecond, float64) {
	var out []result.WarmupSecond
	start := time.Now()
	for sec := 0; time.Since(start) < o.Warmup && ctx.Err() == nil; sec++ {
		var lat []float64
		for time.Since(start) < time.Duration(sec+1)*time.Second {
			t := time.Now()
			if code, err := o.HTTP.Do(ctx, load.Request{Method: "GET", URL: o.WarmURL}); err == nil && code < 400 {
				lat = append(lat, float64(time.Since(t).Microseconds())/1000)
			}
		}
		slices.Sort(lat)
		out = append(out, result.WarmupSecond{Sec: sec + 1, P50Ms: benchmark.Median(lat)})
	}
	if len(out) < 3 {
		return out, 0
	}
	var tail []float64
	for _, w := range out[len(out)-3:] {
		tail = append(tail, w.P50Ms)
	}
	slices.Sort(tail)
	final := benchmark.Median(tail)
	for _, w := range out {
		if w.P50Ms <= final*1.1 {
			return out, float64(w.Sec)
		}
	}
	return out, float64(len(out))
}

// ScanReport says whether images and directories can run on Arch.
type ScanReport struct {
	Arch     string        `json:"arch"`
	Images   []scan.Image  `json:"images,omitempty"`
	Binaries []scan.Binary `json:"binaries,omitempty"`
}

// Scan checks whether images and directories can run on the target
// architecture (amd64 or arm64).
func (a *App) Scan(ctx context.Context, imgs port.ImageInspector, files port.BinaryScanner, arch string, refs, dirs []string) (*ScanReport, error) {
	if err := scan.ValidArch(arch); err != nil {
		return nil, err
	}
	rep := &ScanReport{Arch: arch}
	for _, ref := range refs {
		p, err := imgs.Platforms(ctx, ref)
		rep.Images = append(rep.Images, scan.JudgeImage(ref, p, err, arch))
	}
	for _, d := range dirs {
		bs, err := files.Scan(ctx, d)
		if err != nil {
			return rep, err
		}
		for _, b := range bs {
			rep.Binaries = append(rep.Binaries, scan.JudgeBinary(b, arch))
		}
	}
	return rep, nil
}
