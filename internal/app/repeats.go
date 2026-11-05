package app

import (
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/build"
	"github.com/chaloemph/cpubench/internal/domain/doctor"
	"github.com/chaloemph/cpubench/internal/domain/result"
)

// RepeatGroup is one column made of repeated runs.
type RepeatGroup struct {
	Label   string   // the column's label in the report
	Members []string // labels of the runs, oldest first
}

// GroupRepeats folds repeated runs into one column each. Runs are repeats
// when they share the machine (--machine), the layer (native, vm/kvm, …) and
// the command (for example "auto standard"); runs of one machine on different
// layers stay apart, because their difference is the layer overhead.
//
// Every benchmark row of a group becomes the median of the runs' medians, and
// its Stats (and so the CV the report shows) is the spread between runs. The
// rest of the file (system, stress, power, web, density) comes from the oldest
// run. A file with no repeat passes through unchanged.
//
// Call it after MergeByLabel, so a run's micro and web parts are already one
// file. The result keeps the order in which groups first appear, so the
// default baseline is still the first result given.
func GroupRepeats(files []*result.File) ([]*result.File, []RepeatGroup) {
	type key struct{ machine, layer, command string }
	members := map[key][]*result.File{}
	var order []key
	for _, f := range files {
		k := key{f.Machine, f.System.Layer(), f.Command}
		if _, ok := members[k]; !ok {
			order = append(order, k)
		}
		members[k] = append(members[k], f)
	}
	used := map[string]bool{}
	for _, f := range files {
		used[f.Label] = true
	}
	var out []*result.File
	var groups []RepeatGroup
	for _, k := range order {
		fs := members[k]
		if len(fs) == 1 {
			out = append(out, fs[0])
			continue
		}
		slices.SortStableFunc(fs, func(a, b *result.File) int { return a.CreatedAt.Compare(b.CreatedAt) })
		g := foldRuns(fs)
		for _, f := range fs {
			delete(used, f.Label)
		}
		g.Label = uniqueLabel(groupLabel(fs), k.machine, used)
		used[g.Label] = true
		out = append(out, g)
		groups = append(groups, RepeatGroup{Label: g.Label, Members: g.Runs})
	}
	return out, groups
}

// foldRuns builds one file from runs sorted oldest first.
func foldRuns(fs []*result.File) *result.File {
	first := fs[0]
	g := *first
	g.ID = first.ID + "+" + strconv.Itoa(len(fs)-1)
	g.FinishedAt = fs[len(fs)-1].FinishedAt
	g.Runs = nil
	for _, f := range fs {
		g.Runs = append(g.Runs, f.Label)
	}

	type bkey struct {
		name    string
		threads int
	}
	var keys []bkey
	rows := map[bkey][]benchmark.Result{}
	for _, f := range fs {
		for _, b := range f.Benchmarks {
			k := bkey{b.Name, b.Threads}
			if _, ok := rows[k]; !ok {
				keys = append(keys, k)
			}
			rows[k] = append(rows[k], b)
		}
	}
	g.Benchmarks = make([]benchmark.Result, 0, len(keys))
	for _, k := range keys {
		rs := rows[k]
		b := rs[0]
		b.Samples = make([]float64, 0, len(rs))
		for _, r := range rs {
			b.Samples = append(b.Samples, r.Stats.Median)
			// Runs that computed different checksums did not do the same work.
			b.ChecksumStable = b.ChecksumStable && r.ChecksumStable && r.Checksum == rs[0].Checksum
		}
		b.Stats = benchmark.Summarize(b.Samples)
		g.Benchmarks = append(g.Benchmarks, b)
	}

	// Keep every distinct finding, and say when the runs were not alike.
	g.Findings = nil
	seen := map[string]bool{}
	add := func(ds ...doctor.Finding) {
		for _, d := range ds {
			if k := d.ID + "\x00" + d.Message; !seen[k] {
				seen[k] = true
				g.Findings = append(g.Findings, d)
			}
		}
	}
	for _, f := range fs {
		add(f.Findings...)
	}
	for _, f := range fs[1:] {
		add(doctor.Comparable(first.System, f.System)...)
		if first.Build != nil && f.Build != nil {
			add(build.Comparable(*first.Build, *f.Build)...)
		}
	}
	return &g
}

// groupLabel is the runs' common label prefix without the run number and
// separators: x86-01-r1, x86-01-r2 → x86-01; arm-std-1, arm-std-2 → arm-std.
func groupLabel(fs []*result.File) string {
	p := fs[0].Label
	for _, f := range fs[1:] {
		n := 0
		for n < len(p) && n < len(f.Label) && p[n] == f.Label[n] {
			n++
		}
		p = p[:n]
	}
	p = strings.TrimRightFunc(p, unicode.IsDigit) // a-10, a-11 share "a-1"
	p = strings.TrimRight(p, "-_. ")
	for _, suffix := range []string{"-run", "_run", "-r", "_r"} {
		p = strings.TrimSuffix(p, suffix)
	}
	return p
}

// uniqueLabel avoids a group label that another column already uses.
func uniqueLabel(label, machine string, used map[string]bool) string {
	for _, l := range []string{label, machine} {
		if l != "" && !used[l] {
			return l
		}
	}
	base := machine
	if base == "" {
		base = label
	}
	for i := 2; ; i++ {
		if l := base + "-" + strconv.Itoa(i); !used[l] {
			return l
		}
	}
}
