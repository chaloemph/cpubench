package compare

import (
	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/result"
)

// Basis is what a whole-machine ratio is divided by before it is compared.
type Basis string

const (
	PerMachine Basis = "machine"
	PerCore    Basis = "core"
	PerSocket  Basis = "socket"
	PerWatt    Basis = "watt"
	PerPrice   Basis = "price"
)

// Bases lists every basis in reading order.
var Bases = []Basis{PerMachine, PerCore, PerSocket, PerWatt, PerPrice}

// WholeColumn is what one column's whole-machine throughput is divided by.
type WholeColumn struct {
	Label   string `json:"label"`
	Threads int    `json:"threads"` // highest thread count measured
	Cores   int    `json:"cores"`
	// Sockets is 0 unless the run had the whole physical machine: a VM's
	// sockets are whatever the hypervisor was told.
	Sockets     int     `json:"sockets,omitempty"`
	Watts       float64 `json:"watts,omitempty"`
	WattsSource string  `json:"watts_source,omitempty"` // stress, power-curve
	Price       float64 `json:"price,omitempty"`
	Currency    string  `json:"currency,omitempty"`
}

// Whole compares each column at its own highest thread count, so machines
// with different core counts are compared as machines, then divided by
// cores, sockets, watts and price. The micro-suite ratios compare equal
// thread counts instead, which is per core.
type Whole struct {
	Columns []WholeColumn `json:"columns"`
	// Ratio[basis][label] is the geomean ratio against the baseline (>1 = better).
	Ratio map[Basis]map[string]float64 `json:"ratio"`
	// Workloads is how many workloads each ratio rests on.
	Workloads map[string]int `json:"workloads"`
}

// contended workloads make every thread fight over one cache line or lock:
// their total does not grow with the machine, so they measure contention, not
// capacity, and would penalise the bigger machine once divided by cores.
var contended = map[string]bool{"sync/atomic-contended": true, "sync/mutex": true}

// wholeMachine builds the whole-machine comparison; nil when no two columns
// share a multi-threaded throughput workload.
func wholeMachine(files []*result.File, base *result.File) *Whole {
	top := func(f *result.File) map[string]benchmark.Result {
		m := map[string]benchmark.Result{}
		for _, b := range f.Benchmarks {
			if b.Kind != benchmark.Throughput || b.Stats.Median <= 0 || contended[b.Name] {
				continue
			}
			if cur, ok := m[b.Name]; !ok || b.Threads > cur.Threads {
				m[b.Name] = b
			}
		}
		return m
	}
	bt := top(base)
	w := &Whole{Ratio: map[Basis]map[string]float64{}, Workloads: map[string]int{}}
	for _, b := range Bases {
		w.Ratio[b] = map[string]float64{}
	}
	cols := map[string]WholeColumn{}
	for _, f := range files {
		ft := top(f)
		c := WholeColumn{Label: f.Label, Cores: f.System.UsableCores()}
		for _, r := range ft {
			c.Threads = max(c.Threads, r.Threads)
		}
		if c.Threads <= 1 {
			continue // single-thread runs say nothing about the machine
		}
		if !f.System.Context.InVM && c.Cores == f.System.CPU.PhysicalCores() {
			c.Sockets = f.System.CPU.Sockets
		}
		c.Watts, c.WattsSource = fullLoadWatts(f)
		w.Columns = append(w.Columns, c)
		cols[f.Label] = c
		if f == base {
			continue
		}
		var rs []float64
		for name, r := range ft {
			if b, ok := bt[name]; ok && b.Threads > 1 && r.Threads > 1 {
				rs = append(rs, r.Stats.Median/b.Stats.Median)
			}
		}
		if len(rs) > 0 {
			w.Ratio[PerMachine][f.Label] = benchmark.GeoMean(rs)
			w.Workloads[f.Label] = len(rs)
		}
	}
	bc, ok := cols[base.Label]
	if !ok || len(w.Ratio[PerMachine]) == 0 {
		return nil
	}
	w.Ratio[PerMachine][base.Label] = 1
	for label, r := range w.Ratio[PerMachine] {
		c := cols[label]
		if c.Cores > 0 && bc.Cores > 0 {
			w.Ratio[PerCore][label] = r * float64(bc.Cores) / float64(c.Cores)
		}
		if c.Sockets > 0 && bc.Sockets > 0 {
			w.Ratio[PerSocket][label] = r * float64(bc.Sockets) / float64(c.Sockets)
		}
		if c.Watts > 0 && bc.Watts > 0 {
			w.Ratio[PerWatt][label] = r * bc.Watts / c.Watts
		}
	}
	return w
}

// fullLoadWatts is the server's power with every thread busy: the stress
// average, else the 100% step of the power curve. Wall power of the whole
// server, measured during that test rather than during each workload.
func fullLoadWatts(f *result.File) (float64, string) {
	if f.Stress != nil && f.Stress.AvgWatts > 0 {
		return f.Stress.AvgWatts, "stress"
	}
	if f.PowerCurve != nil {
		for _, l := range f.PowerCurve.Levels {
			if l.UtilPct >= 100 && l.Watts > 0 {
				return l.Watts, "power-curve"
			}
		}
	}
	return 0, ""
}

// AddPrices divides the whole-machine ratio by each server's price from the
// TCO model. Columns priced in another currency than the baseline are left
// out rather than converted.
func (w *Whole) AddPrices(baseline string, servers map[string]result.Cost) {
	if w == nil {
		return
	}
	bp, ok := servers[baseline]
	if !ok || bp.ServerPrice <= 0 {
		return
	}
	for i, c := range w.Columns {
		if p, ok := servers[c.Label]; ok && p.ServerPrice > 0 {
			w.Columns[i].Price, w.Columns[i].Currency = p.ServerPrice, p.Currency
		}
	}
	for label, r := range w.Ratio[PerMachine] {
		p, ok := servers[label]
		if !ok || p.ServerPrice <= 0 || p.Currency != bp.Currency {
			continue
		}
		w.Ratio[PerPrice][label] = r * bp.ServerPrice / p.ServerPrice
	}
}
