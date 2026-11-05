// Package hardware holds pure value types describing the machine under test.
// Nothing here performs I/O; adapters fill these structs.
package hardware

import (
	"math"
	"slices"
	"strconv"
	"strings"
)

// System is a full snapshot of the machine and the context cpubench runs in.
type System struct {
	Hostname string      `json:"hostname"`
	CPU      CPUInfo     `json:"cpu"`
	Topology Topology    `json:"topology"`
	Platform Platform    `json:"platform"`
	OS       OS          `json:"os"`
	Context  ExecContext `json:"context"`
	Memory   Memory      `json:"memory"`
	NICs     []NIC       `json:"nics,omitempty"`
	// PCI lists storage and network controllers: what each vendor put in the
	// box differs even when the CPUs are the same.
	PCI   []PCIDevice `json:"pci,omitempty"`
	Disks []Disk      `json:"disks,omitempty"`
}

// Layer is the execution context together with the hypervisor underneath, so
// a VM on VMware and a VM on Proxmox (KVM) are different layers: their CPU
// models, schedulers and virtual devices differ.
func (s System) Layer() string {
	m := string(s.Context.Mode)
	if !s.Context.InVM {
		return m
	}
	switch v := s.Platform.Virt; v {
	case "", "none", "vm":
		return m // hypervisor unknown
	default:
		return m + "/" + v
	}
}

// UsableCores is the number of physical cores (as this OS sees them) the run
// could use: cores with at least one allowed CPU, capped by a cgroup quota.
// In a VM these are the guest's cores, i.e. vCPUs.
func (s System) UsableCores() int {
	n := 0
	for _, g := range s.Topology.CoreGroups {
		if slices.ContainsFunc(g, func(c int) bool { return slices.Contains(s.Topology.AllowedCPUs, c) }) {
			n++
		}
	}
	if n == 0 {
		n = s.Context.EffectiveCPUs / max(1, s.CPU.ThreadsPerCore)
	}
	if cg := s.Context.Cgroup; cg != nil && cg.CPUQuotaCores > 0 {
		n = min(n, int(math.Ceil(cg.CPUQuotaCores)))
	}
	return n
}

type CPUInfo struct {
	Arch           string   `json:"arch"`    // GOARCH: amd64, arm64
	Machine        string   `json:"machine"` // uname -m: x86_64, aarch64
	Vendor         string   `json:"vendor"`
	Model          string   `json:"model"`
	MIDR           *MIDR    `json:"midr,omitempty"`
	Sockets        int      `json:"sockets"`
	CoresPerSocket int      `json:"cores_per_socket"`
	ThreadsPerCore int      `json:"threads_per_core"`
	LogicalCPUs    int      `json:"logical_cpus"`
	MaxMHz         float64  `json:"max_mhz,omitempty"`
	Features       []string `json:"features"`
	Caches         []Cache  `json:"caches,omitempty"`
}

// HasFeature reports whether a CPU flag (x86 "flags" / arm64 "Features") is present.
func (c CPUInfo) HasFeature(f string) bool { return slices.Contains(c.Features, f) }

// PhysicalCores is sockets × cores per socket.
func (c CPUInfo) PhysicalCores() int { return c.Sockets * c.CoresPerSocket }

type MIDR struct {
	Implementer string `json:"implementer"`
	Part        string `json:"part"`
	Variant     string `json:"variant,omitempty"`
	Revision    string `json:"revision,omitempty"`
}

type Cache struct {
	Level      int    `json:"level"`
	Type       string `json:"type"`
	SizeKB     int    `json:"size_kb"`
	SharedCPUs int    `json:"shared_cpus"`
}

type Topology struct {
	NUMANodes []NUMANode `json:"numa_nodes"`
	// CoreGroups lists the logical CPUs of each physical core (SMT siblings), ordered by first CPU.
	CoreGroups [][]int `json:"core_groups"`
	SMTActive  bool    `json:"smt_active"`
	PageSize   int     `json:"page_size"`
	// AllowedCPUs is the affinity mask of the cpubench process (cgroup cpuset / taskset).
	AllowedCPUs []int `json:"allowed_cpus"`
}

type NUMANode struct {
	ID         int   `json:"id"`
	CPUs       []int `json:"cpus"`
	MemTotalMB int   `json:"mem_total_mb"`
	Distances  []int `json:"distances,omitempty"`
}

// NodeOf returns the NUMA node that owns cpu, or -1.
func (t Topology) NodeOf(cpu int) int {
	for _, n := range t.NUMANodes {
		if slices.Contains(n.CPUs, cpu) {
			return n.ID
		}
	}
	return -1
}

type Platform struct {
	Vendor     string `json:"vendor,omitempty"`
	Product    string `json:"product,omitempty"`
	BIOS       string `json:"bios,omitempty"`
	BIOSVendor string `json:"bios_vendor,omitempty"`
	BIOSDate   string `json:"bios_date,omitempty"`
	// Virt is the hypervisor seen from inside: none, kvm, qemu, vmware, microsoft, wsl, ...
	Virt string `json:"virt"`
}

type OS struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Kernel      string `json:"kernel"`
	THP         string `json:"thp,omitempty"`
	Governor    string `json:"governor,omitempty"`
	Boost       string `json:"boost,omitempty"`
	IRQBalance  bool   `json:"irqbalance"`
	GoVersion   string `json:"go_version"`
	Mitigations string `json:"mitigations,omitempty"`
}

type Mode string

const (
	ModeNative    Mode = "native"
	ModeVM        Mode = "vm"
	ModeContainer Mode = "container"
	ModeK8s       Mode = "k8s"
)

type ExecContext struct {
	Mode          Mode     `json:"mode"`
	InVM          bool     `json:"in_vm"`
	Container     string   `json:"container,omitempty"` // docker, podman
	K8s           *K8sInfo `json:"k8s,omitempty"`
	Cgroup        *Cgroup  `json:"cgroup,omitempty"`
	EffectiveCPUs int      `json:"effective_cpus"`
}

type K8sInfo struct {
	Node      string `json:"node,omitempty"`
	Pod       string `json:"pod,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type Cgroup struct {
	Version       int     `json:"version"`
	CPUQuotaCores float64 `json:"cpu_quota_cores"` // 0 = unlimited
	Cpuset        []int   `json:"cpuset,omitempty"`
	MemMaxMB      int     `json:"mem_max_mb"` // 0 = unlimited
	NrThrottled   int64   `json:"nr_throttled"`
	ThrottledUsec int64   `json:"throttled_usec"`
}

type Memory struct {
	TotalMB int    `json:"total_mb"`
	DIMMs   []DIMM `json:"dimms,omitempty"`
	// Slots counts every memory device the firmware lists, populated or not
	// (0 = unknown), so an empty memory channel can be spotted.
	Slots int `json:"slots,omitempty"`
}

type DIMM struct {
	Locator      string `json:"locator"`
	Bank         string `json:"bank,omitempty"`
	SizeMB       int    `json:"size_mb"`
	Type         string `json:"type,omitempty"`  // DDR4, DDR5
	Speed        string `json:"speed,omitempty"` // rated, e.g. "4800 MT/s"
	Configured   string `json:"configured_speed,omitempty"`
	Rank         int    `json:"rank,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Part         string `json:"part,omitempty"`
}

// MTs parses a dmidecode speed such as "4800 MT/s" (0 when unknown).
func MTs(speed string) int {
	f := strings.Fields(speed)
	if len(f) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(f[0])
	return n
}

type NIC struct {
	Name      string `json:"name"`
	NUMANode  int    `json:"numa_node"`
	SpeedMbps int    `json:"speed_mbps"`
	RxQueues  int    `json:"rx_queues"`
	Driver    string `json:"driver,omitempty"`
}

// PCIDevice is a storage or network controller as sysfs describes it.
type PCIDevice struct {
	Address  string `json:"address"`
	Class    string `json:"class"` // e.g. 0x010802 (NVMe)
	Kind     string `json:"kind"`  // storage, network
	VendorID string `json:"vendor_id"`
	DeviceID string `json:"device_id"`
	Driver   string `json:"driver,omitempty"`
	NUMANode int    `json:"numa_node"`
	// Link speed and width now and at most; a device running below its
	// maximum sits in the wrong slot or riser, or has a bad link.
	LinkSpeed    string `json:"link_speed,omitempty"` // "16.0 GT/s PCIe"
	LinkWidth    int    `json:"link_width,omitempty"`
	MaxLinkSpeed string `json:"max_link_speed,omitempty"`
	MaxLinkWidth int    `json:"max_link_width,omitempty"`
}

// GTs parses a sysfs link speed such as "16.0 GT/s PCIe" (0 when unknown).
func GTs(speed string) float64 {
	f := strings.Fields(speed)
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

// Degraded reports a link trained below what the device and slot support.
func (d PCIDevice) Degraded() bool {
	cur, top := GTs(d.LinkSpeed), GTs(d.MaxLinkSpeed)
	return (cur > 0 && top > 0 && cur < top) || (d.LinkWidth > 0 && d.MaxLinkWidth > 0 && d.LinkWidth < d.MaxLinkWidth)
}

type Disk struct {
	Name       string  `json:"name"`
	Model      string  `json:"model,omitempty"`
	SizeGB     float64 `json:"size_gb"`
	Rotational bool    `json:"rotational"`
}
