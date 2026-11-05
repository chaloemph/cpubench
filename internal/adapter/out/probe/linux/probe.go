// Package linux implements port.SystemProbe and port.Sensors by reading
// /proc and /sys. Root can point at a captured file tree (testdata/fixtures)
// so the same parser is tested against x86 and arm64 machines without
// running on them.
package linux

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/chaloemph/cpubench/internal/domain/hardware"
)

type Probe struct {
	Root string
	// Live enables syscalls, environment and helper binaries; off for fixtures.
	Live bool
}

func New() *Probe { return &Probe{Root: "/", Live: true} }

// Fixture reads a captured tree without touching the running machine.
func Fixture(root string) *Probe { return &Probe{Root: root} }

func (p *Probe) path(parts ...string) string {
	return filepath.Join(append([]string{p.Root}, parts...)...)
}

func (p *Probe) read(parts ...string) string {
	b, err := os.ReadFile(p.path(parts...))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (p *Probe) readInt(def int, parts ...string) int {
	v, err := strconv.Atoi(p.read(parts...))
	if err != nil {
		return def
	}
	return v
}

func (p *Probe) System(ctx context.Context) (hardware.System, error) {
	var s hardware.System
	s.Hostname = p.hostname()
	cpu, err := p.cpuinfo()
	if err != nil {
		return s, err
	}
	s.CPU = cpu
	s.Topology = p.topology(&s.CPU)
	s.Platform = p.platform(ctx, s.CPU)
	s.OS = p.os()
	s.Context = p.context(s.Platform, s.Topology)
	s.Memory = p.memory(ctx)
	s.NICs = p.nics()
	s.PCI = p.pci()
	s.Disks = p.disks()
	return s, nil
}

func (p *Probe) hostname() string {
	if p.Live {
		h, _ := os.Hostname()
		return h
	}
	return p.read("etc", "hostname")
}

func (p *Probe) cpuinfo() (hardware.CPUInfo, error) {
	f, err := os.Open(p.path("proc", "cpuinfo"))
	if err != nil {
		return hardware.CPUInfo{}, fmt.Errorf("read cpuinfo: %w", err)
	}
	defer f.Close()
	first := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if _, seen := first[k]; !seen {
			first[k] = v
		}
	}
	var c hardware.CPUInfo
	if impl, ok := first["CPU implementer"]; ok {
		c.Arch, c.Machine = "arm64", "aarch64"
		c.MIDR = &hardware.MIDR{Implementer: impl, Part: first["CPU part"], Variant: first["CPU variant"], Revision: first["CPU revision"]}
		c.Vendor, c.Model = hardware.DescribeMIDR(*c.MIDR)
		c.Features = strings.Fields(first["Features"])
	} else {
		c.Arch, c.Machine = "amd64", "x86_64"
		c.Vendor = first["vendor_id"]
		c.Model = first["model name"]
		c.Features = relevantX86(strings.Fields(first["flags"]))
		if mhz, err := strconv.ParseFloat(first["cpu MHz"], 64); err == nil {
			c.MaxMHz = mhz
		}
	}
	if khz := p.readInt(0, "sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq"); khz > 0 {
		c.MaxMHz = float64(khz) / 1000
	}
	return c, nil
}

// x86Relevant keeps the flags reports and doctor rules use, including those
// Go's crypto assembly requires (ssse3, sse4_1 alongside aes/pclmulqdq/sha_ni).
var x86Relevant = []string{"ssse3", "sse4_1", "sse4_2", "avx", "avx2", "fma", "avx512f", "avx512bw", "avx512vl", "avx512_vnni",
	"amx_tile", "aes", "vaes", "pclmulqdq", "vpclmulqdq", "sha_ni", "bmi2", "adx", "ht", "hypervisor", "erms", "fsrm"}

func relevantX86(flags []string) []string {
	var out []string
	for _, f := range flags {
		if slices.Contains(x86Relevant, f) {
			out = append(out, f)
		}
	}
	return out
}

func (p *Probe) topology(c *hardware.CPUInfo) hardware.Topology {
	var t hardware.Topology
	online := ParseCPUList(p.read("sys/devices/system/cpu/online"))
	c.LogicalCPUs = len(online)

	type key struct{ pkg, core int }
	groups := map[key][]int{}
	pkgs := map[int]bool{}
	for _, cpu := range online {
		base := fmt.Sprintf("sys/devices/system/cpu/cpu%d/topology", cpu)
		pkg := max(p.readInt(0, base, "physical_package_id"), 0)
		core := p.readInt(cpu, base, "core_id")
		k := key{pkg, core}
		groups[k] = append(groups[k], cpu)
		pkgs[pkg] = true
	}
	for _, g := range groups {
		slices.Sort(g)
		t.CoreGroups = append(t.CoreGroups, g)
		c.ThreadsPerCore = max(c.ThreadsPerCore, len(g))
	}
	slices.SortFunc(t.CoreGroups, func(a, b []int) int { return a[0] - b[0] })
	c.Sockets = max(len(pkgs), 1)
	c.CoresPerSocket = len(groups) / c.Sockets
	t.SMTActive = p.read("sys/devices/system/cpu/smt/active") == "1" || c.ThreadsPerCore > 1

	for i := 0; i < 10; i++ {
		idx := fmt.Sprintf("sys/devices/system/cpu/cpu0/cache/index%d", i)
		lvl := p.readInt(0, idx, "level")
		if lvl == 0 {
			break
		}
		// VMs can expose a cache index without a size: drop it rather than report "0K".
		if kb := parseSizeKB(p.read(idx, "size")); kb > 0 {
			c.Caches = append(c.Caches, hardware.Cache{Level: lvl, Type: p.read(idx, "type"),
				SizeKB: kb, SharedCPUs: len(ParseCPUList(p.read(idx, "shared_cpu_list")))})
		}
	}

	nodes, _ := filepath.Glob(p.path("sys/devices/system/node/node[0-9]*"))
	for _, n := range nodes {
		id, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(n), "node"))
		if err != nil {
			continue
		}
		rel := strings.TrimPrefix(n, p.Root)
		node := hardware.NUMANode{ID: id, CPUs: ParseCPUList(p.read(rel, "cpulist"))}
		for _, f := range strings.Fields(p.read(rel, "distance")) {
			d, _ := strconv.Atoi(f)
			node.Distances = append(node.Distances, d)
		}
		for line := range strings.SplitSeq(p.read(rel, "meminfo"), "\n") {
			if strings.Contains(line, "MemTotal:") {
				fs := strings.Fields(line)
				if len(fs) >= 4 {
					kb, _ := strconv.Atoi(fs[3])
					node.MemTotalMB = kb / 1024
				}
			}
		}
		t.NUMANodes = append(t.NUMANodes, node)
	}
	slices.SortFunc(t.NUMANodes, func(a, b hardware.NUMANode) int { return a.ID - b.ID })
	if len(t.NUMANodes) == 0 {
		t.NUMANodes = []hardware.NUMANode{{ID: 0, CPUs: online}}
	}

	t.PageSize = p.readInt(4096, "pagesize")
	t.AllowedCPUs = online
	if p.Live {
		t.PageSize = os.Getpagesize()
		var set unix.CPUSet
		if err := unix.SchedGetaffinity(0, &set); err == nil {
			t.AllowedCPUs = nil
			for _, cpu := range online {
				if set.IsSet(cpu) {
					t.AllowedCPUs = append(t.AllowedCPUs, cpu)
				}
			}
		}
	}
	return t
}

func (p *Probe) platform(ctx context.Context, c hardware.CPUInfo) hardware.Platform {
	pl := hardware.Platform{
		Vendor:     p.read("sys/class/dmi/id/sys_vendor"),
		Product:    p.read("sys/class/dmi/id/product_name"),
		BIOS:       p.read("sys/class/dmi/id/bios_version"),
		BIOSVendor: p.read("sys/class/dmi/id/bios_vendor"),
		BIOSDate:   p.read("sys/class/dmi/id/bios_date"),
	}
	kernel := strings.ToLower(p.read("proc/sys/kernel/osrelease"))
	switch {
	case strings.Contains(kernel, "microsoft"):
		pl.Virt = "wsl"
	case p.Live && haveBin("systemd-detect-virt"):
		out, _ := exec.CommandContext(ctx, "systemd-detect-virt", "--vm").Output()
		pl.Virt = strings.TrimSpace(string(out))
	}
	if pl.Virt == "" {
		v := strings.ToLower(pl.Vendor + " " + pl.Product)
		switch {
		case strings.Contains(v, "qemu") || strings.Contains(v, "kvm") || strings.Contains(v, "standard pc"):
			pl.Virt = "kvm"
		case strings.Contains(v, "vmware"):
			pl.Virt = "vmware"
		case c.HasFeature("hypervisor"):
			pl.Virt = "vm"
		default:
			pl.Virt = "none"
		}
	}
	return pl
}

func haveBin(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (p *Probe) os() hardware.OS {
	o := hardware.OS{Kernel: p.read("proc/sys/kernel/osrelease"), GoVersion: runtime.Version()}
	for line := range strings.SplitSeq(p.read("etc/os-release"), "\n") {
		k, v, _ := strings.Cut(line, "=")
		v = strings.Trim(v, `"`)
		switch k {
		case "NAME":
			o.Name = v
		case "VERSION_ID":
			o.Version = v
		}
	}
	o.THP = bracketed(p.read("sys/kernel/mm/transparent_hugepage/enabled"))
	o.Governor = p.read("sys/devices/system/cpu/cpu0/cpufreq/scaling_governor")
	if b := p.read("sys/devices/system/cpu/cpufreq/boost"); b != "" {
		o.Boost = map[string]string{"1": "on", "0": "off"}[b]
	} else if nt := p.read("sys/devices/system/cpu/intel_pstate/no_turbo"); nt != "" {
		o.Boost = map[string]string{"0": "on", "1": "off"}[nt]
	}
	vulns, _ := filepath.Glob(p.path("sys/devices/system/cpu/vulnerabilities/*"))
	active := 0
	for _, v := range vulns {
		b, _ := os.ReadFile(v)
		if strings.HasPrefix(string(b), "Mitigation") {
			active++
		}
	}
	if len(vulns) > 0 {
		o.Mitigations = fmt.Sprintf("%d of %d mitigations active", active, len(vulns))
	}
	o.IRQBalance = p.processRunning("irqbalance")
	return o
}

func (p *Probe) processRunning(name string) bool {
	comms, _ := filepath.Glob(p.path("proc/[0-9]*/comm"))
	for _, c := range comms {
		b, _ := os.ReadFile(c)
		if strings.TrimSpace(string(b)) == name {
			return true
		}
	}
	return false
}

func bracketed(s string) string {
	if i := strings.Index(s, "["); i >= 0 {
		if j := strings.Index(s[i:], "]"); j > 0 {
			return s[i+1 : i+j]
		}
	}
	return s
}

func (p *Probe) cgroupDir() string {
	rel := "/"
	for line := range strings.SplitSeq(p.read("proc/self/cgroup"), "\n") {
		if strings.HasPrefix(line, "0::") {
			rel = strings.TrimPrefix(line, "0::")
		}
	}
	return filepath.Join("sys/fs/cgroup", rel)
}

func (p *Probe) cgroup() *hardware.Cgroup {
	if _, err := os.Stat(p.path("sys/fs/cgroup/cgroup.controllers")); err != nil {
		return nil
	}
	dir := p.cgroupDir()
	cg := &hardware.Cgroup{Version: 2}
	if f := strings.Fields(p.read(dir, "cpu.max")); len(f) == 2 && f[0] != "max" {
		q, _ := strconv.ParseFloat(f[0], 64)
		per, _ := strconv.ParseFloat(f[1], 64)
		if per > 0 {
			cg.CPUQuotaCores = q / per
		}
	}
	cg.Cpuset = ParseCPUList(p.read(dir, "cpuset.cpus.effective"))
	if m := p.read(dir, "memory.max"); m != "" && m != "max" {
		b, _ := strconv.ParseInt(m, 10, 64)
		cg.MemMaxMB = int(b >> 20)
	}
	cg.NrThrottled, cg.ThrottledUsec = parseCPUStat(p.read(dir, "cpu.stat"))
	return cg
}

func parseCPUStat(s string) (nr, usec int64) {
	for line := range strings.SplitSeq(s, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		v, _ := strconv.ParseInt(f[1], 10, 64)
		switch f[0] {
		case "nr_throttled":
			nr = v
		case "throttled_usec":
			usec = v
		}
	}
	return nr, usec
}

func (p *Probe) context(pl hardware.Platform, t hardware.Topology) hardware.ExecContext {
	ec := hardware.ExecContext{InVM: pl.Virt != "none" && pl.Virt != ""}
	if _, err := os.Stat(p.path(".dockerenv")); err == nil {
		ec.Container = "docker"
	} else if _, err := os.Stat(p.path("run/.containerenv")); err == nil {
		ec.Container = "podman"
	}
	if p.Live && os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		ec.K8s = &hardware.K8sInfo{Node: os.Getenv("NODE_NAME"), Pod: os.Getenv("POD_NAME"), Namespace: os.Getenv("POD_NAMESPACE")}
		if ec.Container == "" {
			ec.Container = "containerd"
		}
	}
	switch {
	case ec.K8s != nil:
		ec.Mode = hardware.ModeK8s
	case ec.Container != "":
		ec.Mode = hardware.ModeContainer
	case ec.InVM:
		ec.Mode = hardware.ModeVM
	default:
		ec.Mode = hardware.ModeNative
	}
	ec.Cgroup = p.cgroup()
	ec.EffectiveCPUs = hardware.EffectiveCPUs(len(t.AllowedCPUs), ec.Cgroup)
	return ec
}

func (p *Probe) memory(ctx context.Context) hardware.Memory {
	var m hardware.Memory
	for line := range strings.SplitSeq(p.read("proc/meminfo"), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				kb, _ := strconv.Atoi(f[1])
				m.TotalMB = kb / 1024
			}
		}
	}
	dmi := p.read("dmidecode-17.txt")
	if p.Live && os.Geteuid() == 0 && haveBin("dmidecode") {
		out, err := exec.CommandContext(ctx, "dmidecode", "-t", "17").Output()
		if err == nil {
			dmi = string(out)
		}
	}
	m.DIMMs, m.Slots = ParseDMIDecode17(dmi)
	return m
}

// ParseDMIDecode17 extracts populated DIMMs from `dmidecode -t 17` output,
// and counts every slot listed, empty or not.
func ParseDMIDecode17(s string) (dimms []hardware.DIMM, slots int) {
	var cur *hardware.DIMM
	flush := func() {
		if cur != nil && cur.SizeMB > 0 {
			dimms = append(dimms, *cur)
		}
		cur = nil
	}
	for line := range strings.SplitSeq(s, "\n") {
		t := strings.TrimSpace(line)
		if t == "Memory Device" {
			flush()
			cur = &hardware.DIMM{}
			slots++
			continue
		}
		if cur == nil {
			continue
		}
		k, v, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "Size":
			cur.SizeMB = parseDIMMSize(v)
		case "Locator":
			cur.Locator = v
		case "Bank Locator":
			cur.Bank = v
		case "Type":
			cur.Type = v
		case "Speed":
			cur.Speed = v
		case "Configured Memory Speed", "Configured Clock Speed": // name before dmidecode 3.0
			cur.Configured = v
		case "Rank":
			cur.Rank, _ = strconv.Atoi(v)
		case "Manufacturer":
			cur.Manufacturer = v
		case "Part Number":
			cur.Part = v
		}
	}
	flush()
	return dimms, slots
}

func parseDIMMSize(v string) int {
	f := strings.Fields(v)
	if len(f) != 2 {
		return 0
	}
	n, err := strconv.Atoi(f[0])
	if err != nil {
		return 0
	}
	switch strings.ToUpper(f[1]) {
	case "GB":
		return n * 1024
	case "TB":
		return n * 1024 * 1024
	case "MB":
		return n
	}
	return 0
}

func (p *Probe) nics() []hardware.NIC {
	devs, _ := filepath.Glob(p.path("sys/class/net/*"))
	var out []hardware.NIC
	for _, d := range devs {
		if _, err := os.Stat(filepath.Join(d, "device")); err != nil {
			continue // virtual interface
		}
		rel := strings.TrimPrefix(d, p.Root)
		n := hardware.NIC{Name: filepath.Base(d), NUMANode: p.readInt(-1, rel, "device/numa_node"), SpeedMbps: p.readInt(0, rel, "speed")}
		qs, _ := filepath.Glob(filepath.Join(d, "queues/rx-*"))
		n.RxQueues = len(qs)
		if link, err := os.Readlink(filepath.Join(d, "device/driver")); err == nil {
			n.Driver = filepath.Base(link)
		}
		out = append(out, n)
	}
	return out
}

// pciKinds are the PCI base classes worth listing: what differs between
// vendors' builds of the same CPU is mostly the storage and network gear.
var pciKinds = map[string]string{"0x01": "storage", "0x02": "network"}

func (p *Probe) pci() []hardware.PCIDevice {
	devs, _ := filepath.Glob(p.path("sys/bus/pci/devices/*"))
	var out []hardware.PCIDevice
	for _, d := range devs {
		rel := strings.TrimPrefix(d, p.Root)
		class := p.read(rel, "class")
		kind, ok := pciKinds[class[:min(4, len(class))]]
		if !ok {
			continue
		}
		dev := hardware.PCIDevice{Address: filepath.Base(d), Class: class, Kind: kind,
			VendorID: p.read(rel, "vendor"), DeviceID: p.read(rel, "device"), NUMANode: p.readInt(-1, rel, "numa_node"),
			LinkSpeed: p.read(rel, "current_link_speed"), LinkWidth: p.readInt(0, rel, "current_link_width"),
			MaxLinkSpeed: p.read(rel, "max_link_speed"), MaxLinkWidth: p.readInt(0, rel, "max_link_width")}
		if link, err := os.Readlink(filepath.Join(d, "driver")); err == nil {
			dev.Driver = filepath.Base(link)
		}
		out = append(out, dev)
	}
	return out
}

// disks lists block devices backed by hardware (loop, dm, md and zram have
// no device link; optical drives are skipped).
func (p *Probe) disks() []hardware.Disk {
	devs, _ := filepath.Glob(p.path("sys/block/*"))
	var out []hardware.Disk
	for _, d := range devs {
		if _, err := os.Stat(filepath.Join(d, "device")); err != nil || strings.HasPrefix(filepath.Base(d), "sr") {
			continue // virtual, or an optical drive
		}
		rel := strings.TrimPrefix(d, p.Root)
		sectors, _ := strconv.ParseFloat(p.read(rel, "size"), 64)
		out = append(out, hardware.Disk{Name: filepath.Base(d), Model: p.read(rel, "device/model"),
			SizeGB: sectors * 512 / 1e9, Rotational: p.read(rel, "queue/rotational") == "1"})
	}
	return out
}

// ParseCPUList parses kernel CPU lists such as "0-3,8,10-11".
func ParseCPUList(s string) []int {
	var out []int
	for part := range strings.SplitSeq(strings.TrimSpace(s), ",") {
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil {
				continue
			}
		}
		for i := a; i <= b; i++ {
			out = append(out, i)
		}
	}
	return out
}

func parseSizeKB(s string) int {
	s = strings.TrimSpace(s)
	mult := 1
	switch {
	case strings.HasSuffix(s, "K"):
		s = strings.TrimSuffix(s, "K")
	case strings.HasSuffix(s, "M"):
		s, mult = strings.TrimSuffix(s, "M"), 1024
	}
	n, _ := strconv.Atoi(s)
	return n * mult
}
