package linux

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/doctor"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/workload"
)

const fixtures = "../../../../../testdata/fixtures/"

func TestARM64SyntheticFixture(t *testing.T) {
	s, err := Fixture(fixtures + "arm64-2s-synthetic").System(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c := s.CPU
	if c.Arch != "arm64" || c.Vendor != "ARM" || c.MIDR.Part != "0xd0c" {
		t.Fatalf("cpu %+v", c)
	}
	if c.Sockets != 2 || c.CoresPerSocket != 4 || c.ThreadsPerCore != 2 || c.LogicalCPUs != 16 {
		t.Fatalf("topology %d/%d/%d/%d", c.Sockets, c.CoresPerSocket, c.ThreadsPerCore, c.LogicalCPUs)
	}
	if c.MaxMHz != 2200 || !c.HasFeature("atomics") {
		t.Fatalf("mhz %v features %v", c.MaxMHz, c.Features)
	}
	if len(s.Topology.NUMANodes) != 4 || !slices.Equal(s.Topology.NUMANodes[2].CPUs, []int{8, 9, 10, 11}) {
		t.Fatalf("numa %+v", s.Topology.NUMANodes)
	}
	if s.Topology.NUMANodes[0].Distances[3] != 22 {
		t.Fatalf("distance %v", s.Topology.NUMANodes[0].Distances)
	}
	if order := hardware.PlacementOrder(s.Topology); !slices.Equal(order[:4], []int{0, 2, 4, 6}) {
		t.Fatalf("placement %v", order)
	}
	if len(s.Memory.DIMMs) != 16 || s.Memory.DIMMs[0].SizeMB != 32768 || s.Memory.Slots != 17 {
		t.Fatalf("dimms %d slots %d", len(s.Memory.DIMMs), s.Memory.Slots)
	}
	if d := s.Memory.DIMMs[0]; d.Type != "DDR4" || d.Configured != "2933 MT/s" || d.Rank != 2 || d.Manufacturer != "SYNTH" {
		t.Fatalf("dimm %+v", d)
	}
	if s.Platform.BIOSVendor != "Example Corp." || s.Platform.BIOSDate != "06/14/2025" {
		t.Fatalf("bios %+v", s.Platform)
	}
	// Storage and network controllers only: the bridge is left out.
	if len(s.PCI) != 3 {
		t.Fatalf("pci %+v", s.PCI)
	}
	for _, d := range s.PCI {
		if d.Address == "0000:85:00.0" && (d.Kind != "storage" || d.Driver != "nvme" || d.NUMANode != 2 || d.LinkWidth != 4 || d.Degraded()) {
			t.Fatalf("nvme %+v", d)
		}
		if d.Address == "0000:7d:00.0" && (d.Kind != "network" || d.Driver != "mlx5_core") {
			t.Fatalf("nic %+v", d)
		}
	}
	if len(s.Disks) != 2 || s.Disks[0].Name != "nvme0n1" || s.Disks[0].Rotational || !s.Disks[1].Rotational || int(s.Disks[0].SizeGB) != 3840 {
		t.Fatalf("disks %+v", s.Disks)
	}
	if len(s.NICs) != 1 || s.NICs[0].RxQueues != 4 || s.NICs[0].NUMANode != 0 {
		t.Fatalf("nics %+v", s.NICs)
	}
	if s.Platform.Virt != "none" || s.Context.Mode != hardware.ModeNative || s.OS.Version != "24.04" {
		t.Fatalf("platform %+v ctx %+v os %+v", s.Platform, s.Context, s.OS)
	}
	if s.Context.EffectiveCPUs != 16 {
		t.Fatalf("effective %d", s.Context.EffectiveCPUs)
	}
	got := map[string]bool{}
	for _, f := range doctor.Check(s) {
		got[f.ID] = true
	}
	for _, id := range []string{"governor", "numa", "smt", "arm-lse"} {
		if !got[id] {
			t.Errorf("doctor missing %s", id)
		}
	}
}

func TestX86WSLFixture(t *testing.T) {
	s, err := Fixture(fixtures + "x86-wsl-i7-1360p").System(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.CPU.Arch != "amd64" || s.CPU.LogicalCPUs != 6 || s.CPU.ThreadsPerCore != 2 {
		t.Fatalf("%+v", s.CPU)
	}
	if !s.CPU.HasFeature("avx2") || s.Platform.Virt != "wsl" || s.Context.Mode != hardware.ModeVM {
		t.Fatalf("features %v virt %q mode %q", s.CPU.Features, s.Platform.Virt, s.Context.Mode)
	}
	if s.Context.Cgroup == nil || s.Context.Cgroup.CPUQuotaCores != 0 {
		t.Fatalf("cgroup %+v", s.Context.Cgroup)
	}
}

// The probe must keep every flag the crypto backend detection needs, or a
// CPU with AES-NI/SHA-NI is reported as running fallback code.
func TestFixturesKeepCryptoFeatures(t *testing.T) {
	for fixture, want := range map[string]benchmark.BackendKind{
		"x86-wsl-i7-1360p":   benchmark.Hardware, // i7-1360P has AES-NI, PCLMULQDQ and SHA-NI
		"arm64-2s-synthetic": benchmark.Hardware, // Neoverse-N1: ARMv8 AES, PMULL, SHA2
	} {
		s, err := Fixture(fixtures + fixture).System(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"crypto/aes-gcm", "crypto/sha256"} {
			b := workload.Find(name).(benchmark.BackendReporter).Backend(s.CPU)
			if b == nil || b.Kind != want {
				t.Errorf("%s %s: got %v want %s (features %v)", fixture, name, b, want, s.CPU.Features)
			}
		}
	}
}

func TestParseCPUList(t *testing.T) {
	if got := ParseCPUList("0-2,5,7-8\n"); !slices.Equal(got, []int{0, 1, 2, 5, 7, 8}) {
		t.Fatal(got)
	}
}

func TestLiveProbe(t *testing.T) {
	s, err := New().System(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.CPU.LogicalCPUs == 0 || len(s.Topology.AllowedCPUs) == 0 {
		t.Fatalf("%+v", s.CPU)
	}
}

func TestCacheWithoutSizeIsDropped(t *testing.T) {
	root := t.TempDir()
	w := func(rel, v string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(v), 0o644)
	}
	w("proc/cpuinfo", "processor\t: 0\nvendor_id\t: GenuineIntel\nmodel name\t: Test\nflags\t: avx2\n")
	w("sys/devices/system/cpu/online", "0\n")
	w("sys/devices/system/cpu/cpu0/cache/index0/level", "1\n") // a VM exposing a cache with no size
	s, err := Fixture(root).System(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.CPU.Caches) != 0 {
		t.Fatalf("caches %+v", s.CPU.Caches)
	}
}
