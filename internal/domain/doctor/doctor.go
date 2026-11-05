// Package doctor holds environment checks. Every rule is a pure function of a
// hardware.System so it can be tested against fixtures from any machine.
package doctor

import (
	"fmt"
	"slices"
	"strings"

	"github.com/chaloemph/cpubench/internal/domain/hardware"
)

type Severity string

const (
	Info Severity = "info"
	Warn Severity = "warn"
	Fail Severity = "fail"
)

type Finding struct {
	ID       string   `json:"id"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
}

type Rule func(s hardware.System) []Finding

// Check runs every rule.
func Check(s hardware.System) []Finding {
	var out []Finding
	for _, r := range Rules {
		out = append(out, r(s)...)
	}
	return out
}

var Rules = []Rule{
	ruleWSL,
	ruleGovernor,
	ruleVMCPUType,
	ruleARMAtomics,
	ruleISA,
	ruleCgroupLimits,
	ruleCgroupThrottled,
	ruleNUMA,
	ruleNICNUMA,
	ruleDIMMBalance,
	ruleDIMMUnknown,
	ruleDIMMChannels,
	ruleDIMMMixed,
	ruleDIMMSpeed,
	rulePCIeLink,
	rulePageSize,
	ruleSMT,
}

func one(id string, sev Severity, msg, fix string) []Finding {
	return []Finding{{ID: id, Severity: sev, Message: msg, Fix: fix}}
}

func ruleWSL(s hardware.System) []Finding {
	if s.Platform.Virt == "wsl" || strings.Contains(strings.ToLower(s.OS.Kernel), "microsoft") {
		return one("wsl", Warn, "running under WSL2: results are not representative of a server",
			"use for development only; benchmark on bare metal or a real VM")
	}
	return nil
}

func ruleGovernor(s hardware.System) []Finding {
	g := s.OS.Governor
	switch {
	case g == "":
		return nil // no cpufreq: VM or firmware-controlled
	case g != "performance":
		return one("governor", Warn, fmt.Sprintf("CPU governor is %q, results will vary with load", g),
			"sudo cpupower frequency-set -g performance")
	}
	return nil
}

// x86 VMs with the default Proxmox CPU type (kvm64 / x86-64-v2-AES) or an
// old VMware EVC baseline hide AVX2 and friends, which makes crypto and
// compression look much slower.
func ruleVMCPUType(s hardware.System) []Finding {
	if s.CPU.Arch != "amd64" || !s.Context.InVM {
		return nil
	}
	if !s.CPU.HasFeature("avx2") {
		why := "CPU type is probably kvm64/x86-64-v2"
		if s.Platform.Virt == "vmware" {
			why = "the cluster's EVC mode probably masks it"
		}
		return one("vm-cpu-type", Warn, "VM does not expose AVX2: "+why, hostCPUFix(s))
	}
	return nil
}

// hostCPUFix says how to give a VM the host's CPU features on its hypervisor.
func hostCPUFix(s hardware.System) string {
	if s.Platform.Virt == "vmware" {
		return "VMware: raise or disable the cluster EVC mode, or move the VM to a host/cluster without an EVC baseline"
	}
	return "Proxmox: qm set <vmid> --cpu host"
}

func ruleARMAtomics(s hardware.System) []Finding {
	if s.CPU.Arch != "arm64" || len(s.CPU.Features) == 0 {
		return nil
	}
	if !s.CPU.HasFeature("atomics") {
		return one("arm-lse", Warn, "CPU does not report LSE atomics: locks and atomics will be slow",
			"check the CPU/VM model; ARMv8.1 and later server cores support LSE")
	}
	return one("arm-lse", Info, "LSE atomics available; make sure runtimes use them (Go: automatic, JDK: UseLSE, GCC>=10: -moutline-atomics)", "")
}

// ruleISA calls out instruction set extensions the CPU lacks, so a
// comparison is not read as raw CPU speed where one side runs fallback code.
func ruleISA(s hardware.System) []Finding {
	c := s.CPU
	if len(c.Features) == 0 {
		return nil
	}
	lacks := func(fs ...string) bool {
		for _, f := range fs {
			if !c.HasFeature(f) {
				return true
			}
		}
		return false
	}
	fix := "check the CPU model; in a VM expose the host CPU (" + hostCPUFix(s) + ")"
	var out []Finding
	switch c.Arch {
	case "arm64":
		if lacks("aes", "pmull") {
			out = append(out, Finding{ID: "isa-aes", Severity: Warn, Fix: fix,
				Message: "CPU lacks ARMv8 AES/PMULL: AES-GCM (TLS bulk data) runs in software"})
		}
		if lacks("sha2") {
			out = append(out, Finding{ID: "isa-sha2", Severity: Warn, Fix: fix,
				Message: "CPU lacks ARMv8 SHA2: SHA-256 runs in software"})
		}
		if lacks("sha512") {
			out = append(out, Finding{ID: "isa-sha512", Severity: Info,
				Message: "no ARMv8 SHA512 extension (common on ARMv8.2 cores such as Neoverse N1): SHA-384/512 run in software, e.g. TLS suites with SHA-384 and HS512/RS512 JWTs"})
		}
		if lacks("sve") {
			out = append(out, Finding{ID: "isa-sve", Severity: Info,
				Message: "no SVE: vector code is 128-bit NEON only. Go does not use SVE, so cpubench is unaffected, but C/C++/Java code vectorised for SVE will not get it"})
		}
	case "amd64":
		if lacks("aes", "pclmulqdq") {
			out = append(out, Finding{ID: "isa-aes", Severity: Warn, Fix: fix,
				Message: "CPU lacks AES-NI/PCLMULQDQ: AES-GCM (TLS bulk data) runs in software"})
		}
		if lacks("sha_ni") {
			out = append(out, Finding{ID: "isa-sha-ni", Severity: Info,
				Message: "no SHA-NI (Xeon before Ice Lake): SHA-256 runs on AVX2, so a CPU with SHA instructions (SHA-NI or ARMv8 SHA2) looks better on crypto/sha256 than on general code"})
		}
	}
	return out
}

func ruleCgroupLimits(s hardware.System) []Finding {
	cg := s.Context.Cgroup
	if cg == nil {
		return nil
	}
	if cg.CPUQuotaCores > 0 || (len(cg.Cpuset) > 0 && len(cg.Cpuset) < s.CPU.LogicalCPUs) {
		return one("cgroup-limit", Info, fmt.Sprintf("cgroup limits cpubench to %d effective CPUs", s.Context.EffectiveCPUs),
			"thread counts are derived from effective CPUs, not nproc")
	}
	return nil
}

func ruleCgroupThrottled(s hardware.System) []Finding {
	cg := s.Context.Cgroup
	if cg == nil || cg.NrThrottled == 0 {
		return nil
	}
	return one("cgroup-throttled", Warn, fmt.Sprintf("cgroup was CPU-throttled %d times (%.1fs)", cg.NrThrottled, float64(cg.ThrottledUsec)/1e6),
		"give the pod/container requests=limits (Guaranteed QoS) or use fewer threads")
}

func ruleNUMA(s hardware.System) []Finding {
	if n := len(s.Topology.NUMANodes); n > 1 {
		return one("numa", Info, fmt.Sprintf("%d NUMA nodes: compare whole-machine runs with per-node runs (--numa-node)", n),
			"Proxmox: numa=1 and pin vCPUs to one node; K8s: topologyManagerPolicy=single-numa-node")
	}
	return nil
}

func ruleNICNUMA(s hardware.System) []Finding {
	if len(s.Topology.NUMANodes) < 2 {
		return nil
	}
	var out []Finding
	for _, n := range s.NICs {
		if n.NUMANode < 0 || n.SpeedMbps <= 0 {
			continue
		}
		msg := fmt.Sprintf("NIC %s is attached to NUMA node %d", n.Name, n.NUMANode)
		if s.OS.IRQBalance {
			out = append(out, Finding{ID: "nic-numa", Severity: Warn, Message: msg + " and irqbalance may spread its IRQs to other nodes",
				Fix: "pin NIC IRQs and the web workload to the NIC's node for web tests"})
		} else {
			out = append(out, Finding{ID: "nic-numa", Severity: Info, Message: msg})
		}
	}
	return out
}

func ruleDIMMBalance(s hardware.System) []Finding {
	d := s.Memory.DIMMs
	if len(d) == 0 || s.CPU.Sockets < 2 {
		return nil
	}
	if len(d)%s.CPU.Sockets != 0 {
		return one("dimm-balance", Warn, fmt.Sprintf("%d DIMMs across %d sockets: population is unbalanced", len(d), s.CPU.Sockets),
			"populate the same channels on every socket; memory bandwidth drops a lot otherwise")
	}
	return nil
}

// ruleDIMMUnknown says so when DIMM details are missing on bare metal, because
// the DIMM rules below then skip silently and a clean doctor would read as
// "memory is fine" when it was not checked. dmidecode needs root.
func ruleDIMMUnknown(s hardware.System) []Finding {
	if s.Context.InVM || s.Memory.TotalMB == 0 || len(s.Memory.DIMMs) > 0 {
		return nil
	}
	return one("dimm-unknown", Warn, "no DIMM details: the DIMM population, speed and mix checks were skipped",
		"run as root with dmidecode installed (sudo cpubench doctor)")
}

// ruleDIMMChannels spots empty memory channels from the slot count. Servers
// have at most two DIMMs per channel, so with every channel populated at least
// half the slots hold a DIMM; fewer means a channel is empty, and memory
// bandwidth drops by that channel's share. dmidecode inside a VM lists the
// hypervisor's virtual DIMMs, so only bare metal is checked.
func ruleDIMMChannels(s hardware.System) []Finding {
	m := s.Memory
	if s.Context.InVM || m.Slots == 0 || len(m.DIMMs) == 0 || s.CPU.Sockets < 1 {
		return nil
	}
	per, slots := len(m.DIMMs)/s.CPU.Sockets, m.Slots/s.CPU.Sockets
	if per*2 < slots {
		return one("dimm-channels", Warn, fmt.Sprintf("%d of %d DIMM slots per socket populated: at least one memory channel is empty", per, slots),
			"populate one DIMM in every channel of every socket (see the vendor's population guide); compare memory bandwidth only between machines populated the same way")
	}
	return nil
}

// ruleDIMMMixed flags DIMMs of different size, speed or part: the memory
// controller runs all of them at the slowest, and uneven sizes break
// interleaving.
func ruleDIMMMixed(s hardware.System) []Finding {
	kinds := map[string]bool{}
	for _, d := range s.Memory.DIMMs {
		kinds[fmt.Sprintf("%d MB %s %s", d.SizeMB, d.Speed, strings.TrimSpace(d.Part))] = true
	}
	if len(kinds) > 1 {
		var ks []string
		for k := range kinds {
			ks = append(ks, k)
		}
		slices.Sort(ks)
		return one("dimm-mixed", Warn, fmt.Sprintf("DIMMs are not identical (%s)", strings.Join(ks, "; ")),
			"use one DIMM part throughout; mixed sizes or speeds lower bandwidth and break interleaving")
	}
	return nil
}

// ruleDIMMSpeed reports DIMMs running below their rated speed. The CPU's
// memory speed and two DIMMs per channel both lower it legitimately, but it
// changes every memory number, so it belongs next to them.
func ruleDIMMSpeed(s hardware.System) []Finding {
	for _, d := range s.Memory.DIMMs {
		rated, conf := hardware.MTs(d.Speed), hardware.MTs(d.Configured)
		if rated > 0 && conf > 0 && conf < rated {
			return one("dimm-speed", Warn, fmt.Sprintf("DIMMs run at %d MT/s, rated %d MT/s", conf, rated),
				"the CPU's maximum memory speed or 2 DIMMs per channel can cap it; check the BIOS memory frequency setting and the vendor's population guide")
		}
	}
	return nil
}

// rulePCIeLink flags storage and network controllers whose link trained
// below the device's maximum: wrong slot, riser or a bad link caps their
// throughput whatever the CPU does.
func rulePCIeLink(s hardware.System) []Finding {
	if s.Context.InVM {
		return nil // virtual devices report made-up links
	}
	var out []Finding
	for _, d := range s.PCI {
		if !d.Degraded() {
			continue
		}
		out = append(out, Finding{ID: "pcie-link", Severity: Warn,
			Message: fmt.Sprintf("%s controller %s (%s:%s, %s) links at %s x%d, capable of %s x%d",
				d.Kind, d.Address, d.VendorID, d.DeviceID, orNone(d.Driver), d.LinkSpeed, d.LinkWidth, d.MaxLinkSpeed, d.MaxLinkWidth),
			Fix: "check the slot and riser match the card; some devices lower the link at idle (ASPM), so re-check under load"})
	}
	return out
}

func orNone(s string) string {
	if s == "" {
		return "no driver"
	}
	return s
}

func rulePageSize(s hardware.System) []Finding {
	if p := s.Topology.PageSize; p > 4096 {
		return one("page-size", Info, fmt.Sprintf("kernel page size is %dK while most x86-64 and arm64 kernels use 4K: memory-heavy rows are not like for like", p/1024), "")
	}
	return nil
}

func ruleSMT(s hardware.System) []Finding {
	if s.CPU.ThreadsPerCore > 1 {
		return one("smt", Info, fmt.Sprintf("SMT on (%d threads/core): thread counts up to %d use one thread per physical core", s.CPU.ThreadsPerCore, s.CPU.PhysicalCores()), "")
	}
	return nil
}

// Comparable reports differences between two systems that make a comparison
// unfair (different OS release, kernel, Go version).
func Comparable(a, b hardware.System) []Finding {
	var out []Finding
	if a.OS.Version != b.OS.Version {
		out = append(out, Finding{ID: "os-mismatch", Severity: Warn,
			Message: fmt.Sprintf("OS differs: %s %s vs %s %s", a.OS.Name, a.OS.Version, b.OS.Name, b.OS.Version),
			Fix:     "use the same Ubuntu release on both machines"})
	}
	if kernelMajor(a.OS.Kernel) != kernelMajor(b.OS.Kernel) {
		out = append(out, Finding{ID: "kernel-mismatch", Severity: Warn,
			Message: fmt.Sprintf("kernel differs: %s vs %s", a.OS.Kernel, b.OS.Kernel),
			Fix:     "on 22.04 install linux-generic-hwe-22.04 to match 24.04's 6.8"})
	}
	if a.OS.GoVersion != b.OS.GoVersion {
		out = append(out, Finding{ID: "tool-mismatch", Severity: Warn,
			Message: fmt.Sprintf("cpubench built with different Go: %s vs %s", a.OS.GoVersion, b.OS.GoVersion)})
	}
	if a.Context.Mode != b.Context.Mode {
		out = append(out, Finding{ID: "mode-mismatch", Severity: Info,
			Message: fmt.Sprintf("execution context differs: %s vs %s", a.Context.Mode, b.Context.Mode)})
	}
	// Same mode on different hypervisors is still a different layer: CPU
	// model (EVC vs host), scheduler and virtual devices all differ.
	if a.Context.InVM && b.Context.InVM && a.Layer() != b.Layer() {
		out = append(out, Finding{ID: "virt-mismatch", Severity: Warn,
			Message: fmt.Sprintf("hypervisor differs: %s vs %s, so part of any difference is the hypervisor, not the hardware", a.Layer(), b.Layer()),
			Fix:     "run both on the same hypervisor, or add a run on bare metal or the other hypervisor to measure the gap"})
	}
	return out
}

func kernelMajor(k string) string {
	parts := strings.SplitN(k, ".", 3)
	if len(parts) < 2 {
		return k
	}
	return parts[0] + "." + parts[1]
}
