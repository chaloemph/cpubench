package doctor

import (
	"slices"
	"strings"
	"testing"

	"github.com/chaloemph/cpubench/internal/domain/hardware"
)

func ids(fs []Finding) map[string]Severity {
	m := map[string]Severity{}
	for _, f := range fs {
		m[f.ID] = f.Severity
	}
	return m
}

func TestVMWithoutAVX2(t *testing.T) {
	s := hardware.System{
		CPU:     hardware.CPUInfo{Arch: "amd64", Features: []string{"aes", "sse4_2"}},
		Context: hardware.ExecContext{InVM: true, Mode: hardware.ModeVM},
	}
	if ids(Check(s))["vm-cpu-type"] != Warn {
		t.Fatal("expected vm-cpu-type warning")
	}
	s.CPU.Features = append(s.CPU.Features, "avx2")
	if _, ok := ids(Check(s))["vm-cpu-type"]; ok {
		t.Fatal("unexpected warning with avx2")
	}
}

func TestTwoSocketARM64(t *testing.T) {
	s := hardware.System{
		CPU: hardware.CPUInfo{Arch: "arm64", Sockets: 2, CoresPerSocket: 64, ThreadsPerCore: 2,
			Features: []string{"fp", "asimd", "aes", "sha2", "atomics"}},
		Topology: hardware.Topology{PageSize: 4096, NUMANodes: []hardware.NUMANode{{ID: 0}, {ID: 1}, {ID: 2}, {ID: 3}}},
		OS:       hardware.OS{Governor: "ondemand", IRQBalance: true},
		NICs:     []hardware.NIC{{Name: "enp125s0f0", NUMANode: 0, SpeedMbps: 25000}},
		Memory:   hardware.Memory{DIMMs: make([]hardware.DIMM, 15)},
	}
	got := ids(Check(s))
	want := map[string]Severity{"governor": Warn, "arm-lse": Info, "numa": Info, "nic-numa": Warn, "dimm-balance": Warn, "smt": Info}
	for id, sev := range want {
		if got[id] != sev {
			t.Errorf("%s: got %q want %q", id, got[id], sev)
		}
	}
	if _, ok := got["page-size"]; ok {
		t.Error("4K page should not be flagged")
	}
}

func TestMissingISA(t *testing.T) {
	// ARMv8.2 features (Neoverse-N1, arm64-2s-synthetic fixture): crypto extension but no SHA512 or SVE.
	armv82 := hardware.System{CPU: hardware.CPUInfo{Arch: "arm64",
		Features: []string{"fp", "asimd", "aes", "pmull", "sha1", "sha2", "crc32", "atomics", "asimddp"}}}
	got := ids(Check(armv82))
	if got["isa-sha512"] != Info || got["isa-sve"] != Info {
		t.Errorf("armv82: %v", got)
	}
	for _, id := range []string{"isa-aes", "isa-sha2"} {
		if _, ok := got[id]; ok {
			t.Errorf("armv82 has %s", id)
		}
	}

	noCrypto := hardware.System{CPU: hardware.CPUInfo{Arch: "arm64", Features: []string{"fp", "asimd", "atomics"}}}
	if got := ids(Check(noCrypto)); got["isa-aes"] != Warn || got["isa-sha2"] != Warn {
		t.Errorf("arm64 without crypto: %v", got)
	}

	skylake := hardware.System{CPU: hardware.CPUInfo{Arch: "amd64", Features: []string{"aes", "pclmulqdq", "avx2", "bmi2"}}}
	if got := ids(Check(skylake)); got["isa-sha-ni"] != Info || got["isa-aes"] != "" {
		t.Errorf("x86 without SHA-NI: %v", got)
	}

	if got := ids(Check(hardware.System{CPU: hardware.CPUInfo{Arch: "arm64"}})); got["isa-sve"] != "" {
		t.Error("unknown features must not be reported as missing")
	}
}

func TestComparable(t *testing.T) {
	a := hardware.System{OS: hardware.OS{Name: "Ubuntu", Version: "22.04", Kernel: "5.15.0-1", GoVersion: "go1.26.3"}}
	b := hardware.System{OS: hardware.OS{Name: "Ubuntu", Version: "24.04", Kernel: "6.8.0-1", GoVersion: "go1.26.3"}}
	got := ids(Comparable(a, b))
	if got["os-mismatch"] != Warn || got["kernel-mismatch"] != Warn {
		t.Fatalf("%v", got)
	}
	if _, ok := got["tool-mismatch"]; ok {
		t.Fatal("same go version flagged")
	}
}

func TestVMwareVMCPUTypeFix(t *testing.T) {
	s := hardware.System{
		CPU:      hardware.CPUInfo{Arch: "amd64", Features: []string{"aes"}},
		Platform: hardware.Platform{Virt: "vmware"},
		Context:  hardware.ExecContext{InVM: true, Mode: hardware.ModeVM},
	}
	for _, f := range Check(s) {
		if f.ID == "vm-cpu-type" && !strings.Contains(f.Fix, "EVC") {
			t.Fatalf("VMware VM told to fix Proxmox: %q", f.Fix)
		}
	}
}

func TestComparableHypervisor(t *testing.T) {
	vm := func(virt string) hardware.System {
		return hardware.System{Platform: hardware.Platform{Virt: virt}, Context: hardware.ExecContext{InVM: true, Mode: hardware.ModeVM}}
	}
	if got := ids(Comparable(vm("vmware"), vm("kvm"))); got["virt-mismatch"] != Warn {
		t.Fatalf("VMware vs KVM: %v", got)
	}
	if got := ids(Comparable(vm("kvm"), vm("kvm"))); got["virt-mismatch"] != "" {
		t.Fatalf("same hypervisor flagged: %v", got)
	}
	native := hardware.System{Platform: hardware.Platform{Virt: "none"}, Context: hardware.ExecContext{Mode: hardware.ModeNative}}
	if got := ids(Comparable(native, vm("kvm"))); got["virt-mismatch"] != "" || got["mode-mismatch"] != Info {
		t.Fatalf("native vs VM is a mode difference: %v", got)
	}
}

func TestDIMMPopulation(t *testing.T) {
	dimm := hardware.DIMM{SizeMB: 32768, Speed: "4800 MT/s", Configured: "4800 MT/s", Part: "M321R4GA3BB6"}
	metal := func(dimms []hardware.DIMM, slots int) hardware.System {
		return hardware.System{CPU: hardware.CPUInfo{Arch: "amd64", Sockets: 2}, Memory: hardware.Memory{DIMMs: dimms, Slots: slots}}
	}
	n := func(k int) []hardware.DIMM { return slices.Repeat([]hardware.DIMM{dimm}, k) }
	// 8 channels × 2 DIMMs per channel × 2 sockets = 32 slots.
	if got := ids(Check(metal(n(16), 32))); got["dimm-channels"] != "" {
		t.Errorf("one DIMM per channel flagged: %v", got)
	}
	if got := ids(Check(metal(n(8), 32))); got["dimm-channels"] != Warn {
		t.Errorf("4 of 16 slots per socket not flagged: %v", got)
	}
	vm := metal(n(2), 32)
	vm.Context.InVM = true
	if got := ids(Check(vm)); got["dimm-channels"] != "" {
		t.Errorf("VM DIMMs checked: %v", got)
	}

	mixed := n(16)
	mixed[3].SizeMB = 65536
	if got := ids(Check(metal(mixed, 32))); got["dimm-mixed"] != Warn {
		t.Errorf("mixed sizes: %v", got)
	}
	slow := n(16)
	for i := range slow {
		slow[i].Speed, slow[i].Configured = "5600 MT/s", "4400 MT/s"
	}
	if got := ids(Check(metal(slow, 32))); got["dimm-speed"] != Warn || got["dimm-mixed"] != "" {
		t.Errorf("downclocked DIMMs: %v", got)
	}
}

func TestPCIeLink(t *testing.T) {
	nvme := hardware.PCIDevice{Address: "0000:81:00.0", Kind: "storage", VendorID: "0x144d", DeviceID: "0xa80a", Driver: "nvme",
		LinkSpeed: "16.0 GT/s PCIe", LinkWidth: 4, MaxLinkSpeed: "16.0 GT/s PCIe", MaxLinkWidth: 4}
	s := hardware.System{PCI: []hardware.PCIDevice{nvme}}
	if got := ids(Check(s)); got["pcie-link"] != "" {
		t.Fatalf("full link flagged: %v", got)
	}
	s.PCI[0].LinkWidth = 2
	if got := ids(Check(s)); got["pcie-link"] != Warn {
		t.Fatalf("x2 of x4 not flagged: %v", got)
	}
	s.Context.InVM = true
	if got := ids(Check(s)); got["pcie-link"] != "" {
		t.Fatalf("virtual link checked: %v", got)
	}
}

func TestDIMMUnknownOnBareMetal(t *testing.T) {
	s := hardware.System{CPU: hardware.CPUInfo{Arch: "amd64", Sockets: 2}, Memory: hardware.Memory{TotalMB: 262144}}
	if got := ids(Check(s)); got["dimm-unknown"] != Warn {
		t.Fatalf("bare metal without DIMM details: %v", got)
	}
	s.Memory.DIMMs = []hardware.DIMM{{SizeMB: 32768}}
	if got := ids(Check(s)); got["dimm-unknown"] != "" {
		t.Fatalf("flagged with DIMMs: %v", got)
	}
	vm := hardware.System{Memory: hardware.Memory{TotalMB: 16384}, Context: hardware.ExecContext{InVM: true}}
	if got := ids(Check(vm)); got["dimm-unknown"] != "" {
		t.Fatalf("a VM has no real DIMMs to read: %v", got)
	}
}
