package app

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/workload"
)

type DensityOptions struct {
	Label, Machine string
	Workload       string
	GroupSize      int // threads per instance, like the vCPUs of one VM/pod
	Iterations     int
	Scale          float64
	Pin            bool
}

// Density packs more and more instances of GroupSize threads onto the machine
// (each on its own CPUs, filling NUMA node by node) and reports how much each
// instance slows down as neighbours are added. The answer, "how many
// instances still get ≥90% of solo speed", is the consolidation ratio used to
// plan VMs or pods per server.
func (a *App) Density(ctx context.Context, o DensityOptions) (*result.File, error) {
	f, err := a.newFile(ctx, o.Label, o.Machine, "density")
	if err != nil {
		return nil, err
	}
	w := workload.Find(o.Workload)
	if w == nil || w.Info().SingleThread {
		return nil, fmt.Errorf("unknown or single-thread workload %q", o.Workload)
	}
	g := max(o.GroupSize, 1)
	cpus := nodeMajorOrder(f.System)
	maxK := len(cpus) / g
	if maxK < 1 {
		return nil, fmt.Errorf("group size %d exceeds %d usable CPUs", g, len(cpus))
	}
	if prev := runtime.GOMAXPROCS(0); len(cpus) > prev {
		runtime.GOMAXPROCS(len(cpus))
		defer runtime.GOMAXPROCS(prev)
	}
	d := &result.Density{Workload: o.Workload, GroupSize: g}
	var solo float64
	for _, k := range densitySteps(maxK) {
		if err := ctx.Err(); err != nil {
			return f, err
		}
		n := k * g
		if err := w.Setup(n, o.Scale); err != nil {
			return nil, err
		}
		var per []float64
		for i := range 1 + o.Iterations { // first iteration is warmup
			runtime.GC()
			r, err := a.iterateRaw(w, cpus[:n], o.Pin)
			if err != nil {
				w.Teardown()
				return nil, err
			}
			if i == 0 {
				continue
			}
			var sum float64
			for inst := range k {
				var work float64
				var longest time.Duration
				for t := inst * g; t < (inst+1)*g; t++ {
					work += r.works[t]
					longest = max(longest, r.durs[t])
				}
				sum += work / longest.Seconds()
			}
			per = append(per, sum/float64(k))
		}
		w.Teardown()
		slices.Sort(per)
		p := benchmark.Median(per) * w.Info().UnitScale
		if k == 1 {
			solo = p
		}
		row := result.DensityRow{Instances: k, PerInstance: p, Total: p * float64(k), RelPct: p / solo * 100}
		if row.RelPct >= 90 {
			d.MaxInstances = k
		}
		d.Rows = append(d.Rows, row)
		a.logf("density %3d x %d thr  per-instance %10.1f %s  (%5.1f%% of solo)  total %10.1f", k, g, p, w.Info().Unit, row.RelPct, row.Total)
	}
	f.Density = d
	f.FinishedAt = time.Now().UTC()
	return f, nil
}

// nodeMajorOrder lists usable CPUs NUMA node by node (cores before SMT
// siblings within a node), so consecutive groups look like NUMA-pinned VMs.
func nodeMajorOrder(sys hardware.System) []int {
	usable := cpuOrder(sys, -1)
	var out []int
	for _, n := range sys.Topology.NUMANodes {
		for _, c := range hardware.CPUsOnNode(sys.Topology, n.ID) {
			if slices.Contains(usable, c) {
				out = append(out, c)
			}
		}
	}
	for _, c := range usable {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// densitySteps: every count up to 8, then roughly ×1.5, always ending at max.
func densitySteps(maxK int) []int {
	var ks []int
	for k := 1; k <= maxK; {
		ks = append(ks, k)
		if k < 8 {
			k++
		} else {
			k = k * 3 / 2
		}
	}
	if ks[len(ks)-1] != maxK {
		ks = append(ks, maxK)
	}
	return ks
}
