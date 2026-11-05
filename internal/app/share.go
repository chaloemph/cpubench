package app

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
)

// Others says what a copy for one vendor does with the other vendors' machines.
type Others string

const (
	// OthersDrop leaves the other machines out: the safest copy to send.
	OthersDrop Others = "drop"
	// OthersAnonymize keeps their numbers under "candidate-a", "candidate-b",
	// ... without names, CPU model, platform, devices or prices.
	OthersAnonymize Others = "anonymize"
)

// ShareCopy prepares results to send to one vendor. Runs of the machines
// named in keep (by label or machine) and every run of the baseline's
// machine stay as they are; the other machines are dropped or anonymized.
// It returns the aliases given to anonymized runs, and never changes files.
func ShareCopy(files []*result.File, baseline string, keep []string, others Others) ([]*result.File, []string, error) {
	if others != OthersDrop && others != OthersAnonymize {
		return nil, nil, fmt.Errorf("others must be %q or %q, not %q", OthersDrop, OthersAnonymize, others)
	}
	kept := map[string]bool{} // machines
	for _, k := range keep {
		i := slices.IndexFunc(files, func(f *result.File) bool { return f.Label == k || f.Machine == k })
		if i < 0 {
			return nil, nil, fmt.Errorf("no result has label or machine %q", k)
		}
		kept[files[i].Machine] = true
	}
	bi := slices.IndexFunc(files, func(f *result.File) bool { return f.Label == baseline })
	if bi < 0 {
		return nil, nil, fmt.Errorf("baseline %q is not among the results", baseline)
	}
	kept[files[bi].Machine] = true

	var out []*result.File
	var aliases []string
	machineAlias := map[string]string{}
	runs := map[string]int{}
	for _, f := range files {
		if !kept[f.Machine] {
			runs[f.Machine]++
		}
	}
	seen := map[string]int{}
	for _, f := range files {
		if kept[f.Machine] {
			out = append(out, withoutBMC(f))
			continue
		}
		if others == OthersDrop {
			continue
		}
		m, ok := machineAlias[f.Machine]
		if !ok {
			m = "candidate-" + string(rune('a'+len(machineAlias)%26)) + strings.Repeat("'", len(machineAlias)/26)
			machineAlias[f.Machine] = m
		}
		seen[f.Machine]++
		label := m
		if runs[f.Machine] > 1 {
			label = fmt.Sprintf("%s-%d", m, seen[f.Machine])
		}
		a, err := anonymize(f, m, label)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, a)
		aliases = append(aliases, label)
	}
	return out, aliases, nil
}

// anonymize returns a copy of f under a new name, without what identifies
// the vendor or the bid: hostnames, CPU model and features, platform and
// BIOS, DIMM and disk parts, PCI devices, BMC addresses, URLs and price.
// Free text (findings, kernel log lines) is scrubbed of the same strings.
// The architecture stays, so when only one vendor offers a given
// architecture its column still says who it is.
func anonymize(f *result.File, machine, label string) (*result.File, error) {
	b, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	var c result.File
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	s := &c.System
	// Names first become the alias; identifying strings become "(hidden)".
	names := []string{c.Label, c.Machine, c.ID, s.Hostname}
	hidden := []string{s.CPU.Model, s.Platform.Vendor, s.Platform.Product, s.Platform.BIOS}
	for _, d := range s.Memory.DIMMs {
		hidden = append(hidden, d.Part)
	}
	for _, d := range s.Disks {
		hidden = append(hidden, d.Model)
	}
	for _, d := range s.PCI {
		hidden = append(hidden, d.VendorID+":"+d.DeviceID, d.Driver)
	}
	if c.Web != nil && c.Web.LoadGen != nil {
		names = append(names, c.Web.LoadGen.Hostname)
	}
	scrub := scrubber(label, names, hidden)

	c.Label, c.Machine, c.ID = label, machine, label
	c.Cost = nil
	s.Hostname = label
	s.CPU = hardware.CPUInfo{Arch: s.CPU.Arch, Machine: s.CPU.Machine, Sockets: s.CPU.Sockets,
		CoresPerSocket: s.CPU.CoresPerSocket, ThreadsPerCore: s.CPU.ThreadsPerCore, LogicalCPUs: s.CPU.LogicalCPUs}
	s.Platform = hardware.Platform{Virt: s.Platform.Virt}
	for i := range s.Memory.DIMMs {
		d := &s.Memory.DIMMs[i]
		d.Locator, d.Bank, d.Part, d.Manufacturer = "", "", "", ""
	}
	for i := range s.NICs {
		s.NICs[i].Name, s.NICs[i].Driver = fmt.Sprintf("nic%d", i), ""
	}
	s.PCI, s.Disks = nil, nil
	if k := s.Context.K8s; k != nil {
		*k = hardware.K8sInfo{}
	}
	for i := range c.Findings {
		c.Findings[i].Message, c.Findings[i].Fix = scrub(c.Findings[i].Message), scrub(c.Findings[i].Fix)
	}
	if c.Web != nil {
		c.Web.Target, c.Web.Scenario, c.Web.PowerSource = "", "", scheme(c.Web.PowerSource)
		if g := c.Web.LoadGen; g != nil {
			g.Hostname, g.CPU = "", ""
		}
	}
	if st := c.Stress; st != nil {
		st.PowerSource = scheme(st.PowerSource)
		var kept []string
		for _, l := range st.KernelErrors {
			if !strings.Contains(l, "DMI:") && !strings.Contains(l, "Hardware name:") {
				kept = append(kept, scrub(l))
			}
		}
		st.KernelErrors = kept
	}
	if pc := c.PowerCurve; pc != nil {
		pc.Source = scheme(pc.Source)
	}
	if p := c.Power; p != nil {
		p.Source = scheme(p.Source)
	}
	if su := c.Startup; su != nil {
		su.Command, su.ReadyURL = "", ""
	}
	return &c, nil
}

// withoutBMC copies f without the BMC address and user in its power sources:
// a copy leaving the organisation should not carry how to reach its BMCs.
func withoutBMC(f *result.File) *result.File {
	c := *f
	if c.Stress != nil {
		st := *c.Stress
		st.PowerSource, c.Stress = scheme(st.PowerSource), &st
	}
	if c.PowerCurve != nil {
		pc := *c.PowerCurve
		pc.Source, c.PowerCurve = scheme(pc.Source), &pc
	}
	if c.Power != nil {
		p := *c.Power
		p.Source, c.Power = scheme(p.Source), &p
	}
	if c.Web != nil {
		w := *c.Web
		w.PowerSource, c.Web = scheme(w.PowerSource), &w
	}
	return &c
}

// scrubber replaces names with the alias and other identifying strings with
// "(hidden)", longest first so a name inside a longer string goes with it.
func scrubber(alias string, names, hidden []string) func(string) string {
	type rep struct{ old, new string }
	var reps []rep
	add := func(s, with string) {
		s = strings.TrimSpace(s)
		if len(s) < 2 {
			return
		}
		reps = append(reps, rep{s, with}, rep{strings.ToLower(s), with})
	}
	for _, n := range names {
		add(n, alias)
	}
	for _, h := range hidden {
		add(h, "(hidden)")
	}
	slices.SortStableFunc(reps, func(a, b rep) int { return cmp.Compare(len(b.old), len(a.old)) })
	var args []string
	for _, r := range reps {
		args = append(args, r.old, r.new)
	}
	if len(args) == 0 {
		return func(s string) string { return s }
	}
	return strings.NewReplacer(args...).Replace
}

// scheme keeps the kind of a power source ("redfish", "ipmi", "rapl") and
// drops the BMC address and user.
func scheme(src string) string {
	if i := strings.Index(src, "://"); i >= 0 {
		return src[:i]
	}
	if i := strings.IndexByte(src, '@'); i >= 0 {
		return src[:i]
	}
	return src
}
