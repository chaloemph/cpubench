package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chaloemph/cpubench/internal/app"
	"github.com/chaloemph/cpubench/internal/domain/load"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
)

func init() {
	register("auto", "run everything for this machine in one go and write one result + report", cmdAuto)
}

func cmdAuto(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "auto", "[--profile quick|standard|full] [--power SRC] [--target URL]")
	var o outputFlags
	o.register(fs, true)
	profile := fs.String("profile", "standard", "smoke (~1 min check), quick (~5 min), standard (~25 min), full (~1.5 h)")
	target := fs.String("target", "", "also load test this web target (run `cpubench web serve` there)")
	endpoint := fs.String("endpoint", "mix", "web request preset for --target")
	p99 := fs.Float64("slo-p99", 100, "web SLO: p99 ms")
	conc := fs.Int("concurrency", 512, "web: max in-flight requests")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := o.meter(d)
	if err != nil {
		return err
	}
	opts := app.AutoOptions{Label: o.label, Machine: o.machine, Profile: *profile, Power: m}
	if *target != "" {
		w, err := webOptions(d, *target, *endpoint, *p99, *conc)
		if err != nil {
			return err
		}
		opts.Web = w
	}
	if p, ok := app.Profiles[*profile]; ok {
		fmt.Fprintf(d.Stdout, "cpubench auto · profile %s · stress %s · power %v · web %v\n", p.Name, p.Stress, m != nil, *target != "")
	}
	start := time.Now()
	f, err := d.App.Auto(ctx, opts)
	if f == nil {
		return err
	}
	if err != nil {
		fmt.Fprintf(d.Stderr, "auto stopped early: %v (saving partial result)\n", err)
	}
	if o.html == "" {
		o.html = fmt.Sprintf("cpubench-%s-%s.html", safeName(f.Label), *profile)
	}
	fmt.Fprintf(d.Stdout, "done in %s\n", time.Since(start).Round(time.Second))
	return o.finish(ctx, d, f)
}

// webOptions builds a ramp load test against target with a request preset.
func webOptions(d *Deps, target, endpoint string, p99 float64, conc int) (*app.LoadOptions, error) {
	reqs, ok := presets[endpoint]
	if !ok {
		return nil, fmt.Errorf("unknown endpoint preset %q", endpoint)
	}
	sc := load.Scenario{Target: target, Profile: load.Ramp, SLO: result.SLO{P99Ms: p99}, Concurrency: conc,
		Warmup: 10 * time.Second, Requests: append([]load.RequestSpec(nil), reqs...), Name: "preset " + endpoint}
	sc.Ramp = load.RampSpec{Start: 1000, Max: 1_000_000, Adaptive: true}
	sc.Defaults()
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	bodies, err := resolveBodies(sc.Requests, ".")
	if err != nil {
		return nil, err
	}
	return &app.LoadOptions{Scenario: sc, Bodies: bodies, SUT: d.Web.NewSUT(target),
		Agents: []port.LoadAgent{app.LocalAgent{Target: d.Web.NewTarget(sc.Concurrency, true)}}}, nil
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, s)
}

// expandResultArgs turns directories into their *.json files.
func expandResultArgs(args []string) []string {
	var out []string
	for _, a := range args {
		if st, err := os.Stat(a); err == nil && st.IsDir() {
			m, _ := filepath.Glob(filepath.Join(a, "*.json"))
			out = append(out, m...)
			continue
		}
		out = append(out, a)
	}
	return out
}
