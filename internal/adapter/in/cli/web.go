package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/chaloemph/cpubench/internal/app"
	"github.com/chaloemph/cpubench/internal/domain/load"
	"github.com/chaloemph/cpubench/internal/port"
)

// WebDeps are the factories the web commands need.
type WebDeps struct {
	NewTarget  func(concurrency int, keepAlive bool) port.LoadTarget
	NewSUT     func(target string) port.SUTStats
	NewAgent   func(addr string) port.LoadAgent
	ServeWeb   func(ctx context.Context, addr, tlsKey string, power port.PowerMeter) error
	ServeAgent func(ctx context.Context, addr string) error
	ServeHub   func(ctx context.Context, addr, dir, token string) error
}

func init() {
	register("web serve", "run the built-in web API that load tests target", cmdWebServe)
	register("web load", "load test a web target: ramp to SLO, constant/soak, stress, spike", cmdWebLoad)
	register("web agent", "run a remote load generator for distributed `web load --agents`", cmdWebAgent)
}

func cmdWebServe(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "web serve", "[--addr :8080] [--tls ecdsa|rsa] [--power SRC]")
	addr := fs.String("addr", ":8080", "listen address")
	tlsKey := fs.String("tls", "", "serve HTTPS with a self-signed ecdsa or rsa certificate")
	powerSrc := fs.String("power", "", "this server's power source, reported to load generators (redfish://..., ipmi://local, ...)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var m port.PowerMeter
	if *powerSrc != "" {
		var err error
		if m, err = d.OpenPower(*powerSrc); err != nil {
			return err
		}
	}
	scheme := "http"
	if *tlsKey != "" {
		scheme = "https"
	}
	fmt.Fprintf(d.Stdout, "serving %s on %s (endpoints: /plaintext /json /json/parse /html /gzip /login /cache /_cpubench/*)\n", scheme, *addr)
	return d.Web.ServeWeb(ctx, *addr, *tlsKey, m)
}

func cmdWebAgent(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "web agent", "[--listen :7070]")
	addr := fs.String("listen", ":7070", "listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "load agent listening on %s\n", *addr)
	return d.Web.ServeAgent(ctx, *addr)
}

// Endpoint presets for --endpoint, matching `web serve`.
var presets = map[string][]load.RequestSpec{
	"/plaintext":  {{Method: "GET", Path: "/plaintext"}},
	"/json":       {{Method: "GET", Path: "/json?id={{rand 1 100000}}"}},
	"/json/parse": {{Method: "POST", Path: "/json/parse", BodyFile: "@products", Headers: map[string]string{"Content-Type": "application/json"}}},
	"/html":       {{Method: "GET", Path: "/html"}},
	"/gzip":       {{Method: "GET", Path: "/gzip", Headers: map[string]string{"Accept-Encoding": "gzip"}}},
	"/login":      {{Method: "POST", Path: "/login", Body: "user={{rand 1 1000}}", Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}}},
	"/cache":      {{Method: "GET", Path: "/cache?key={{rand 0 9999}}"}},
	"mix": {
		{Weight: 40, Method: "GET", Path: "/json?id={{rand 1 100000}}"},
		{Weight: 15, Method: "GET", Path: "/html"},
		{Weight: 22, Method: "GET", Path: "/cache?key={{rand 0 9999}}"},
		{Weight: 3, Method: "GET", Path: "/cache?key={{rand 0 9999}}&write=1"},
		{Weight: 10, Method: "GET", Path: "/gzip", Headers: map[string]string{"Accept-Encoding": "gzip"}},
		{Weight: 7, Method: "POST", Path: "/json/parse", BodyFile: "@products", Headers: map[string]string{"Content-Type": "application/json"}},
		{Weight: 3, Method: "POST", Path: "/login", Body: "user={{rand 1 1000}}", Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}},
	},
}

func productsBody() []byte {
	type p struct {
		ID    int     `json:"id"`
		Name  string  `json:"name"`
		Price float64 `json:"price"`
		Stock int     `json:"stock"`
	}
	items := make([]p, 50)
	for i := range items {
		items[i] = p{ID: i, Name: fmt.Sprintf("Product %d", i), Price: float64(i) * 9.5, Stock: i % 7}
	}
	b, _ := json.Marshal(items)
	return b
}

func cmdWebLoad(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "web load", "--target URL [flags]")
	var o outputFlags
	o.register(fs, true)
	scenarioPath := fs.String("scenario", "", "scenario YAML (see examples/scenario-*.yaml); flags below override it")
	target := fs.String("target", "", "base URL of the server under test, e.g. http://10.0.0.20:8080")
	endpoint := fs.String("endpoint", "mix", "built-in request preset: mix, /json, /plaintext, /html, /gzip, /login, /cache, /json/parse")
	profile := fs.String("profile", "ramp", "ramp | constant | stress | spike")
	rate := fs.Float64("rate", 0, "requests/s for constant, stress (overload rate) and spike (base rate)")
	peak := fs.Float64("peak", 0, "spike peak rate (default 3x --rate)")
	duration := fs.Duration("duration", time.Minute, "constant/stress phase length (soak: 4h-8h)")
	ramp := fs.String("ramp", "500:500:200000", "ramp start:step:max requests/s")
	stepDur := fs.Duration("step-duration", 20*time.Second, "ramp step length")
	p99 := fs.Float64("slo-p99", 100, "SLO: p99 latency in ms")
	errRate := fs.Float64("slo-errors", 0.001, "SLO: max error rate (0.001 = 0.1%)")
	conc := fs.Int("concurrency", 512, "max in-flight requests (total across agents)")
	keepAlive := fs.Bool("keepalive", true, "reuse connections (false = new TCP/TLS connection per request)")
	warmup := fs.Duration("warmup", 10*time.Second, "warmup before measuring (JIT, caches)")
	agents := fs.String("agents", "", "comma-separated 'web agent' addresses for distributed load (default: this process)")
	adaptive := fs.Bool("adaptive", false, "ramp by doubling the rate, then refine near the knee (fewer steps on big servers)")
	sutStats := fs.Bool("sut-stats", true, "read SUT CPU and system info from /_cpubench/* when the target is 'web serve'")
	if err := fs.Parse(args); err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	var sc load.Scenario
	if *scenarioPath != "" {
		b, err := os.ReadFile(*scenarioPath)
		if err != nil {
			return err
		}
		if err := yaml.Unmarshal(b, &sc); err != nil {
			return fmt.Errorf("%s: %w", *scenarioPath, err)
		}
		if sc.Name == "" {
			sc.Name = filepath.Base(*scenarioPath)
		}
	}
	apply := func(name string, fn func()) {
		if set[name] || *scenarioPath == "" {
			fn()
		}
	}
	apply("target", func() { sc.Target = *target })
	apply("profile", func() { sc.Profile = load.Profile(*profile) })
	apply("rate", func() { sc.Rate = *rate })
	apply("peak", func() { sc.Spike.Peak = *peak })
	apply("duration", func() { sc.Duration = *duration })
	apply("step-duration", func() { sc.Ramp.StepDuration = *stepDur })
	apply("slo-p99", func() { sc.SLO.P99Ms = *p99 })
	apply("slo-errors", func() { sc.SLO.ErrorRate = *errRate })
	apply("concurrency", func() { sc.Concurrency = *conc })
	apply("keepalive", func() { sc.KeepAlive = keepAlive })
	apply("warmup", func() { sc.Warmup = *warmup })
	apply("adaptive", func() { sc.Ramp.Adaptive = *adaptive })
	apply("ramp", func() {
		var start, step, mx float64
		if _, err := fmt.Sscanf(*ramp, "%g:%g:%g", &start, &step, &mx); err == nil {
			sc.Ramp.Start, sc.Ramp.Step, sc.Ramp.Max = start, step, mx
		}
	})
	if len(sc.Requests) == 0 || set["endpoint"] {
		reqs, ok := presets[*endpoint]
		if !ok {
			return fmt.Errorf("unknown endpoint preset %q", *endpoint)
		}
		sc.Requests = append([]load.RequestSpec(nil), reqs...)
		if sc.Name == "" {
			sc.Name = "preset " + *endpoint
		}
	}
	sc.Defaults()
	if err := sc.Validate(); err != nil {
		return err
	}
	bodies, err := resolveBodies(sc.Requests, filepath.Dir(*scenarioPath))
	if err != nil {
		return err
	}

	opts := app.LoadOptions{Label: o.label, Machine: o.machine, Scenario: sc, Bodies: bodies}
	if *agents != "" {
		for a := range strings.SplitSeq(*agents, ",") {
			opts.Agents = append(opts.Agents, d.Web.NewAgent(strings.TrimSpace(a)))
		}
	} else {
		opts.Agents = []port.LoadAgent{app.LocalAgent{Target: d.Web.NewTarget(sc.Concurrency, *sc.KeepAlive)}}
	}
	if *sutStats {
		opts.SUT = d.Web.NewSUT(sc.Target)
	}
	if opts.Power, err = o.meter(d); err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "load test %s · profile %s · SLO p99 ≤ %.0f ms, errors ≤ %.2f%% · %d agent(s)\n",
		sc.Target, sc.Profile, sc.SLO.P99Ms, sc.SLO.ErrorRate*100, len(opts.Agents))
	f, err := d.App.Load(ctx, opts)
	if f == nil {
		return err
	}
	if err != nil {
		fmt.Fprintf(d.Stderr, "load test stopped early: %v (saving partial result)\n", err)
	}
	fmt.Fprintf(d.Stdout, "max RPS @ SLO: %.0f", f.Web.KneeRPS)
	if f.Web.RecoverySec != 0 {
		fmt.Fprintf(d.Stdout, " · recovery %.1fs", f.Web.RecoverySec)
	}
	fmt.Fprintln(d.Stdout)
	return o.finish(ctx, d, f)
}

func resolveBodies(reqs []load.RequestSpec, dir string) ([][]byte, error) {
	out := make([][]byte, len(reqs))
	for i, r := range reqs {
		switch {
		case r.BodyFile == "@products":
			out[i] = productsBody()
		case r.BodyFile != "":
			p := r.BodyFile
			if !filepath.IsAbs(p) {
				p = filepath.Join(dir, p)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			out[i] = b
		default:
			out[i] = []byte(r.Body)
		}
	}
	return out, nil
}

func init() {
	register("hub", "collect results from many machines and compare them in a browser", cmdHub)
}

func cmdHub(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "hub", "[--listen :8090] [--dir hubdata] [--token T]")
	addr := fs.String("listen", ":8090", "listen address (8090 so it never clashes with 'web serve' on 8080)")
	dir := fs.String("dir", "hubdata", "directory for uploaded results")
	token := fs.String("token", os.Getenv("CPUBENCH_TOKEN"), "token required to upload ($CPUBENCH_TOKEN; empty = open)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *token == "" {
		fmt.Fprintln(d.Stderr, "warning: no --token, anyone who can reach the hub can upload")
	}
	fmt.Fprintf(d.Stdout, "hub on %s, storing in %s\n", *addr, *dir)
	return d.Web.ServeHub(ctx, *addr, *dir, *token)
}
