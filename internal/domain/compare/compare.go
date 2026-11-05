// Package compare turns several result files into ratios against a baseline,
// a layer-overhead matrix and an on-prem TCO table.
package compare

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/build"
	"github.com/chaloemph/cpubench/internal/domain/doctor"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
)

type Column struct {
	Label   string        `json:"label"`
	Machine string        `json:"machine"`
	Arch    string        `json:"arch"`
	Model   string        `json:"model"`
	Mode    hardware.Mode `json:"mode"`
	// Layer is Mode plus the hypervisor (vm/kvm, vm/vmware).
	Layer string `json:"layer"`
}

type Row struct {
	Key            string             `json:"key"`
	Name           string             `json:"name"`
	Category       benchmark.Category `json:"category"`
	Unit           string             `json:"unit"`
	Threads        int                `json:"threads"`
	HigherIsBetter bool               `json:"higher_is_better"`
	Values         map[string]float64 `json:"values"`
	CV             map[string]float64 `json:"cv"`
	// Speedup vs baseline: >1 means better than baseline regardless of unit direction.
	Speedup          map[string]float64 `json:"speedup"`
	Checksums        map[string]string  `json:"checksums"`
	ChecksumMismatch bool               `json:"checksum_mismatch"`
	// Backends is the implementation each column ran (crypto kernels only).
	Backends map[string]benchmark.Backend `json:"backends,omitempty"`
	// BackendDiffers: columns ran different kinds of implementation, so the
	// ratio reflects the instructions available, not general CPU speed.
	BackendDiffers bool `json:"backend_differs,omitempty"`
}

type CategoryRow struct {
	Category benchmark.Category `json:"category"`
	Speedup  map[string]float64 `json:"speedup"` // geomean per column
}

type LayerRow struct {
	Machine string `json:"machine"`
	// Base is the least-virtualised run of the machine (native, else VM),
	// the reference for the other layers.
	Base      string        `json:"base_label"`
	BaseMode  hardware.Mode `json:"base_mode"`
	BaseLayer string        `json:"base_layer"`
	// Pct change vs Base per label (negative = overhead).
	MicroPct map[string]float64 `json:"micro_pct"`
	WebPct   map[string]float64 `json:"web_pct,omitempty"`
}

type Report struct {
	Baseline   string             `json:"baseline"`
	Columns    []Column           `json:"columns"`
	Rows       []Row              `json:"rows"`
	Categories []CategoryRow      `json:"categories"`
	Overall    map[string]float64 `json:"overall"` // geomean speedup of all rows
	// Whole compares each column at its own highest thread count.
	Whole    *Whole           `json:"whole,omitempty"`
	Layers   []LayerRow       `json:"layers,omitempty"`
	TCO      *TCO             `json:"tco,omitempty"`
	Findings []doctor.Finding `json:"findings,omitempty"`
}

// Build compares files against the one labelled baseline (or the first file).
func Build(files []*result.File, baseline string) Report {
	if len(files) == 0 {
		return Report{}
	}
	if baseline == "" {
		baseline = files[0].Label
	}
	var base *result.File
	for _, f := range files {
		if f.Label == baseline {
			base = f
		}
	}
	if base == nil {
		base = files[0]
		baseline = base.Label
	}

	rep := Report{Baseline: baseline, Overall: map[string]float64{}}
	for _, f := range files {
		rep.Columns = append(rep.Columns, Column{Label: f.Label, Machine: f.Machine, Arch: f.System.CPU.Arch,
			Model: f.System.CPU.Model, Mode: f.System.Context.Mode, Layer: f.System.Layer()})
		if f.Build == nil {
			rep.Findings = append(rep.Findings, doctor.Finding{ID: "build-unknown", Severity: doctor.Info,
				Message: f.Label + ": no build info (result from an older cpubench), cannot check it ran the same binary"})
		}
		if f != base {
			rep.Findings = append(rep.Findings, prefix(f.Label, doctor.Comparable(base.System, f.System))...)
			if base.Build != nil && f.Build != nil {
				rep.Findings = append(rep.Findings, prefix(f.Label, build.Comparable(*base.Build, *f.Build))...)
			}
			if fd, ok := loadGenDiffers(base, f); ok {
				rep.Findings = append(rep.Findings, fd)
			}
		}
	}

	rows := map[string]*Row{}
	for _, f := range files {
		for _, b := range f.Benchmarks {
			r, ok := rows[b.Key()]
			if !ok {
				r = &Row{Key: b.Key(), Name: b.Name, Category: b.Category, Unit: b.Unit, Threads: b.Threads,
					HigherIsBetter: b.Kind != benchmark.Latency, Values: map[string]float64{}, CV: map[string]float64{},
					Speedup: map[string]float64{}, Checksums: map[string]string{}}
				rows[b.Key()] = r
			}
			r.Values[f.Label] = b.Stats.Median
			r.CV[f.Label] = b.Stats.CV
			r.Checksums[f.Label] = b.Checksum
			if b.Backend != nil {
				if r.Backends == nil {
					r.Backends = map[string]benchmark.Backend{}
				}
				r.Backends[f.Label] = *b.Backend
			}
		}
	}
	rep.Findings = append(rep.Findings, backendFindings(files, rows)...)
	for _, r := range rows {
		bv, ok := r.Values[baseline]
		var sums []string
		for label, v := range r.Values {
			sums = append(sums, r.Checksums[label])
			if !ok || bv == 0 || v == 0 {
				continue
			}
			if r.HigherIsBetter {
				r.Speedup[label] = v / bv
			} else {
				r.Speedup[label] = bv / v
			}
		}
		slices.Sort(sums)
		r.ChecksumMismatch = len(slices.Compact(sums)) > 1
		rep.Rows = append(rep.Rows, *r)
	}
	slices.SortFunc(rep.Rows, func(a, b Row) int {
		return cmp.Or(cmp.Compare(a.Category, b.Category), cmp.Compare(a.Name, b.Name), cmp.Compare(a.Threads, b.Threads))
	})

	cats := map[benchmark.Category]map[string][]float64{}
	all := map[string][]float64{}
	for _, r := range rep.Rows {
		if cats[r.Category] == nil {
			cats[r.Category] = map[string][]float64{}
		}
		for label, s := range r.Speedup {
			cats[r.Category][label] = append(cats[r.Category][label], s)
			all[label] = append(all[label], s)
		}
	}
	for c, m := range cats {
		cr := CategoryRow{Category: c, Speedup: map[string]float64{}}
		for label, v := range m {
			cr.Speedup[label] = benchmark.GeoMean(v)
		}
		rep.Categories = append(rep.Categories, cr)
	}
	slices.SortFunc(rep.Categories, func(a, b CategoryRow) int { return cmp.Compare(a.Category, b.Category) })
	for label, v := range all {
		rep.Overall[label] = benchmark.GeoMean(v)
	}
	if web := webKnee(base); web > 0 {
		for _, f := range files {
			if k := webKnee(f); k > 0 {
				rep.Overall["web:"+f.Label] = k / web
			}
		}
	}
	rep.Whole = wholeMachine(files, base)
	rep.Layers = layers(files)
	return rep
}

// backendFindings marks rows whose columns ran different kinds of
// implementation (e.g. SHA-256 on dedicated instructions vs AVX2) and reports
// each such workload once.
func backendFindings(files []*result.File, rows map[string]*Row) []doctor.Finding {
	reported := map[string]bool{}
	var names []string
	for _, r := range rows {
		var kinds []benchmark.BackendKind
		for _, b := range r.Backends {
			kinds = append(kinds, b.Kind)
		}
		slices.Sort(kinds)
		r.BackendDiffers = len(slices.Compact(kinds)) > 1
		if r.BackendDiffers && !reported[r.Name] {
			reported[r.Name] = true
			names = append(names, r.Name)
		}
	}
	slices.Sort(names)
	var out []doctor.Finding
	for _, name := range names {
		var parts []string
		for _, f := range files {
			for _, b := range f.Benchmarks {
				if b.Name == name && b.Backend != nil {
					parts = append(parts, f.Label+" "+b.Backend.String())
					break
				}
			}
		}
		out = append(out, doctor.Finding{ID: "backend-differs", Severity: doctor.Warn,
			Message: fmt.Sprintf("%s runs different implementations: %s; the ratio shows which instructions each CPU has, not general CPU speed",
				name, strings.Join(parts, ", ")),
			Fix: "weigh this row by how much the real service uses it (OpenSSL/BoringSSL may pick other code paths than Go)"})
	}
	return out
}

// loadGenDiffers warns when two web results were driven from different load
// generators: then RPS differences may come from the generator or the
// network path rather than the server CPU.
func loadGenDiffers(a, b *result.File) (doctor.Finding, bool) {
	if a.Web == nil || b.Web == nil || a.Web.LoadGen == nil || b.Web.LoadGen == nil {
		return doctor.Finding{}, false
	}
	x, y := a.Web.LoadGen, b.Web.LoadGen
	if x.Hostname == y.Hostname && x.Agents == y.Agents {
		return doctor.Finding{}, false
	}
	sev, why := doctor.Info, "same load generator spec"
	if x.CPU != y.CPU || x.LogicalCPUs != y.LogicalCPUs || x.Agents != y.Agents {
		sev, why = doctor.Warn, "different load generator spec"
	}
	return doctor.Finding{ID: "loadgen-differs", Severity: sev,
		Message: fmt.Sprintf("%s: web test driven from %s (%s, %d CPUs, %d agent) vs %s (%s, %d CPUs, %d agent): %s, and the network path may differ",
			b.Label, y.Hostname, y.CPU, y.LogicalCPUs, y.Agents, x.Hostname, x.CPU, x.LogicalCPUs, x.Agents, why),
		Fix: "check the load generator's CPU stayed below ~70% and compare p50 at the lowest step (network latency); best is the same machine for both"}, true
}

func prefix(label string, fs []doctor.Finding) []doctor.Finding {
	for i := range fs {
		fs[i].Message = label + ": " + fs[i].Message
	}
	return fs
}

func webKnee(f *result.File) float64 {
	if f.Web == nil {
		return 0
	}
	return f.Web.KneeRPS
}

// layerRank orders execution contexts from bare metal outwards.
var layerRank = map[hardware.Mode]int{hardware.ModeNative: 0, hardware.ModeVM: 1, hardware.ModeContainer: 2, hardware.ModeK8s: 2}

// layers compares every run of a machine with its least-virtualised run:
// native when there is one, else e.g. the VM that Docker runs inside.
func layers(files []*result.File) []LayerRow {
	byMachine := map[string][]*result.File{}
	var order []string
	for _, f := range files {
		if _, ok := byMachine[f.Machine]; !ok {
			order = append(order, f.Machine)
		}
		byMachine[f.Machine] = append(byMachine[f.Machine], f)
	}
	var out []LayerRow
	for _, m := range order {
		fs := byMachine[m]
		if len(fs) < 2 {
			continue
		}
		native := fs[0]
		for _, f := range fs[1:] {
			if layerRank[f.System.Context.Mode] < layerRank[native.System.Context.Mode] {
				native = f
			}
		}
		if layerRank[native.System.Context.Mode] >= 2 {
			continue // only containers: nothing underneath to compare with
		}
		lr := LayerRow{Machine: m, Base: native.Label, BaseMode: native.System.Context.Mode, BaseLayer: native.System.Layer(),
			MicroPct: map[string]float64{}, WebPct: map[string]float64{}}
		nat := map[string]benchmark.Result{}
		for _, b := range native.Benchmarks {
			nat[b.Key()] = b
		}
		for _, f := range fs {
			if f == native {
				continue
			}
			var sp []float64
			for _, b := range f.Benchmarks {
				n, ok := nat[b.Key()]
				if !ok || n.Stats.Median == 0 || b.Stats.Median == 0 {
					continue
				}
				if b.Kind == benchmark.Latency {
					sp = append(sp, n.Stats.Median/b.Stats.Median)
				} else {
					sp = append(sp, b.Stats.Median/n.Stats.Median)
				}
			}
			if len(sp) > 0 {
				lr.MicroPct[f.Label] = (benchmark.GeoMean(sp) - 1) * 100
			}
			if nk, fk := webKnee(native), webKnee(f); nk > 0 && fk > 0 {
				lr.WebPct[f.Label] = (fk/nk - 1) * 100
			}
		}
		out = append(out, lr)
	}
	return out
}
