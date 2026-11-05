package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/chaloemph/cpubench/internal/app"
	"github.com/chaloemph/cpubench/internal/domain/compare"
	"github.com/chaloemph/cpubench/internal/domain/doctor"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
	"github.com/chaloemph/cpubench/internal/workload"
)

func init() {
	register("info", "show detected CPU, topology and execution context", cmdInfo)
	register("doctor", "check the environment for things that skew results", cmdDoctor)
	register("list", "list built-in workloads", cmdList)
	register("run", "run the micro-benchmark suite", cmdRun)
	register("stress", "sustained all-core load: peak vs sustained, throttling, errors", cmdStress)
	register("power-curve", "power draw at 0/25/50/75/100% utilisation", cmdPowerCurve)
	register("compare", "compare result files and render a report", cmdCompare)
	register("report", "render a report for one or more result files (alias of compare)", cmdCompare)
	register("version", "print version", func(_ context.Context, d *Deps, _ []string) error {
		fmt.Fprintln(d.Stdout, "cpubench", d.App.Build)
		return nil
	})
}

func cmdInfo(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "info", "[--json]")
	asJSON := fs.Bool("json", false, "print the full snapshot as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, findings, err := d.App.Info(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(d.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			System   hardware.System  `json:"system"`
			Findings []doctor.Finding `json:"findings"`
		}{s, findings})
	}
	c := s.CPU
	p := func(k, format string, a ...any) { fmt.Fprintf(d.Stdout, "%-12s %s\n", k, fmt.Sprintf(format, a...)) }
	p("Host", "%s", s.Hostname)
	p("CPU", "%s · %s (%s)", c.Vendor, c.Model, c.Arch)
	if c.MIDR != nil {
		p("MIDR", "implementer %s part %s variant %s", c.MIDR.Implementer, c.MIDR.Part, c.MIDR.Variant)
	}
	p("Topology", "%d socket × %d cores × %d threads = %d logical · SMT %v", c.Sockets, c.CoresPerSocket, c.ThreadsPerCore, c.LogicalCPUs, s.Topology.SMTActive)
	for _, n := range s.Topology.NUMANodes {
		p(fmt.Sprintf("NUMA %d", n.ID), "cpus %s · %d MB · distances %v", cpuRanges(n.CPUs), n.MemTotalMB, n.Distances)
	}
	var caches []string
	for _, k := range c.Caches {
		caches = append(caches, fmt.Sprintf("L%d%s %dK/%d cpu", k.Level, map[string]string{"Data": "d", "Instruction": "i"}[k.Type], k.SizeKB, k.SharedCPUs))
	}
	p("Caches", "%s", strings.Join(caches, " · "))
	p("Max MHz", "%.0f", c.MaxMHz)
	p("Features", "%s", strings.Join(c.Features, " "))
	p("Memory", "%d MB · %d DIMMs populated · page %dK", s.Memory.TotalMB, len(s.Memory.DIMMs), s.Topology.PageSize/1024)
	p("Platform", "%s %s · virt %s", s.Platform.Vendor, s.Platform.Product, s.Platform.Virt)
	p("OS", "%s %s · kernel %s · governor %s · boost %s", s.OS.Name, s.OS.Version, s.OS.Kernel, orNA(s.OS.Governor), orNA(s.OS.Boost))
	p("Build", "cpubench %s", d.App.Build)
	cg := "none"
	if s.Context.Cgroup != nil {
		q := "unlimited"
		if s.Context.Cgroup.CPUQuotaCores > 0 {
			q = fmt.Sprintf("%.2f cores", s.Context.Cgroup.CPUQuotaCores)
		}
		cg = fmt.Sprintf("v%d quota %s · cpuset %s", s.Context.Cgroup.Version, q, cpuRanges(s.Context.Cgroup.Cpuset))
	}
	p("Context", "%s · effective CPUs %d · allowed %s · cgroup %s", s.Context.Mode, s.Context.EffectiveCPUs, cpuRanges(s.Topology.AllowedCPUs), cg)
	for _, n := range s.NICs {
		p("NIC", "%s · %d Mb/s · NUMA %d · %d rx queues · %s", n.Name, n.SpeedMbps, n.NUMANode, n.RxQueues, n.Driver)
	}
	fmt.Fprintln(d.Stdout)
	printFindings(d, findings)
	return nil
}

func orNA(s string) string {
	if s == "" {
		return "n/a"
	}
	return s
}

func cpuRanges(cpus []int) string {
	if len(cpus) == 0 {
		return "-"
	}
	var parts []string
	start, prev := cpus[0], cpus[0]
	flush := func() {
		if start == prev {
			parts = append(parts, fmt.Sprint(start))
		} else {
			parts = append(parts, fmt.Sprintf("%d-%d", start, prev))
		}
	}
	for _, c := range cpus[1:] {
		if c == prev+1 {
			prev = c
			continue
		}
		flush()
		start, prev = c, c
	}
	flush()
	return strings.Join(parts, ",")
}

func printFindings(d *Deps, fs []doctor.Finding) {
	if len(fs) == 0 {
		fmt.Fprintln(d.Stdout, "doctor: no findings")
		return
	}
	for _, f := range fs {
		fmt.Fprintf(d.Stdout, "doctor %-4s %s\n", f.Severity, f.Message)
		if f.Fix != "" {
			fmt.Fprintf(d.Stdout, "            → %s\n", f.Fix)
		}
	}
}

func cmdDoctor(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "doctor", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, findings, err := d.App.Info(ctx)
	if err != nil {
		return err
	}
	printFindings(d, findings)
	for _, f := range findings {
		if f.Severity == doctor.Fail {
			return fmt.Errorf("environment check failed")
		}
	}
	return nil
}

func cmdList(_ context.Context, d *Deps, args []string) error {
	for _, w := range workload.All() {
		i := w.Info()
		fmt.Fprintf(d.Stdout, "%-24s %-12s %-10s %s\n", i.Name, i.Category, i.Unit, i.Description)
	}
	return nil
}

func cmdRun(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "run", "[flags]")
	var o outputFlags
	o.register(fs, true)
	filter := fs.String("suite", "all", "workloads: all, a category (integer,float,crypto,memory,concurrency) or name prefixes, comma separated")
	threads := fs.String("threads", "1,max", "thread counts, e.g. 1,4,16,max")
	iters := fs.Int("iterations", 5, "measured iterations per point")
	warmup := fs.Int("warmup", 1, "warmup iterations (also first-touches memory)")
	scale := fs.Float64("scale", 1, "work per iteration multiplier (lower for quick checks)")
	pin := fs.Bool("pin", true, "pin each thread to its own CPU (cores first, then SMT siblings)")
	numaNode := fs.Int("numa-node", -1, "run only on CPUs of this NUMA node")
	numaMatrix := fs.Bool("numa-matrix", true, "measure cross-node memory latency on multi-node machines")
	if err := fs.Parse(args); err != nil {
		return err
	}
	tl, err := app.ParseThreads(*threads)
	if err != nil {
		return err
	}
	m, err := o.meter(d)
	if err != nil {
		return err
	}
	f, err := d.App.Run(ctx, app.RunOptions{Label: o.label, Machine: o.machine, Filter: *filter, Threads: tl,
		Iterations: *iters, Warmup: *warmup, Scale: *scale, Pin: *pin, NUMANode: *numaNode, NUMAMatrix: *numaMatrix, Power: m})
	if f == nil {
		return err
	}
	if err != nil {
		fmt.Fprintf(d.Stderr, "run stopped early: %v (saving partial result)\n", err)
	}
	return o.finish(ctx, d, f)
}

func cmdStress(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "stress", "[flags]")
	var o outputFlags
	o.register(fs, true)
	filter := fs.String("workloads", app.DefaultStressMix, "workloads to mix across threads")
	threads := fs.Int("threads", 0, "threads (0 = all usable CPUs)")
	dur := fs.Duration("duration", 10*time.Minute, "how long to run (use 30m-1h for real tests)")
	interval := fs.Duration("interval", 5*time.Second, "sampling interval")
	scale := fs.Float64("scale", 0.25, "work per kernel call (smaller = finer sampling)")
	pin := fs.Bool("pin", true, "pin threads")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := o.meter(d)
	if err != nil {
		return err
	}
	f, err := d.App.Stress(ctx, app.StressOptions{Label: o.label, Machine: o.machine, Filter: *filter, Threads: *threads,
		Duration: *dur, Interval: *interval, Scale: *scale, Pin: *pin, Power: m})
	if err != nil {
		return err
	}
	s := f.Stress
	fmt.Fprintf(d.Stdout, "peak %.1f it/s · sustained %.1f it/s (%+.1f%%) · MHz %.0f–%.0f · errors %d · kernel hw errors %d\n",
		s.PeakRate, s.SustainedRate, s.DegradationPct, s.MinMHz, s.MaxMHz, s.Errors, len(s.KernelErrors))
	return o.finish(ctx, d, f)
}

func cmdPowerCurve(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "power-curve", "--power <source> [flags]")
	var o outputFlags
	o.register(fs, true)
	wl := fs.String("workload", "int/json", "workload used to generate load")
	levels := fs.String("levels", "0,25,50,75,100", "utilisation levels in %")
	per := fs.Duration("per-level", time.Minute, "measurement time per level")
	settle := fs.Duration("settle", 10*time.Second, "time to settle before measuring each level")
	pin := fs.Bool("pin", true, "pin threads")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lv, err := parseFloats(*levels)
	if err != nil {
		return err
	}
	m, err := o.meter(d)
	if err != nil {
		return err
	}
	f, err := d.App.PowerCurve(ctx, app.PowerCurveOptions{Label: o.label, Machine: o.machine, Workload: *wl, Levels: lv,
		PerLevel: *per, Settle: *settle, Pin: *pin, Power: m})
	if err != nil {
		return err
	}
	return o.finish(ctx, d, f)
}

func cmdCompare(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "compare", "[flags] <result.json | results-dir | http://hub:8090> ...")
	baseline := fs.String("baseline", "", "label of the baseline run, e.g. the machine in production now (default: first result given)")
	out := fs.String("o", "report.html", "HTML report path ('' to skip)")
	md := fs.String("md", "", "Markdown report path")
	tco := fs.String("tco", "", "TCO cost model YAML (see examples/tco.yaml)")
	title := fs.String("title", "", "report title")
	lang := fs.String("lang", "th", "report language: th or en")
	latest := fs.Bool("latest", false, "keep only the newest result per label (handy with a results directory)")
	share := fs.String("for", "", "prepare a copy to send to a vendor: comma-separated labels or machines to keep (the baseline's machine is always kept)")
	others := fs.String("others", string(app.OthersDrop), "with --for, what to do with every other machine: drop or anonymize")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		fs.Usage()
		return fmt.Errorf("no result files given")
	}
	var files []*result.File
	for _, ref := range expandResultArgs(fs.Args()) {
		if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
			got, err := d.FetchHub(ctx, ref)
			if err != nil {
				return fmt.Errorf("%s: %w", ref, err)
			}
			fmt.Fprintf(d.Stdout, "fetched %d result(s) from %s\n", len(got), ref)
			files = append(files, got...)
			continue
		}
		f, err := d.Store.Load(ctx, ref)
		if err != nil {
			return err
		}
		files = append(files, f)
	}
	if *latest {
		files = app.LatestPerLabel(files)
	}
	files = app.MergeByLabel(files)
	var guessed []string
	if *baseline == "" {
		*baseline = app.DefaultBaseline(files)
		if guessed = app.AmbiguousBaseline(files); guessed != nil {
			fmt.Fprintf(d.Stderr, "warning: baseline %q picked automatically among machines %s; pass --baseline <label>\n",
				*baseline, strings.Join(guessed, ", "))
		}
	}
	opts := port.ReportOptions{Baseline: *baseline, BaselineGuessed: guessed, Title: *title, Lang: *lang}
	if *share != "" {
		var err error
		files, opts.Anonymized, err = app.ShareCopy(files, *baseline, strings.Split(*share, ","), app.Others(*others))
		if err != nil {
			return err
		}
		opts.Shared = true
		opts.BaselineGuessed = nil // the list names every machine tested
	}
	if *tco != "" {
		in, err := loadTCO(*tco)
		if err != nil {
			return err
		}
		if opts.Shared {
			in = in.Only(files) // other vendors' prices never leave
		}
		opts.TCO = in
	}
	if *out != "" {
		if err := renderTo(*out, d.HTML, files, opts); err != nil {
			return err
		}
		fmt.Fprintf(d.Stdout, "report: %s\n", *out)
	}
	if *md != "" {
		if err := renderTo(*md, d.Markdown, files, opts); err != nil {
			return err
		}
		fmt.Fprintf(d.Stdout, "report: %s\n", *md)
	}
	cmp := compare.Build(files, *baseline)
	for _, l := range sortedKeys(cmp.Overall) {
		if l != cmp.Baseline && l != "web:"+cmp.Baseline {
			fmt.Fprintf(d.Stdout, "%-28s %+.1f%% vs %s\n", l, (cmp.Overall[l]-1)*100, cmp.Baseline)
		}
	}
	return nil
}

func loadTCO(path string) (*compare.TCOInput, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var in compare.TCOInput
	if err := yaml.Unmarshal(b, &in); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &in, nil
}

func init() {
	register("power", "read or record power from a BMC/sensor (check access, or trace a VM run from the host)", cmdPower)
}

func cmdPower(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "power", "--power <source> [--duration 10m]")
	var o outputFlags
	o.register(fs, true)
	dur := fs.Duration("duration", 0, "record for this long and save a result (0 = one reading)")
	every := fs.Duration("every", time.Second, "sampling interval")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := o.meter(d)
	if err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("--power is required")
	}
	if *dur == 0 {
		w, err := m.Watts(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(d.Stdout, "%s: %.1f W\n", m.Source(), w)
		return nil
	}
	f, err := d.App.RecordPower(ctx, o.label, o.machine, m, *dur, *every)
	if err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "avg %.1f W · max %.1f W · %d samples\n", f.Power.Avg, f.Power.Max, len(f.Power.Samples))
	return o.finish(ctx, d, f)
}
