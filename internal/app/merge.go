package app

import (
	"slices"

	"github.com/chaloemph/cpubench/internal/domain/result"
)

// MergeByLabel combines files that share a label (e.g. a micro run and a web
// load test of the same server) into one, keeping the first file's identity.
func MergeByLabel(files []*result.File) []*result.File {
	byLabel := map[string]*result.File{}
	var out []*result.File
	for _, f := range files {
		m, ok := byLabel[f.Label]
		if !ok {
			c := *f
			byLabel[f.Label] = &c
			out = append(out, &c)
			continue
		}
		if len(m.Benchmarks) == 0 && len(f.Benchmarks) > 0 {
			m.System = f.System // micro runs carry the most complete snapshot
		}
		m.Benchmarks = append(m.Benchmarks, f.Benchmarks...)
		m.Findings = append(m.Findings, f.Findings...)
		m.Build = first(m.Build, f.Build)
		m.NUMA = first(m.NUMA, f.NUMA)
		m.Stress = first(m.Stress, f.Stress)
		m.PowerCurve = first(m.PowerCurve, f.PowerCurve)
		m.Web = first(m.Web, f.Web)
		m.Density = first(m.Density, f.Density)
		m.Startup = first(m.Startup, f.Startup)
		m.Power = first(m.Power, f.Power)
		m.Command += "+" + f.Command
	}
	return out
}

func first[T any](a, b *T) *T {
	if a != nil {
		return a
	}
	return b
}

// LatestPerLabel keeps only the newest file for each (label, command), so a
// results directory with repeated runs compares the most recent of each.
func LatestPerLabel(files []*result.File) []*result.File {
	type key struct{ label, cmd string }
	best := map[key]*result.File{}
	var order []key
	for _, f := range files {
		k := key{f.Label, f.Command}
		cur, ok := best[k]
		if !ok {
			order = append(order, k)
		}
		if !ok || f.CreatedAt.After(cur.CreatedAt) {
			best[k] = f
		}
	}
	out := make([]*result.File, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out
}

// DefaultBaseline picks the first result in the order given. No architecture
// is preferred: the baseline should be the machine in production today, which
// only the user knows, so AmbiguousBaseline tells them when it was a guess.
func DefaultBaseline(files []*result.File) string {
	if len(files) > 0 {
		return files[0].Label
	}
	return ""
}

// AmbiguousBaseline lists the machines DefaultBaseline chose among when there
// were several: then "the first" is an accident of file order, not the machine
// in production, and the reader has to be told. Runs of one machine on
// different layers are not ambiguous.
func AmbiguousBaseline(files []*result.File) []string {
	var machines []string
	for _, f := range files {
		if !slices.Contains(machines, f.Machine) {
			machines = append(machines, f.Machine)
		}
	}
	if len(machines) < 2 {
		return nil
	}
	return machines
}
