// Package report renders result files as a self-contained HTML page or as
// Markdown for wikis and pull requests.
package report

import (
	"fmt"
	"html/template"
	"math"
	"slices"
	"strings"
	"time"

	"maps"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/compare"
	"github.com/chaloemph/cpubench/internal/domain/doctor"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
	"github.com/chaloemph/cpubench/internal/workload"
)

type card struct {
	Label, Value, Sub, Tone string
}

type cell struct {
	Value string
	CV    string
	Noisy bool
	// Backend is the implementation a crypto kernel ran, e.g. "hardware · SHA-NI".
	Backend  string
	Fallback bool // generic Go code instead of dedicated instructions
}

type spCell struct {
	Text, Class string
	// Range is the lowest and highest row ratio behind a category ratio, so a
	// mean of rows that agree reads differently from a mean of rows that don't.
	Range string
}

type tableRow struct {
	Name, Unit, Category string
	Threads              int
	Cells                []cell
	Speedups             []spCell
	Mismatch             bool
	BackendDiffers       bool
}

type findingGroup struct {
	Label    string
	Findings []doctor.Finding
}

type envRow struct {
	Key    string
	Values []string
}

// caveat is a condition that decides how far the numbers may be read.
type caveat struct {
	Severity doctor.Severity
	Text     string
}

// term explains a word the labels use, so the report can be read without the
// findings documents next to it.
type term struct {
	Name, Intro string
	Points      []string
}

// catRow is a category ratio next to the reason to distrust it, if any.
type catRow struct {
	Category string
	// Anchor is the id of the category's rows in the benchmark table: a
	// category mixes units, so its measured values exist only row by row.
	Anchor string
	Cells  []spCell
	Note   string
}

// benchGroup is one category's rows of the benchmark table, the measured
// values behind that category's ratio.
type benchGroup struct {
	Category, Anchor string
	Rows             []tableRow
}

// wholeRow is one basis of the whole-machine comparison.
type wholeRow struct {
	Basis string
	Cells []spCell
}

type wholeView struct {
	Rows     []wholeRow
	Divisors []envRow
}

type numaView struct {
	Label string
	Nodes []int
	Rows  [][]spCell
}

type view struct {
	Lang                       string
	Title, Generated, Baseline string
	Labels, Others             []string
	Files                      []*result.File
	// Verdict, Caveats and Limits frame every number below them: what the
	// comparison says, what limits how it may be read, and what it cannot say.
	Verdict                string
	Caveats                []caveat
	Limits                 []string
	Terms                  []term
	Cards                  []card
	Cats                   []catRow
	Whole                  *wholeView
	CategoryChart          template.HTML
	ScalingCharts          []template.HTML
	LatencyChart           template.HTML
	NUMA                   []numaView
	Rows                   []tableRow
	Groups                 []benchGroup
	WebChart, WebTimeline  template.HTML
	WebFiles               []*result.File
	StressChart, StressMHz template.HTML
	StressFiles            []*result.File
	PowerChart             template.HTML
	PowerFiles             []*result.File
	DensityChart           template.HTML
	DensityFiles           []*result.File
	StartupFiles           []*result.File
	TCO                    *compare.TCO
	Layers                 []compare.LayerRow
	Findings               []findingGroup
	Env                    []envRow
}

var scalingWorkloads = []string{"int/json", "int/compress", "float/matmul", "crypto/aes-gcm", "mem/triad", "sync/atomic-contended"}

func buildView(files []*result.File, opts port.ReportOptions) view {
	cmp := compare.Build(files, opts.Baseline)
	lang := normLang(opts.Lang)
	T := translator(lang)
	v := view{Lang: lang, Title: opts.Title, Generated: time.Now().Format("2006-01-02 15:04 MST"), Baseline: cmp.Baseline, Files: files}
	if v.Title == "" {
		v.Title = T("report.title")
	}
	for _, f := range files {
		v.Labels = append(v.Labels, f.Label)
		if f.Label != cmp.Baseline {
			v.Others = append(v.Others, f.Label)
		}
	}
	if opts.TCO != nil {
		v.TCO = compare.ComputeTCO(files, *opts.TCO)
		cmp.Whole.AddPrices(cmp.Baseline, opts.TCO.Servers)
	}
	v.Whole = buildWhole(files, cmp, v.Others, T)
	v.Cards = cards(files, cmp, v)
	v.Rows = rows(files, cmp, v.Others)
	v.Groups = groups(v.Rows)
	v.Verdict = verdict(files, cmp, v.Others, T)
	v.Caveats = caveats(files, cmp, T)
	if opts.Shared {
		v.Caveats = append(v.Caveats, caveat{Severity: doctor.Info, Text: T("cav.shared")})
		if len(opts.Anonymized) > 0 {
			v.Caveats = append(v.Caveats, caveat{Severity: doctor.Info, Text: T("cav.anon", strings.Join(opts.Anonymized, ", "))})
		}
	}
	if len(opts.BaselineGuessed) > 0 {
		v.Caveats = append([]caveat{{Severity: doctor.Warn, Text: T("cav.baseline_guess", cmp.Baseline, strings.Join(opts.BaselineGuessed, ", "))}}, v.Caveats...)
	}
	v.Limits = limits(files, v.Others, cmp.Whole != nil, T)
	v.Terms = terms(files, T)
	v.Cats = catRows(cmp, v.Others, T)

	if len(v.Others) > 0 {
		var cats []string
		for _, c := range cmp.Categories {
			cats = append(cats, string(c.Category))
		}
		v.CategoryChart = speedupBars(v.Others, cats, func(cat, g string) (float64, bool) {
			for _, c := range cmp.Categories {
				if string(c.Category) == cat {
					s, ok := c.Speedup[g]
					return s, ok
				}
			}
			return 0, false
		}, T)
	}
	v.ScalingCharts = scalingCharts(files, T)
	v.LatencyChart = latencyChart(files, T)
	for _, f := range files {
		if f.NUMA != nil {
			v.NUMA = append(v.NUMA, buildNUMA(f))
		}
		if f.Web != nil {
			v.WebFiles = append(v.WebFiles, f)
		}
		if f.Stress != nil {
			v.StressFiles = append(v.StressFiles, f)
		}
		if f.PowerCurve != nil {
			v.PowerFiles = append(v.PowerFiles, f)
		}
		if f.Density != nil {
			v.DensityFiles = append(v.DensityFiles, f)
		}
		if f.Startup != nil {
			v.StartupFiles = append(v.StartupFiles, f)
		}
		if len(f.Findings) > 0 {
			v.Findings = append(v.Findings, findingGroup{Label: f.Label, Findings: f.Findings})
		}
	}
	if len(cmp.Findings) > 0 {
		v.Findings = append(v.Findings, findingGroup{Label: T("doc.comparison"), Findings: cmp.Findings})
	}
	v.WebChart, v.WebTimeline = webCharts(v.WebFiles, T)
	v.StressChart, v.StressMHz = stressCharts(v.StressFiles, T)
	v.PowerChart = powerChart(v.PowerFiles, T)
	v.DensityChart = densityChart(v.DensityFiles, T)
	v.Layers = cmp.Layers
	v.Env = env(files, T)
	return v
}

func cards(files []*result.File, cmp compare.Report, v view) []card {
	var out []card
	if len(files) == 1 {
		f := files[0]
		out = append(out,
			card{Label: v.T("card.cpu"), Value: f.System.CPU.Model, Sub: v.T("card.logical", f.System.CPU.Arch, f.System.CPU.LogicalCPUs)},
			card{Label: v.T("card.context"), Value: f.System.Layer(), Sub: v.T("card.effective", f.System.Context.EffectiveCPUs)},
			card{Label: v.T("card.benchmarks"), Value: fmt.Sprint(len(f.Benchmarks)), Sub: v.T("card.findings", len(f.Findings))})
		if f.Web != nil {
			out = append(out, card{Label: v.T("card.maxrps"), Value: num(f.Web.KneeRPS), Sub: fmt.Sprintf("p99 ≤ %.0f ms", f.Web.SLO.P99Ms)})
		}
		return out
	}
	for _, l := range v.Others {
		// single-thread and multi-thread first: the pair a reader knows from
		// other CPU benchmarks, then the geomean of every row.
		if s, ok := cmp.Single[l]; ok {
			out = append(out, card{Label: v.T("card.single"), Value: pctChange(s),
				Sub: v.T("card.single_sub", l, cmp.Baseline, cmp.SingleWorkloads[l]), Tone: tone(s)})
		}
		if w := cmp.Whole; w != nil {
			if s, ok := w.Ratio[compare.PerMachine][l]; ok {
				out = append(out, card{Label: v.T("card.whole"), Value: pctChange(s),
					Sub: v.T("card.whole_sub", l, cmp.Baseline, wholeThreads(w, l), wholeThreads(w, cmp.Baseline), w.Workloads[l]), Tone: tone(s)})
			}
		}
		if s, ok := cmp.Overall[l]; ok {
			out = append(out, card{Label: v.T("card.micro"), Value: pctChange(s), Sub: v.T("card.vs", l, cmp.Baseline), Tone: tone(s)})
		}
		if s, ok := cmp.Overall["web:"+l]; ok {
			out = append(out, card{Label: v.T("card.maxrps"), Value: pctChange(s), Sub: v.T("card.vs", l, cmp.Baseline), Tone: tone(s)})
		}
		if lo := lowLoadP99(files, cmp.Baseline, l); lo > 0 {
			out = append(out, card{Label: v.T("card.p99low"), Value: pctChange(1 / lo), Sub: v.T("card.p99low_sub"), Tone: tone(lo)})
		}
	}
	if v.TCO != nil {
		for _, r := range v.TCO.Rows {
			if r.Servers > 0 {
				out = append(out, card{Label: v.T("card.servers", num(v.TCO.TargetRPS)), Value: fmt.Sprint(r.Servers),
					Sub: v.T("card.servers_sub", r.Label, r.Years, money(r.Total), r.Currency)})
			}
		}
	}
	return out
}

func wholeThreads(w *compare.Whole, label string) int {
	for _, c := range w.Columns {
		if c.Label == label {
			return c.Threads
		}
	}
	return 0
}

// buildWhole lays the whole-machine ratios out per basis, with what each
// machine was divided by, so a per-watt or per-price ratio can be checked.
func buildWhole(files []*result.File, cmp compare.Report, others []string, T tfn) *wholeView {
	w := cmp.Whole
	if w == nil || len(others) == 0 {
		return nil
	}
	out := &wholeView{}
	names := map[compare.Basis]string{compare.PerMachine: T("whole.machine"), compare.PerCore: T("whole.core"),
		compare.PerSocket: T("whole.socket"), compare.PerWatt: T("whole.watt"), compare.PerPrice: T("whole.price")}
	for _, b := range compare.Bases {
		row := wholeRow{Basis: names[b]}
		any := false
		for _, l := range others {
			if s, ok := w.Ratio[b][l]; ok {
				row.Cells, any = append(row.Cells, spCell{Text: pctChange(s), Class: tone(s)}), true
			} else {
				row.Cells = append(row.Cells, spCell{Text: "–"})
			}
		}
		if any {
			out.Rows = append(out.Rows, row)
		}
	}
	cols := map[string]compare.WholeColumn{}
	for _, c := range w.Columns {
		cols[c.Label] = c
	}
	div := func(name string, get func(c compare.WholeColumn) string) {
		r := envRow{Key: name}
		for _, f := range files {
			c, ok := cols[f.Label]
			if !ok {
				r.Values = append(r.Values, "–")
				continue
			}
			r.Values = append(r.Values, get(c))
		}
		if slices.ContainsFunc(r.Values, func(v string) bool { return v != "–" }) {
			out.Divisors = append(out.Divisors, r)
		}
	}
	div(T("whole.threads"), func(c compare.WholeColumn) string { return fmt.Sprint(c.Threads) })
	div(T("whole.cores"), func(c compare.WholeColumn) string { return fmt.Sprint(c.Cores) })
	div(T("whole.sockets"), func(c compare.WholeColumn) string {
		if c.Sockets == 0 {
			return "–"
		}
		return fmt.Sprint(c.Sockets)
	})
	div(T("whole.watts"), func(c compare.WholeColumn) string {
		if c.Watts == 0 {
			return "–"
		}
		return fmt.Sprintf("%.0f W · %s", c.Watts, c.WattsSource)
	})
	div(T("whole.price_v"), func(c compare.WholeColumn) string {
		if c.Price == 0 {
			return "–"
		}
		return money(c.Price) + " " + c.Currency
	})
	div(T("whole.n"), func(c compare.WholeColumn) string {
		if n, ok := w.Workloads[c.Label]; ok {
			return fmt.Sprint(n)
		}
		return "–"
	})
	return out
}

// lowLoadP99 returns baseline p99 / other p99 at the lowest common target RPS (>1 = other is faster).
func lowLoadP99(files []*result.File, base, other string) float64 {
	var b, o *result.Web
	for _, f := range files {
		if f.Web == nil {
			continue
		}
		switch f.Label {
		case base:
			b = f.Web
		case other:
			o = f.Web
		}
	}
	if b == nil || o == nil || len(b.Steps) == 0 || len(o.Steps) == 0 || o.Steps[0].P99Ms == 0 {
		return 0
	}
	return b.Steps[0].P99Ms / o.Steps[0].P99Ms
}

func tone(speedup float64) string {
	switch {
	case speedup >= 1.03:
		return "good"
	case speedup <= 0.97:
		return "bad"
	}
	return ""
}

func rows(files []*result.File, cmp compare.Report, others []string) []tableRow {
	var out []tableRow
	for _, r := range cmp.Rows {
		tr := tableRow{Name: r.Name, Unit: r.Unit, Category: string(r.Category), Threads: r.Threads, Mismatch: r.ChecksumMismatch,
			BackendDiffers: r.BackendDiffers}
		for _, f := range files {
			val, ok := r.Values[f.Label]
			if !ok {
				tr.Cells = append(tr.Cells, cell{Value: "–"})
				continue
			}
			cv := r.CV[f.Label]
			c := cell{Value: num(val), CV: fmt.Sprintf("%.1f%%", cv), Noisy: cv > benchmark.NoisyCV}
			if b, ok := r.Backends[f.Label]; ok {
				c.Backend, c.Fallback = string(b.Kind)+" · "+b.Detail, b.Kind == benchmark.Software
			}
			tr.Cells = append(tr.Cells, c)
		}
		for _, l := range others {
			if s, ok := r.Speedup[l]; ok {
				tr.Speedups = append(tr.Speedups, spCell{Text: pctChange(s), Class: tone(s)})
			} else {
				tr.Speedups = append(tr.Speedups, spCell{Text: "–"})
			}
		}
		out = append(out, tr)
	}
	return out
}

// groups splits the rows by category. The rows arrive sorted by category.
func groups(rows []tableRow) []benchGroup {
	var out []benchGroup
	for _, r := range rows {
		if n := len(out); n == 0 || out[n-1].Category != r.Category {
			out = append(out, benchGroup{Category: r.Category, Anchor: catAnchor(r.Category)})
		}
		out[len(out)-1].Rows = append(out[len(out)-1].Rows, r)
	}
	return out
}

// catAnchor is the HTML id of a category's rows in the benchmark table.
func catAnchor(cat string) string { return "bm-" + mdAnchor(cat) }

func scalingCharts(files []*result.File, T tfn) []template.HTML {
	var out []template.HTML
	for _, name := range scalingWorkloads {
		var ss []series
		unit := ""
		for _, f := range files {
			s := series{Name: f.Label}
			for _, b := range f.Benchmarks {
				if b.Name == name {
					s.X = append(s.X, float64(b.Threads))
					s.Y = append(s.Y, b.Stats.Median)
					unit = b.Unit
				}
			}
			if len(s.X) > 1 {
				ss = append(ss, s)
			}
		}
		if len(ss) > 0 {
			out = append(out, lineChart(ss, lineOpts{Title: T("chart.scaling", name), XLabel: T("chart.threads"), YLabel: unit, ZeroY: true}))
		}
	}
	return out
}

func latencyChart(files []*result.File, T tfn) template.HTML {
	var ss []series
	for _, f := range files {
		s := series{Name: f.Label}
		for _, kb := range workload.LatencySizesKB {
			for _, b := range f.Benchmarks {
				if b.Name == "mem/latency-"+sizeLabel(kb) {
					s.X = append(s.X, float64(kb))
					s.Y = append(s.Y, b.Stats.Median)
				}
			}
		}
		if len(s.X) > 0 {
			ss = append(ss, s)
		}
	}
	if len(ss) == 0 {
		return ""
	}
	var ticks []float64
	for _, kb := range workload.LatencySizesKB {
		ticks = append(ticks, float64(kb))
	}
	return lineChart(ss, lineOpts{Title: T("chart.latency"), XLabel: T("chart.workingset"), YLabel: T("chart.nsload"),
		LogX: true, ZeroY: true, XTicks: ticks, XTickFmt: func(x float64) string { return sizeLabel(int(x)) }})
}

func sizeLabel(kb int) string {
	if kb >= 1024 {
		return fmt.Sprintf("%dM", kb/1024)
	}
	return fmt.Sprintf("%dK", kb)
}

func buildNUMA(f *result.File) numaView {
	nv := numaView{Label: f.Label, Nodes: f.NUMA.Nodes}
	lo := math.Inf(1)
	for _, r := range f.NUMA.LatencyNs {
		for _, x := range r {
			lo = math.Min(lo, x)
		}
	}
	for _, r := range f.NUMA.LatencyNs {
		var cells []spCell
		for _, x := range r {
			cls := ""
			switch {
			case x > lo*1.6:
				cls = "bad"
			case x > lo*1.2:
				cls = "warn"
			}
			cells = append(cells, spCell{Text: fmt.Sprintf("%.0f ns", x), Class: cls})
		}
		nv.Rows = append(nv.Rows, cells)
	}
	return nv
}

func webCharts(files []*result.File, T tfn) (template.HTML, template.HTML) {
	var lat, tl []series
	var slo float64
	for _, f := range files {
		s := series{Name: f.Label}
		for _, st := range f.Web.Steps {
			s.X = append(s.X, st.AchievedRPS)
			s.Y = append(s.Y, st.P99Ms)
		}
		if len(s.X) > 0 {
			lat = append(lat, s)
		}
		slo = f.Web.SLO.P99Ms
		t := series{Name: f.Label}
		for _, sec := range f.Web.Timeline {
			t.X = append(t.X, sec.T)
			t.Y = append(t.Y, sec.P99Ms)
		}
		if len(t.X) > 1 {
			tl = append(tl, t)
		}
	}
	var a, b template.HTML
	if len(lat) > 0 {
		a = lineChart(lat, lineOpts{Title: T("chart.web_lat"), XLabel: T("chart.rps"), YLabel: "p99 ms", ZeroY: true, RefY: slo})
	}
	if len(tl) > 0 {
		b = lineChart(tl, lineOpts{Title: T("chart.web_tl"), XLabel: T("chart.seconds"), YLabel: "p99 ms", ZeroY: true, RefY: slo})
	}
	return a, b
}

func stressCharts(files []*result.File, T tfn) (template.HTML, template.HTML) {
	var rate, mhz []series
	for _, f := range files {
		st := f.Stress
		r, m := series{Name: f.Label}, series{Name: f.Label}
		for _, s := range st.Samples {
			if st.PeakRate > 0 {
				r.X = append(r.X, s.T)
				r.Y = append(r.Y, s.Rate/st.PeakRate*100)
			}
			if s.MHz > 0 {
				m.X = append(m.X, s.T)
				m.Y = append(m.Y, s.MHz)
			}
		}
		if len(r.X) > 0 {
			rate = append(rate, r)
		}
		if len(m.X) > 0 {
			mhz = append(mhz, m)
		}
	}
	var a, b template.HTML
	if len(rate) > 0 {
		a = lineChart(rate, lineOpts{Title: T("chart.stress_rate"), XLabel: T("chart.seconds"), YLabel: T("chart.pctpeak"), ZeroY: true})
	}
	if len(mhz) > 0 {
		b = lineChart(mhz, lineOpts{Title: T("chart.stress_mhz"), XLabel: T("chart.seconds"), YLabel: "MHz", ZeroY: true})
	}
	return a, b
}

func powerChart(files []*result.File, T tfn) template.HTML {
	var ss []series
	for _, f := range files {
		s := series{Name: f.Label}
		for _, l := range f.PowerCurve.Levels {
			s.X = append(s.X, l.UtilPct)
			s.Y = append(s.Y, l.Watts)
		}
		ss = append(ss, s)
	}
	if len(ss) == 0 {
		return ""
	}
	return lineChart(ss, lineOpts{Title: T("chart.power"), XLabel: T("chart.util"), YLabel: T("chart.watts"), ZeroY: true})
}

func densityChart(files []*result.File, T tfn) template.HTML {
	var ss []series
	for _, f := range files {
		s := series{Name: f.Label}
		for _, r := range f.Density.Rows {
			s.X = append(s.X, float64(r.Instances))
			s.Y = append(s.Y, r.RelPct)
		}
		ss = append(ss, s)
	}
	if len(ss) == 0 {
		return ""
	}
	return lineChart(ss, lineOpts{Title: T("chart.density"), XLabel: T("chart.instances"), YLabel: T("chart.pctsolo"), ZeroY: true, RefY: 90})
}

// verdict is the one line a reader who stops after the title still leaves
// with: what the comparison says and what it is per.
func verdict(files []*result.File, cmp compare.Report, others []string, T tfn) string {
	if len(others) == 0 {
		f := files[0]
		return T("verdict.single", orDash(f.System.Layer()), f.Label, orDash(f.System.CPU.Model))
	}
	var parts []string
	for _, l := range others {
		s, hasMicro := cmp.Overall[l]
		w, hasWeb := cmp.Overall["web:"+l]
		switch {
		case hasMicro && hasWeb:
			parts = append(parts, T("verdict.both", l, pctChange(s), cmp.Baseline, pctChange(w)))
		case hasMicro:
			parts = append(parts, T("verdict.micro", l, pctChange(s), cmp.Baseline))
		case hasWeb:
			// a web-only report has no micro suite to lead with
			parts = append(parts, T("verdict.web", l, pctChange(w), cmp.Baseline))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + "." + scopeNote(files, cmp.Whole != nil, T)
}

// scopeNote names what the ratios are per, because "B is 13% slower" means
// nothing without it: the same CPUs can win per machine and lose per core.
func scopeNote(files []*result.File, whole bool, T tfn) string {
	n := files[0].System.Context.EffectiveCPUs
	for _, f := range files {
		if f.System.Context.EffectiveCPUs != n {
			return T("scope.differs")
		}
	}
	if n == 0 {
		return ""
	}
	if whole {
		return T("scope.whole", n)
	}
	return T("scope.per", n)
}

// caveats are the conditions that decide how far the numbers may be read.
// The report leads with them rather than closing with them, because a reader
// who skims takes the ratio and leaves.
func caveats(files []*result.File, cmp compare.Report, T tfn) []caveat {
	var out []caveat
	add := func(s doctor.Severity, key string, a ...any) {
		out = append(out, caveat{Severity: s, Text: T(key, a...)})
	}
	raw := func(s doctor.Severity, text string) { out = append(out, caveat{Severity: s, Text: text}) }
	// Same work on both sides is the precondition for every ratio below it.
	// A float kernel may differ benignly across architectures (arm64 fuses
	// multiply-add), so only a mismatch outside float says the work differed.
	var hard, soft, backend []string
	for _, r := range cmp.Rows {
		if r.ChecksumMismatch {
			if r.Category == benchmark.Float {
				soft = uniq(append(soft, r.Name))
			} else {
				hard = uniq(append(hard, r.Name))
			}
		}
		if r.BackendDiffers {
			backend = uniq(append(backend, r.Name))
		}
	}
	if len(hard) > 0 {
		add(doctor.Fail, "cav.checksum_hard", summarise(hard, 3, T))
	}
	if len(soft) > 0 {
		add(doctor.Info, "cav.checksum_soft", summarise(soft, 3, T))
	}
	if len(backend) > 0 {
		add(doctor.Warn, "cav.backend", summarise(backend, 3, T))
	}
	// A layer or quota difference between columns is not a CPU difference.
	// The hypervisor is part of the layer: a VM on VMware and one on Proxmox
	// differ in CPU model, scheduler and devices.
	if ms := distinct(files, func(f *result.File) string { return f.System.Layer() }); len(ms) > 1 {
		add(doctor.Warn, "cav.context", strings.Join(ms, ", "))
	}
	if cs := distinct(files, func(f *result.File) string { return fmt.Sprint(f.System.Context.EffectiveCPUs) }); len(cs) > 1 {
		add(doctor.Warn, "cav.cpus", strings.Join(cs, ", "))
	}
	// Without NUMA pinning the memory and concurrency rows depend on where the
	// host happened to put the vCPUs and pages during that run.
	var unpinned, pinned []string
	for _, f := range files {
		switch name := strings.ToLower(f.Label + " " + f.Machine); {
		case strings.Contains(name, "unpinned"):
			unpinned = append(unpinned, f.Label)
		case strings.Contains(name, "pinned"):
			pinned = append(pinned, f.Label)
		}
	}
	if len(unpinned) > 0 {
		t := T("cav.unpinned", strings.Join(unpinned, ", "))
		if len(pinned) > 0 {
			t += T("cav.unpinned_mixed")
		}
		raw(doctor.Warn, t)
	}
	// Instruction baselines are per architecture, so they only compare within
	// one: GOAMD64 and GOARM64 levels are never the same string.
	if mixed, all := archLevels(files); len(mixed) > 0 {
		add(doctor.Warn, "cav.arch_mixed", strings.Join(mixed, ", "))
	} else if len(all) > 0 {
		add(doctor.Info, "cav.arch_base", strings.Join(all, ", "))
	}
	// Columns folded from repeated runs show the spread between runs.
	for _, f := range files {
		if len(f.Runs) > 1 {
			add(doctor.Info, "cav.repeats", f.Label, len(f.Runs))
		}
	}
	// A run with no repetitions has no run-to-run spread to show.
	if single := singleRun(files); len(single) > 0 {
		add(doctor.Info, "cav.once", strings.Join(single, ", "))
	}
	for _, f := range files {
		if c := f.System.Context.Cgroup; c != nil && c.NrThrottled > 0 {
			add(doctor.Info, "cav.throttled", f.Label, c.NrThrottled, float64(c.ThrottledUsec)/1e6)
		}
		if s, pct, ok := loadGenCeiling(f); ok {
			add(doctor.Warn, "cav.loadgen", f.Label, num(s), pct)
		}
	}
	// Doctor already found things on the machines themselves. Its messages
	// come from the domain rules and are passed through as they are written.
	for _, f := range files {
		for _, d := range f.Findings {
			if d.Severity == doctor.Info {
				continue
			}
			t := f.Label + ": " + d.Message
			if d.Fix != "" {
				t += " → " + d.Fix
			}
			raw(d.Severity, t)
		}
	}
	slices.SortStableFunc(out, func(a, b caveat) int { return sevRank(a.Severity) - sevRank(b.Severity) })
	return out
}

// loadGenCeiling reports a web step that failed the SLO while the server still
// had CPU left: then the generator saturated first and the RPS is its limit.
func loadGenCeiling(f *result.File) (rps, cpuPct float64, ok bool) {
	if f.Web == nil {
		return 0, 0, false
	}
	for _, s := range f.Web.Steps {
		if !s.PassSLO && s.SUTCPUPct > 0 && s.SUTCPUPct < 75 {
			return s.AchievedRPS, s.SUTCPUPct, true
		}
	}
	return 0, 0, false
}

// limits closes the report with what it cannot answer, so the gap is on the
// page instead of being rediscovered later.
func limits(files []*result.File, others []string, whole bool, T tfn) []string {
	var out []string
	if len(others) > 0 && !whole {
		out = append(out, T("lim.ratios"))
	}
	if !anyFile(files, func(f *result.File) bool { return f.Power != nil || f.PowerCurve != nil }) {
		out = append(out, T("lim.nopower"))
	}
	if !anyFile(files, func(f *result.File) bool { return f.Web != nil }) {
		out = append(out, T("lim.noweb"))
	} else if !anyFile(files, func(f *result.File) bool {
		return f.Web != nil && f.Web.LoadGen != nil && f.Web.LoadGen.Hostname != f.System.Hostname
	}) {
		out = append(out, T("lim.loopback"))
	}
	if ms := distinct(files, func(f *result.File) string { return string(f.System.Context.Mode) }); len(ms) == 1 && (ms[0] == string(hardware.ModeVM) || ms[0] == string(hardware.ModeContainer)) {
		out = append(out, T("lim.layer", ms[0]))
	}
	if cs := distinct(files, func(f *result.File) string { return f.System.CPU.Model }); len(cs) > 1 {
		out = append(out, T("lim.models"))
	}
	return out
}

// noisyShare is the fraction of a category's measurements that must exceed
// NoisyCV before the category is called noisy. A note that fires on the odd
// noisy row fires on every category and so says nothing.
const noisyShare = 0.34

// pinTerm defines "pinned" and "unpinned", the NUMA setting that decides
// whether the memory and concurrency rows of a VM can be compared at all.
func pinTerm(T tfn) term {
	return term{
		Name:   T("pin.name"),
		Intro:  T("pin.intro"),
		Points: []string{T("pin.p1"), T("pin.p2"), T("pin.p3"), T("pin.p4"), T("pin.p5"), T("pin.p6")},
	}
}

// terms lists the definitions for the words this report's labels use.
func terms(files []*result.File, T tfn) []term {
	var out []term
	if anyFile(files, func(f *result.File) bool {
		return strings.Contains(strings.ToLower(f.Label+" "+f.Machine), "pinned")
	}) {
		out = append(out, pinTerm(T))
	}
	return out
}

// catRows pairs each category ratio with the reason to distrust it, if any.
func catRows(cmp compare.Report, others []string, T tfn) []catRow {
	notes := map[benchmark.Category][]string{}
	noisy, total := map[benchmark.Category]int{}, map[benchmark.Category]int{}
	ratios := map[benchmark.Category]map[string][]float64{}
	for _, r := range cmp.Rows {
		if ratios[r.Category] == nil {
			ratios[r.Category] = map[string][]float64{}
		}
		for l, s := range r.Speedup {
			ratios[r.Category][l] = append(ratios[r.Category][l], s)
		}
		switch {
		case r.ChecksumMismatch:
			notes[r.Category] = append(notes[r.Category], T("note.checksum"))
		case r.BackendDiffers:
			notes[r.Category] = append(notes[r.Category], T("note.impl"))
		}
		for _, cv := range r.CV {
			total[r.Category]++
			if cv > benchmark.NoisyCV {
				noisy[r.Category]++
			}
		}
	}
	for cat, n := range noisy {
		if t := total[cat]; t > 0 && float64(n)/float64(t) > noisyShare {
			notes[cat] = append(notes[cat], T("note.noisy", n, t, benchmark.NoisyCV))
		}
	}
	var out []catRow
	for _, c := range cmp.Categories {
		row := catRow{Category: string(c.Category), Anchor: catAnchor(string(c.Category))}
		for _, l := range others {
			if s, ok := c.Speedup[l]; ok {
				sc := spCell{Text: pctChange(s), Class: tone(s)}
				if rs := ratios[c.Category][l]; len(rs) > 1 {
					if lo, hi := pctChange(slices.Min(rs)), pctChange(slices.Max(rs)); lo != hi {
						sc.Range = lo + " … " + hi
					}
				}
				row.Cells = append(row.Cells, sc)
			} else {
				row.Cells = append(row.Cells, spCell{Text: "–"})
			}
		}
		if n := uniq(notes[c.Category]); len(n) > 0 {
			row.Note = strings.Join(n, ", ")
		}
		out = append(out, row)
	}
	return out
}

func sevRank(s doctor.Severity) int {
	switch s {
	case doctor.Fail:
		return 0
	case doctor.Warn:
		return 1
	}
	return 2
}

func archLevel(f *result.File) string {
	if f.Build == nil {
		return ""
	}
	return f.Build.ArchLevel
}

// archLevels groups the build baselines by architecture. mixed lists the
// architectures whose columns disagree; all is set only when every column
// sits at the lowest baseline its architecture has, where the binary uses
// none of the optional instruction sets.
func archLevels(files []*result.File) (mixed, all []string) {
	byArch := map[string][]string{}
	for _, f := range files {
		if l := archLevel(f); l != "" {
			byArch[f.System.CPU.Arch] = uniq(append(byArch[f.System.CPU.Arch], l))
		}
	}
	base := map[string]string{"amd64": "GOAMD64=v1", "arm64": "GOARM64=v8.0"}
	atBase := len(byArch) > 0
	for _, arch := range slices.Sorted(maps.Keys(byArch)) {
		ls := byArch[arch]
		if len(ls) > 1 {
			mixed = append(mixed, arch+": "+strings.Join(ls, " vs "))
			atBase = false
			continue
		}
		if ls[0] != base[arch] {
			atBase = false
			continue
		}
		all = append(all, ls[0])
	}
	if !atBase {
		return mixed, nil
	}
	return mixed, all
}

func singleRun(files []*result.File) []string {
	var out []string
	for _, f := range files {
		one := len(f.Benchmarks) > 0
		for _, b := range f.Benchmarks {
			if b.Stats.N > 1 {
				one = false
				break
			}
		}
		if one {
			out = append(out, f.Label)
		}
	}
	return out
}

func distinct(files []*result.File, get func(*result.File) string) []string {
	var out []string
	for _, f := range files {
		if v := get(f); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func anyFile(files []*result.File, pred func(*result.File) bool) bool {
	for _, f := range files {
		if pred(f) {
			return true
		}
	}
	return false
}

// summarise names a few items and counts the rest: a caveat has to stay
// readable when half the table trips it.
func summarise(xs []string, max int, T tfn) string {
	if len(xs) <= max {
		return strings.Join(xs, ", ")
	}
	return T("cav.rows_more", strings.Join(xs[:max], ", "), len(xs)-max)
}

func uniq(xs []string) []string {
	var out []string
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func env(files []*result.File, T tfn) []envRow {
	type getter struct {
		key string
		get func(f *result.File) string
	}
	// The hardware inventory rows are left out when no column has a value, so
	// a set of VMs is not padded with empty rows.
	optional := map[string]bool{T("env.bios"): true, T("env.dimms"): true, T("env.storage"): true, T("env.disks"): true, T("env.netctl"): true, T("env.runs"): true}
	gs := []getter{
		{T("env.machine"), func(f *result.File) string { return f.Machine + " · " + f.System.Layer() }},
		{T("env.cpu"), func(f *result.File) string { return orDash(f.System.CPU.Model) }},
		// MIDR is the only CPU identity an arm64 guest is sure to have: a VM
		// often reports no model name at all, and the name cpubench prints
		// comes from its own table, not from the hardware.
		{T("env.identity"), func(f *result.File) string {
			m := f.System.CPU.MIDR
			if m == nil {
				return orDash(f.System.CPU.Vendor)
			}
			s := fmt.Sprintf("%s · implementer %s part %s", orDash(f.System.CPU.Vendor), m.Implementer, m.Part)
			if m.Variant != "" {
				s += " variant " + m.Variant
			}
			if m.Revision != "" {
				s += " rev " + m.Revision
			}
			return s
		}},
		{T("env.arch"), func(f *result.File) string { return f.System.CPU.Arch }},
		{T("env.topology"), func(f *result.File) string {
			c := f.System.CPU
			return fmt.Sprintf("%d × %d × %d", c.Sockets, c.CoresPerSocket, c.ThreadsPerCore)
		}},
		{T("env.eff"), func(f *result.File) string { return fmt.Sprint(f.System.Context.EffectiveCPUs) }},
		{T("env.mhz"), func(f *result.File) string {
			if f.System.CPU.MaxMHz == 0 {
				return "–"
			}
			return num(f.System.CPU.MaxMHz)
		}},
		{T("env.numa"), func(f *result.File) string { return fmt.Sprint(len(f.System.Topology.NUMANodes)) }},
		{T("env.page"), func(f *result.File) string { return fmt.Sprintf("%dK", f.System.Topology.PageSize/1024) }},
		{T("env.memory"), func(f *result.File) string {
			return T("env.memory_v", f.System.Memory.TotalMB/1024, len(f.System.Memory.DIMMs))
		}},
		{T("env.platform"), func(f *result.File) string {
			pl := f.System.Platform
			if name := strings.TrimSpace(pl.Vendor + " " + pl.Product); name != "" {
				return name + " · virt " + pl.Virt
			}
			return "virt " + pl.Virt
		}},
		{T("env.bios"), func(f *result.File) string {
			pl := f.System.Platform
			v := strings.TrimSpace(pl.BIOSVendor + " " + pl.BIOS)
			if pl.BIOSDate != "" {
				v += " (" + pl.BIOSDate + ")"
			}
			return orDash(v)
		}},
		{T("env.dimms"), func(f *result.File) string {
			m := f.System.Memory
			if len(m.DIMMs) == 0 {
				return "–"
			}
			if m.Slots == 0 {
				return dimmSummary(m.DIMMs, T)
			}
			return T("env.dimms_v", len(m.DIMMs), m.Slots, dimmSummary(m.DIMMs, T))
		}},
		{T("env.storage"), func(f *result.File) string { return pciSummary(f.System.PCI, "storage") }},
		{T("env.disks"), func(f *result.File) string { return diskSummary(f.System.Disks) }},
		{T("env.netctl"), func(f *result.File) string { return pciSummary(f.System.PCI, "network") }},
		{T("env.os"), func(f *result.File) string {
			return f.System.OS.Name + " " + f.System.OS.Version + " · " + f.System.OS.Kernel
		}},
		{T("env.gov"), func(f *result.File) string { return orDash(f.System.OS.Governor) + " / " + orDash(f.System.OS.Boost) }},
		{T("env.mitig"), func(f *result.File) string { return orDash(f.System.OS.Mitigations) }},
		{T("env.loadgen"), func(f *result.File) string {
			if f.Web == nil || f.Web.LoadGen == nil {
				return "–"
			}
			g := f.Web.LoadGen
			return T("env.loadgen_v", g.Hostname, g.CPU, g.LogicalCPUs, g.Agents)
		}},
		{T("env.power"), func(f *result.File) string {
			if f.Power == nil {
				return "–"
			}
			return fmt.Sprintf("%.0f / %.0f W · %s", f.Power.Avg, f.Power.Max, f.Power.Source)
		}},
		{T("env.build"), func(f *result.File) string {
			if f.Build == nil {
				return f.Tool + " · " + f.System.OS.GoVersion
			}
			return f.Build.String()
		}},
		{T("env.run"), func(f *result.File) string { return f.Command + " · " + f.CreatedAt.Format("2006-01-02 15:04Z") }},
		{T("env.runs"), func(f *result.File) string {
			if len(f.Runs) == 0 {
				return "–"
			}
			return T("env.runs_v", len(f.Runs), strings.Join(f.Runs, ", "))
		}},
	}
	var out []envRow
	for _, g := range gs {
		r := envRow{Key: g.key}
		for _, f := range files {
			r.Values = append(r.Values, g.get(f))
		}
		if optional[g.key] && !slices.ContainsFunc(r.Values, func(v string) bool { return v != "–" }) {
			continue
		}
		out = append(out, r)
	}
	return out
}

// dimmSummary describes the DIMM population as "16 × 32 GB DDR5 4800 MT/s",
// one group per kind, with the rated speed when the DIMMs run slower.
func dimmSummary(ds []hardware.DIMM, T tfn) string {
	var keys []string
	count := map[string]int{}
	for _, d := range ds {
		k := strings.TrimSpace(fmt.Sprintf("%d GB %s %s", d.SizeMB/1024, d.Type, orRated(d, T)))
		if count[k] == 0 {
			keys = append(keys, k)
		}
		count[k]++
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d × %s", count[k], k))
	}
	return strings.Join(parts, ", ")
}

func orRated(d hardware.DIMM, T tfn) string {
	if d.Configured == "" || d.Configured == d.Speed {
		return d.Speed
	}
	return T("env.rated", d.Configured, d.Speed)
}

// pciSummary groups controllers of one kind by driver and PCI id, with the
// link and NUMA node, e.g. "2 × nvme 0x144d:0xa824 · 16.0 GT/s x4 · node 2".
func pciSummary(devs []hardware.PCIDevice, kind string) string {
	var keys []string
	count := map[string]int{}
	for _, d := range devs {
		if d.Kind != kind {
			continue
		}
		k := strings.TrimSpace(d.Driver + " " + d.VendorID + ":" + d.DeviceID)
		if sp := strings.TrimSuffix(d.LinkSpeed, " PCIe"); sp != "" {
			k += fmt.Sprintf(" · %s x%d", sp, d.LinkWidth)
			if d.Degraded() {
				k += fmt.Sprintf(" (max %s x%d)", strings.TrimSuffix(d.MaxLinkSpeed, " PCIe"), d.MaxLinkWidth)
			}
		}
		if d.NUMANode >= 0 {
			k += fmt.Sprintf(" · node %d", d.NUMANode)
		}
		if count[k] == 0 {
			keys = append(keys, k)
		}
		count[k]++
	}
	if len(keys) == 0 {
		return "–"
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d × %s", count[k], k))
	}
	return strings.Join(parts, "; ")
}

// diskSummary groups disks by model and size, e.g. "2 × 3.84 TB SSD SAMSUNG …".
func diskSummary(ds []hardware.Disk) string {
	var keys []string
	count := map[string]int{}
	for _, d := range ds {
		media := "SSD"
		if d.Rotational {
			media = "HDD"
		}
		size := fmt.Sprintf("%.0f GB", d.SizeGB)
		if d.SizeGB >= 1000 {
			size = fmt.Sprintf("%.2f TB", d.SizeGB/1000)
		}
		k := strings.TrimSpace(size + " " + media + " " + d.Model)
		if count[k] == 0 {
			keys = append(keys, k)
		}
		count[k]++
	}
	if len(keys) == 0 {
		return "–"
	}
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d × %s", count[k], k))
	}
	return strings.Join(parts, "; ")
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

func money(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

var funcs = template.FuncMap{
	"num":   num,
	"money": money,
	"pct":   func(v float64) string { return fmt.Sprintf("%+.1f%%", v) },
	"f1":    func(v float64) string { return fmt.Sprintf("%.1f", v) },
	"tonePct": func(v float64) string {
		return tone(1 + v/100)
	},
	"sevClass": func(s doctor.Severity) string {
		return map[doctor.Severity]string{doctor.Warn: "warn", doctor.Fail: "bad"}[s]
	},
	"hasWarn": hasWarn,
	"nfind":   countFindings,
	// blocking caveats go above the numbers, notes below them.
	"blocking": func(cs []caveat) []caveat { return filterSev(cs, doctor.Fail, doctor.Warn) },
	"notes":    func(cs []caveat) []caveat { return filterSev(cs, doctor.Info) },
	"anyFail": func(cs []caveat) bool {
		return slices.ContainsFunc(cs, func(c caveat) bool { return c.Severity == doctor.Fail })
	},
	"rpsw": func(rps, w float64) float64 { return rps / w },
	"usec": func(u int64) float64 { return float64(u) / 1e6 },
	"has":  func(xs []string, x string) bool { return slices.Contains(xs, x) },
	"get": func(m map[string]float64, k string) (string, error) {
		v, ok := m[k]
		if !ok {
			return "–", nil
		}
		return fmt.Sprintf("%+.1f%%", v), nil
	},
}
