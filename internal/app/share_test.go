package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/doctor"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
)

func bidder(label, machine, vendor, model string) *result.File {
	f := &result.File{ID: label + "-20261103T101500-abc123", Label: label, Machine: machine, Command: "auto standard"}
	f.System = hardware.System{Hostname: machine + "-host",
		CPU:      hardware.CPUInfo{Arch: "amd64", Vendor: "GenuineIntel", Model: model, Sockets: 2, CoresPerSocket: 32, ThreadsPerCore: 2, Features: []string{"avx512f"}},
		Platform: hardware.Platform{Vendor: vendor, Product: vendor + " S9000 G1", BIOS: vendor + "-BIOS-5.32", Virt: "none"},
		Memory:   hardware.Memory{Slots: 32, DIMMs: []hardware.DIMM{{Locator: vendor + "_CPU0_A0", SizeMB: 65536, Part: "M321R8GA0BB0", Manufacturer: "Samsung"}}},
		NICs:     []hardware.NIC{{Name: "ens1f0", SpeedMbps: 25000, Driver: "ice"}},
		PCI:      []hardware.PCIDevice{{Kind: "storage", VendorID: "0x1bd4", DeviceID: "0x0081", Driver: "megaraid_sas"}},
		Disks:    []hardware.Disk{{Name: "sda", Model: vendor + " RAID5 LUN", SizeGB: 1920}},
	}
	f.Findings = []doctor.Finding{{ID: "pcie-link", Severity: doctor.Warn,
		Message: "storage controller (0x1bd4:0x0081, megaraid_sas) on " + machine + "-host (" + model + ") links at x4"}}
	f.Benchmarks = []benchmark.Result{{Name: "int/json", Kind: benchmark.Throughput, Threads: 128, Checksum: "a1b2", Stats: benchmark.Stats{Median: 900}}}
	f.Stress = &result.Stress{AvgWatts: 650, PowerSource: "redfish://admin@" + machine + "-bmc",
		KernelErrors: []string{"mce: CPU 3: Machine Check on " + machine + "-host", "DMI: " + vendor + " S9000 G1/BIOS 5.32"}}
	f.Web = &result.Web{Target: "http://" + machine + "-host:8080", KneeRPS: 50000, LoadGen: &result.LoadGen{Hostname: "loadgen01", CPU: "Xeon"}}
	f.Cost = &result.Cost{ServerPrice: 1_234_567, Currency: "THB"}
	return f
}

func TestShareCopyDropsOtherBidders(t *testing.T) {
	files := []*result.File{
		bidder("current-vm", "current", "VendorX", "Xeon Gold 6000A"),
		bidder("vendora", "vendora01", "VendorA", "Xeon Gold 6000B"),
		bidder("vendord", "vendord01", "VendorD", "Xeon Platinum 8000C"),
	}
	out, aliases, err := ShareCopy(files, "current-vm", []string{"vendora"}, OthersDrop)
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, f := range out {
		labels = append(labels, f.Label)
	}
	if !slices.Equal(labels, []string{"current-vm", "vendora"}) || aliases != nil {
		t.Fatalf("labels %v aliases %v", labels, aliases)
	}
	if out[0].Stress.PowerSource != "redfish" || files[0].Stress.PowerSource != "redfish://admin@current-bmc" {
		t.Fatalf("BMC address: copy %q original %q", out[0].Stress.PowerSource, files[0].Stress.PowerSource)
	}
	if _, _, err := ShareCopy(files, "current-vm", []string{"vendorx"}, OthersDrop); err == nil {
		t.Fatal("unknown vendor accepted")
	}
}

func TestShareCopyAnonymizesWithoutLeaks(t *testing.T) {
	vendord := bidder("vendord-native", "vendord01", "VendorD", "Xeon Platinum 8000C")
	vendordVM := bidder("vendord-vm", "vendord01", "VendorD", "Xeon Platinum 8000C")
	files := []*result.File{
		bidder("current-vm", "current", "VendorX", "Xeon Gold 6000A"),
		bidder("vendora", "vendora01", "VendorA", "Xeon Gold 6000B"),
		vendord, vendordVM,
		bidder("vendore", "arm-01", "VendorE", "Neoverse-N1"),
	}
	out, aliases, err := ShareCopy(files, "current-vm", []string{"vendora01"}, OthersAnonymize)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(aliases, []string{"candidate-a-1", "candidate-a-2", "candidate-b"}) {
		t.Fatalf("aliases %v", aliases)
	}
	if out[2].Machine != "candidate-a" || out[3].Machine != "candidate-a" {
		t.Fatalf("runs of one machine must share an alias: %q %q", out[2].Machine, out[3].Machine)
	}
	b, _ := json.Marshal(out[2:])
	text := string(b)
	for _, secret := range []string{"VendorD", "vendord", "VendorE", "vendore", "arm-01", "Neoverse", "Platinum 8000C", "S9000", "BIOS-5.32",
		"M321R8GA0BB0", "Samsung", "0x1bd4", "megaraid", "RAID5 LUN", "-bmc", "admin@", "1234567", "loadgen01", "GenuineIntel", "avx512f", "_CPU0_"} {
		if strings.Contains(text, secret) {
			t.Errorf("anonymized copy still contains %q", secret)
		}
	}
	// The numbers and what they need to be read stay.
	a := out[2]
	if a.Benchmarks[0].Stats.Median != 900 || a.Benchmarks[0].Checksum != "a1b2" || a.System.CPU.PhysicalCores() != 64 ||
		a.Stress.AvgWatts != 650 || a.Stress.PowerSource != "redfish" || a.Web.KneeRPS != 50000 {
		t.Fatalf("data lost: %+v", a)
	}
	if !strings.Contains(a.Findings[0].Message, "candidate-a-1") || len(a.Stress.KernelErrors) != 1 {
		t.Fatalf("free text not scrubbed: %q %q", a.Findings[0].Message, a.Stress.KernelErrors)
	}
	// The originals are untouched.
	if vendord.Label != "vendord-native" || vendord.System.Platform.Vendor != "VendorD" || vendord.Cost == nil {
		t.Fatal("ShareCopy changed its input")
	}
	// Kept machines are not scrubbed.
	if out[1].System.Platform.Vendor != "VendorA" {
		t.Fatal("the vendor's own machine was anonymized")
	}
}
