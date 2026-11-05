package hardware

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

var implementers = map[string]string{
	"0x41": "ARM",
	"0x42": "Broadcom",
	"0x43": "Cavium",
	"0x46": "Fujitsu",
	"0x48": "HiSilicon",
	"0x4e": "NVIDIA",
	"0x50": "APM",
	"0x51": "Qualcomm",
	"0x61": "Apple",
	"0xc0": "Ampere",
}

// parts names common server cores by MIDR implementer/part, following the
// CPU part numbers in Linux arch/arm64/include/asm/cputype.h. Unknown parts are
// shown as hex, so a missing entry never mislabels a CPU.
var parts = map[string]string{
	"0x41/0xd08": "Cortex-A72",
	"0x41/0xd0b": "Cortex-A76",
	"0x41/0xd0c": "Neoverse-N1",
	"0x41/0xd40": "Neoverse-V1",
	"0x41/0xd49": "Neoverse-N2",
	"0x41/0xd4f": "Neoverse-V2",
	"0x41/0xd84": "Neoverse-V3",
	"0x41/0xd8e": "Neoverse-N3",
	"0x43/0x0af": "ThunderX2",
	"0x46/0x001": "A64FX",
	"0x48/0xd01": "TaiShan-v110 (Kunpeng 920)",
	"0x48/0xd02": "TaiShan-v120",
	// ARMv9 (SVE2, SHA512); name from openEuler kernel patches (HISI_CPU_PART_HIP10).
	"0x48/0xd03": "HiSilicon HIP10 (ARMv9)",
	"0xc0/0xac3": "Ampere-1",
	"0xc0/0xac4": "Ampere-1a",
}

// DescribeMIDR turns an arm64 MIDR into vendor and model names. Unknown parts
// are reported as hex rather than guessed.
func DescribeMIDR(m MIDR) (vendor, model string) {
	impl := strings.ToLower(m.Implementer)
	part := strings.ToLower(m.Part)
	vendor, ok := implementers[impl]
	if !ok {
		vendor = "implementer " + impl
	}
	model, ok = parts[impl+"/"+part]
	if !ok {
		model = fmt.Sprintf("%s part %s", vendor, part)
	}
	return vendor, model
}

// EffectiveCPUs is the number of CPUs cpubench can really use: the affinity
// mask, further limited by a cgroup CPU quota.
func EffectiveCPUs(allowed int, cg *Cgroup) int {
	n := allowed
	if cg != nil {
		if len(cg.Cpuset) > 0 && len(cg.Cpuset) < n {
			n = len(cg.Cpuset)
		}
		if cg.CPUQuotaCores > 0 {
			if q := int(math.Ceil(cg.CPUQuotaCores)); q < n {
				n = q
			}
		}
	}
	return max(n, 1)
}

// PlacementOrder orders allowed CPUs so that taking the first N spreads N
// threads over distinct physical cores first (NUMA node by node), and only
// then fills SMT siblings. This keeps "4 threads" meaning "4 cores" on both
// x86 (SMT) and arm64 machines.
func PlacementOrder(t Topology) []int {
	allowed := t.AllowedCPUs
	isAllowed := func(c int) bool { return len(allowed) == 0 || slices.Contains(allowed, c) }

	groups := t.CoreGroups
	if len(groups) == 0 {
		for _, c := range allowed {
			groups = append(groups, []int{c})
		}
	}
	nodeOf := func(g []int) int {
		if n := t.NodeOf(g[0]); n >= 0 {
			return n
		}
		return 0
	}
	sorted := slices.Clone(groups)
	slices.SortStableFunc(sorted, func(a, b []int) int {
		if na, nb := nodeOf(a), nodeOf(b); na != nb {
			return na - nb
		}
		return a[0] - b[0]
	})

	var order []int
	for depth := 0; ; depth++ {
		added := false
		for _, g := range sorted {
			if depth < len(g) {
				added = true
				if isAllowed(g[depth]) {
					order = append(order, g[depth])
				}
			}
		}
		if !added {
			break
		}
	}
	// CPUs in the mask that topology did not describe.
	for _, c := range allowed {
		if !slices.Contains(order, c) {
			order = append(order, c)
		}
	}
	return order
}

// CPUsOnNode returns the allowed CPUs of a NUMA node in placement order.
func CPUsOnNode(t Topology, node int) []int {
	var out []int
	for _, c := range PlacementOrder(t) {
		if t.NodeOf(c) == node {
			out = append(out, c)
		}
	}
	return out
}
