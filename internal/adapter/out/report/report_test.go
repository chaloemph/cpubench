package report

import (
	"bytes"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/build"
	"github.com/chaloemph/cpubench/internal/domain/compare"
	"github.com/chaloemph/cpubench/internal/domain/doctor"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
)

func bench(name string, cat benchmark.Category, kind benchmark.Kind, threads int, v float64, sum string) benchmark.Result {
	return benchmark.Result{Name: name, Category: cat, Kind: kind, Unit: "u", Threads: threads, Checksum: sum,
		Stats: benchmark.Stats{N: 3, Median: v, CV: 1}, ChecksumStable: true}
}

func full(label, machine, arch string, mode hardware.Mode, k float64) *result.File {
	f := &result.File{Schema: result.Schema, Label: label, Machine: machine, Command: "run", Tool: "cpubench test", CreatedAt: time.Now()}
	f.System.CPU = hardware.CPUInfo{Arch: arch, Model: label + " cpu", Sockets: 2, CoresPerSocket: 4, ThreadsPerCore: 2}
	f.System.Context.Mode = mode
	f.System.OS = hardware.OS{Name: "Ubuntu", Version: "24.04", Kernel: "6.8.0"}
	f.Findings = []doctor.Finding{{ID: "x", Severity: doctor.Warn, Message: "check <this>", Fix: "do it"}}
	f.Benchmarks = []benchmark.Result{
		bench("int/json", benchmark.Integer, benchmark.Throughput, 1, 100*k, "a"),
		bench("int/json", benchmark.Integer, benchmark.Throughput, 8, 700*k, "a"),
		bench("float/matmul", benchmark.Float, benchmark.Throughput, 1, 50*k, arch),
		bench("mem/latency-32K", benchmark.Memory, benchmark.Latency, 1, 2/k, "c"),
		bench("mem/latency-512M", benchmark.Memory, benchmark.Latency, 1, 120/k, "c"),
	}
	sha := bench("crypto/sha256", benchmark.Crypto, benchmark.Throughput, 1, 900*k, "s")
	sha.Backend = &benchmark.Backend{Kind: benchmark.Hardware, Detail: "SHA-NI"}
	if arch == "arm64" {
		sha.Backend = &benchmark.Backend{Kind: benchmark.Software, Detail: "no ARMv8 SHA2"}
	}
	f.Benchmarks = append(f.Benchmarks, sha)
	f.Build = &build.Info{Version: "v1.0.0", Commit: "c2ac45a14753ea8d", GoVersion: "go1.26.3", GOARCH: arch}
	f.NUMA = &result.NUMAMatrix{Nodes: []int{0, 1}, LatencyNs: [][]float64{{90, 140}, {140, 90}}}
	f.Stress = &result.Stress{Threads: 8, DurationSec: 60, PeakRate: 100, SustainedRate: 95, DegradationPct: -5,
		Samples: []result.StressSample{{T: 5, Rate: 100, MHz: 3000}, {T: 10, Rate: 95, MHz: 2800}}}
	f.PowerCurve = &result.PowerCurve{Levels: []result.PowerLevel{{UtilPct: 0, Watts: 100}, {UtilPct: 100, Watts: 300 / k}}}
	f.Web = &result.Web{Target: "http://sut", Profile: "ramp", SLO: result.SLO{P99Ms: 100}, KneeRPS: 10000 * k,
		Steps: []result.WebStep{{TargetRPS: 5000, AchievedRPS: 5000, P99Ms: 10, PassSLO: true, Watts: 200},
			{TargetRPS: 10000 * k, AchievedRPS: 10000 * k, P99Ms: 80, PassSLO: true, Watts: 250}},
		Timeline: []result.WebSecond{{T: 1, P99Ms: 10}, {T: 2, P99Ms: 20}}}
	f.Density = &result.Density{Workload: "int/json", GroupSize: 8, MaxInstances: 3,
		Rows: []result.DensityRow{{Instances: 1, RelPct: 100}, {Instances: 2, RelPct: 95}}}
	f.Startup = &result.Startup{Command: "node server.js", ReadyMedianMs: 800, WarmupSec: 12}
	f.Power = &result.PowerTrace{Source: "redfish", Avg: 250, Max: 300}
	return f
}

func TestRenderEverySection(t *testing.T) {
	files := []*result.File{
		full("x86-01", "x86-01", "amd64", hardware.ModeNative, 1),
		full("arm-01", "arm-01", "arm64", hardware.ModeNative, 1.3),
		full("arm-01-vm", "arm-01", "arm64", hardware.ModeVM, 1.2),
	}
	// float/matmul already differs by arch (benign, FMA); make an integer row
	// differ too, which is the kind that means the work really differed.
	for i, b := range files[1].Benchmarks {
		if b.Name == "int/json" {
			files[1].Benchmarks[i].Checksum = "other"
		}
	}
	opts := port.ReportOptions{Lang: "en", Baseline: "x86-01", TCO: &compare.TCOInput{TargetRPS: 20000, Redundancy: 1,
		Servers: map[string]result.Cost{"x86-01": {ServerPrice: 400000, Currency: "THB", LifespanYears: 5, PowerRate: 4.5, PUE: 1.5},
			"arm-01": {ServerPrice: 500000, Currency: "THB", LifespanYears: 5, PowerRate: 4.5, PUE: 1.5}}}}
	var html bytes.Buffer
	if err := (HTML{}).Render(&html, files, opts); err != nil {
		t.Fatal(err)
	}
	out := html.String()
	for _, want := range []string{
		"Change vs x86-01 by category", "Web load test", "On-prem TCO", "Thread scaling", "Memory hierarchy",
		"NUMA latency — arm-01", "Stress (sustained vs peak)", "Power curve", "Density", "Startup and warmup",
		"Virtualisation / container overhead", "All benchmarks", "checksum differs", "Doctor findings", "check &lt;this&gt;",
		"Conditions", "CPU identity", "+30%", "<svg", "implementation differs", "hardware · SHA-NI", "software · no ARMv8 SHA2",
		"v1.0.0 · c2ac45a14753 · go1.26.3", "crypto/sha256 runs different implementations",
		// the frame around the numbers: conclusion, what limits it, what it cannot say
		"class=\"verdict\"", "against x86-01 over the micro suite", "Read before any number below",
		"did not run the same work", "What this report does not answer",
		"expected where arm64 fuses multiply-add",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if strings.Contains(out, "<this>") {
		t.Error("finding text not escaped")
	}
	var md bytes.Buffer
	if err := (Markdown{}).Render(&md, files, opts); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Change vs x86-01 by category", "TCO for", "All benchmarks",
		"| [integer](#93-integer) | +30% | +20% | checksum mismatch |", "| [memory](#94-memory) | +30% | +20% | – |", "crypto/sha256 ⚠ implementation differs", "(software · no ARMv8 SHA2)",
		// Markdown used to be numbers only: no conditions, and the caveats last.
		"arm-01 is +30% against x86-01", "> [!IMPORTANT]", "## 1. Conditions", "| CPU identity |",
		"Virtualisation / container overhead", "What this report does not answer"} {
		if !strings.Contains(md.String(), want) {
			t.Errorf("Markdown missing %q\n%s", want, md.String())
		}
	}
	// A caveat that voids the ratios must appear before the ratio it voids.
	s := md.String()
	if i, j := strings.Index(s, "did not run the same work"), strings.Index(s, "| [integer]("); i < 0 || i > j {
		t.Errorf("checksum caveat at %d must come before the category table at %d", i, j)
	}
}

// TestCaveatsFromData covers the conditions the report derives on its own,
// each of which had to be diagnosed by hand in an earlier round.
func TestCaveatsFromData(t *testing.T) {
	base := full("x86-host", "x86-host", "amd64", hardware.ModeVM, 1)
	arm := full("arm64-pinned", "arm64-pinned", "arm64", hardware.ModeVM, 1)
	for _, f := range []*result.File{base, arm} {
		f.Findings = nil
		f.Build.ArchLevel = map[string]string{"amd64": "GOAMD64=v1", "arm64": "GOARM64=v8.0"}[f.System.CPU.Arch]
		f.System.Context.EffectiveCPUs = 8
	}
	// Same checksums on both sides, so no mismatch masks the cases under test.
	for i := range arm.Benchmarks {
		arm.Benchmarks[i].Checksum = base.Benchmarks[i].Checksum
	}
	// The server still had CPU left when the step failed: the generator capped it.
	arm.Web.Steps = []result.WebStep{
		{TargetRPS: 80000, AchievedRPS: 80000, P99Ms: 20, SUTCPUPct: 65, PassSLO: true},
		{TargetRPS: 112000, AchievedRPS: 95000, P99Ms: 300, SUTCPUPct: 65, PassSLO: false},
	}
	// One column ran once, so it carries no run-to-run spread.
	for i := range arm.Benchmarks {
		arm.Benchmarks[i].Stats.N = 1
	}
	var md bytes.Buffer
	if err := (Markdown{}).Render(&md, []*result.File{base, arm}, port.ReportOptions{Lang: "en", Baseline: "x86-host"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"load generator's ceiling",              // the server was not the limit
		"lowest baseline of their architecture", // neither binary uses the newer ISA
		"arm64-pinned ran once",                 // no spread
		"Per 8 effective CPUs",                  // what the ratio is per
		"nothing here says what the same CPUs do on bare metal",
	} {
		if !strings.Contains(md.String(), want) {
			t.Errorf("missing caveat %q\n%s", want, md.String())
		}
	}
}

func TestRenderSingleFile(t *testing.T) {
	var b bytes.Buffer
	f := full("solo", "solo", "arm64", hardware.ModeNative, 1)
	if err := (HTML{}).Render(&b, []*result.File{f}, port.ReportOptions{Lang: "en"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "Speedup vs") {
		t.Error("single-file report should not show speedups")
	}
}

// A web-only comparison has no micro suite; the verdict still has to lead.
func TestVerdictWebOnly(t *testing.T) {
	a := full("x86-loop", "x86-loop", "amd64", hardware.ModeVM, 1)
	b := full("arm-loop", "arm-loop", "arm64", hardware.ModeVM, 0.8)
	for _, f := range []*result.File{a, b} {
		f.Benchmarks = nil
	}
	var md bytes.Buffer
	if err := (Markdown{}).Render(&md, []*result.File{a, b}, port.ReportOptions{Lang: "en", Baseline: "x86-loop"}); err != nil {
		t.Fatal(err)
	}
	if want := "arm-loop is -20% against x86-loop on max RPS @ SLO"; !strings.Contains(md.String(), want) {
		t.Errorf("web-only verdict missing %q\n%s", want, md.String())
	}
}

// "pinned" and "unpinned" are words a reader of the labels may not know; the
// report defines them when its labels use them and stays quiet otherwise.
func TestTermsOnlyWhenLabelsUseThem(t *testing.T) {
	render := func(labels ...string) (md, html string) {
		var fs []*result.File
		for i, l := range labels {
			arch := []string{"amd64", "arm64", "arm64"}[i]
			fs = append(fs, full(l, l, arch, hardware.ModeVM, 1))
		}
		var m, h bytes.Buffer
		opts := port.ReportOptions{Lang: "en", Baseline: labels[0]}
		if err := (Markdown{}).Render(&m, fs, opts); err != nil {
			t.Fatal(err)
		}
		if err := (HTML{}).Render(&h, fs, opts); err != nil {
			t.Fatal(err)
		}
		return m.String(), h.String()
	}
	md, html := render("x86-host", "arm64-pinned-1", "arm64-unpinned-1")
	for name, out := range map[string]string{"markdown": md, "html": html} {
		for _, want := range []string{"Terms used in this report", "pinned / unpinned (NUMA pinning)", "unpinned: neither is tied", "Compare pinned with pinned"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s missing %q", name, want)
			}
		}
	}
	// the definition comes after the numbers it explains
	if i, j := strings.Index(md, "## 1. Conditions"), strings.Index(md, ". Terms used in this report\n"); i > j {
		t.Errorf("terms (%d) must come after the conditions (%d)", j, i)
	}
	md, html = render("x86-host", "arm64-host")
	if strings.Contains(md, "Terms used in this report") || strings.Contains(html, "Terms used in this report") {
		t.Error("terms section shown although no label uses the words")
	}
}

// An unpinned column makes the memory and concurrency rows unusable across
// machines, so it is a caveat above the numbers, not just a definition below.
func TestUnpinnedCaveat(t *testing.T) {
	render := func(labels ...string) string {
		var fs []*result.File
		for i, l := range labels {
			fs = append(fs, full(l, l, []string{"amd64", "arm64", "arm64"}[i], hardware.ModeVM, 1))
		}
		var md bytes.Buffer
		if err := (Markdown{}).Render(&md, fs, port.ReportOptions{Lang: "en", Baseline: labels[0]}); err != nil {
			t.Fatal(err)
		}
		return md.String()
	}
	out := render("x86-host", "arm64-unpinned-1")
	for _, want := range []string{"arm64-unpinned-1 ran without NUMA pinning", "do not compare them with another machine"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if i, j := strings.Index(out, "ran without NUMA pinning"), strings.Index(out, "| [memory]("); i < 0 || i > j {
		t.Errorf("caveat at %d must come before the category table at %d", i, j)
	}
	if strings.Contains(out, "Pinned and unpinned columns are both here") {
		t.Error("mixed-columns sentence shown with no pinned column")
	}
	if mixed := render("x86-host", "arm64-pinned-1", "arm64-unpinned-1"); !strings.Contains(mixed, "Pinned and unpinned columns are both here") {
		t.Error("mixed pinned/unpinned report lacks the NUMA-setting sentence")
	}
	if clean := render("x86-host", "arm64-pinned-1"); strings.Contains(clean, "ran without NUMA pinning") {
		t.Error("pinned-only report warned about unpinned")
	}
}

func fullSet() ([]*result.File, port.ReportOptions) {
	files := []*result.File{
		full("x86-01", "x86-01", "amd64", hardware.ModeNative, 1),
		full("arm-01", "arm-01", "arm64", hardware.ModeNative, 1.3),
	}
	return files, port.ReportOptions{Lang: "en", Baseline: "x86-01", TCO: &compare.TCOInput{TargetRPS: 20000, Redundancy: 1,
		Servers: map[string]result.Cost{"x86-01": {ServerPrice: 400000, Currency: "THB", LifespanYears: 5, PowerRate: 4.5, PUE: 1.5},
			"arm-01": {ServerPrice: 500000, Currency: "THB", LifespanYears: 5, PowerRate: 4.5, PUE: 1.5}}}}
}

// The contents list and the category links must point at headings that
// exist, or the links are dead.
func TestMarkdownContentsLinkToRealHeadings(t *testing.T) {
	files, opts := fullSet()
	var b bytes.Buffer
	if err := (Markdown{}).Render(&b, files, opts); err != nil {
		t.Fatal(err)
	}
	md := b.String()
	for _, want := range []string{"**Contents**", "Read first:", "Open when in doubt:"} {
		if !strings.Contains(md, want) {
			t.Errorf("contents missing %q", want)
		}
	}
	anchors := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^###? (.+)$`).FindAllStringSubmatch(md, -1) {
		anchors[mdAnchor(m[1])] = true
	}
	links := regexp.MustCompile(`\]\(#([^)]+)\)`).FindAllStringSubmatch(md, -1)
	if len(links) < 5 {
		t.Fatalf("expected a contents list, found %d links", len(links))
	}
	for _, l := range links {
		if !anchors[l[1]] {
			t.Errorf("contents link #%s has no heading", l[1])
		}
	}
	// the contents come after the caveats and before the first section
	if i, j, k := strings.Index(md, "Read before any number"), strings.Index(md, "**Contents**"), strings.Index(md, "## 1."); !(i < j && j < k) {
		t.Errorf("order wrong: caveat %d, contents %d, first section %d", i, j, k)
	}
}

// The HTML nav is written by hand beside the sections, so check they agree:
// every link has a target and every section is reachable from the nav.
func TestHTMLNavMatchesSections(t *testing.T) {
	files, opts := fullSet()
	var b bytes.Buffer
	if err := (HTML{}).Render(&b, files, opts); err != nil {
		t.Fatal(err)
	}
	page := b.String()
	ids := map[string]bool{}
	for _, m := range regexp.MustCompile(`<h2 id="([^"]+)"`).FindAllStringSubmatch(page, -1) {
		ids[m[1]] = true
	}
	nav := regexp.MustCompile(`<nav class="toc">(?s:.*?)</nav>`).FindString(page)
	if nav == "" {
		t.Fatal("no contents nav")
	}
	linked := map[string]bool{}
	for _, m := range regexp.MustCompile(`href="#([^"]+)"`).FindAllStringSubmatch(nav, -1) {
		linked[m[1]] = true
		if !ids[m[1]] {
			t.Errorf("nav links to #%s, which is not a section", m[1])
		}
	}
	for id := range ids {
		if !linked[id] {
			t.Errorf("section #%s is not in the nav", id)
		}
	}
	if strings.Count(page, "<h2>") != 0 {
		t.Error("an h2 has no id, so the nav cannot reach it")
	}
}

// The long tables are folded; findings stay open only when one warns.
func TestLongSectionsFolded(t *testing.T) {
	files, opts := fullSet()
	render := func(fs []*result.File) (md, html string) {
		var m, h bytes.Buffer
		if err := (Markdown{}).Render(&m, fs, opts); err != nil {
			t.Fatal(err)
		}
		if err := (HTML{}).Render(&h, fs, opts); err != nil {
			t.Fatal(err)
		}
		return m.String(), h.String()
	}
	md, html := render(files)
	for name, out := range map[string]string{"markdown": md, "html": html} {
		if !strings.Contains(out, "Show ") || !strings.Contains(out, "<details") {
			t.Errorf("%s: all-benchmarks table is not folded", name)
		}
	}
	// full() findings are warn: open
	if !strings.Contains(md, "<details open>") || !strings.Contains(html, "<details open>") {
		t.Error("findings that warn must be left open")
	}
	// no file finding, and no comparison finding either: same implementation everywhere
	for _, f := range files {
		f.Findings = nil
		for i := range f.Benchmarks {
			if f.Benchmarks[i].Backend != nil {
				f.Benchmarks[i].Backend = &benchmark.Backend{Kind: benchmark.Hardware, Detail: "SHA"}
			}
		}
	}
	md, html = render(files)
	if strings.Contains(md, "<details open>") || strings.Contains(html, "<details open>") {
		t.Error("nothing warns, yet a section is open")
	}
}

// A category ratio mixes units, so the category table links each category to
// its measured rows and says how far apart those rows are.
func TestCategoryLeadsToMeasuredValues(t *testing.T) {
	files, opts := fullSet()
	// int/json gains more at 8 threads than at 1: the integer rows disagree
	files[1].Benchmarks[1].Stats.Median = 700 * 1.6
	var m, h bytes.Buffer
	if err := (Markdown{}).Render(&m, files, opts); err != nil {
		t.Fatal(err)
	}
	if err := (HTML{}).Render(&h, files, opts); err != nil {
		t.Fatal(err)
	}
	md, page := m.String(), h.String()

	if !regexp.MustCompile(`\| \[integer\]\(#\d+-integer\) \| \+44% \(\+30% … \+60%\) \|`).MatchString(md) {
		t.Errorf("integer row should link to its rows and show the range +30%% … +60%%\n%s", md)
	}
	if !regexp.MustCompile(`\| \[memory\]\(#[^)]+\) \| \+30% \|`).MatchString(md) {
		t.Error("memory rows agree, so no range should be shown")
	}
	if !strings.Contains(md, "| x86-01 (baseline) |") || !strings.Contains(page, "<th>x86-01 (baseline)</th>") {
		t.Error("the baseline column of the benchmark table is not marked")
	}

	// each category's table holds that category's rows and no other
	sec := regexp.MustCompile(`(?s)### \d+\.\d+ integer\n(.*?)(?:###|\z)`).FindStringSubmatch(md)
	if sec == nil {
		t.Fatal("no integer rows under the benchmark section")
	}
	if !strings.Contains(sec[1], "| int/json | 8 |") || regexp.MustCompile(`\| (float|mem|crypto)/`).MatchString(sec[1]) {
		t.Errorf("integer group holds the wrong rows:\n%s", sec[1])
	}

	cats := regexp.MustCompile(`<td><a href="#(bm-[^"]+)">`).FindAllStringSubmatch(page, -1)
	if len(cats) != 4 {
		t.Fatalf("expected 4 linked categories, got %d", len(cats))
	}
	for _, c := range cats {
		if !strings.Contains(page, `<section id="`+c[1]+`">`) {
			t.Errorf("category link #%s has no rows to land on", c[1])
		}
	}
}

// TestProcurementComparison covers a buying round: several x86 machines on
// different hypervisors, compared as whole machines against the one in use.
func TestProcurementComparison(t *testing.T) {
	current := full("current-vm", "current", "amd64", hardware.ModeVM, 1)
	current.System.Context.InVM, current.System.Platform.Virt = true, "vmware"
	vendorc := full("vendorc-vm", "vendorc", "amd64", hardware.ModeVM, 1.1)
	vendorc.System.Context.InVM, vendorc.System.Platform.Virt = true, "kvm"
	vendora := full("vendora", "vendora", "amd64", hardware.ModeNative, 1.5)
	vendora.System.Platform = hardware.Platform{Vendor: "VendorA", BIOSVendor: "AMI", BIOS: "5.32", BIOSDate: "03/01/2026", Virt: "none"}
	vendora.System.Memory = hardware.Memory{Slots: 32, DIMMs: slices.Repeat([]hardware.DIMM{{SizeMB: 65536, Type: "DDR5", Speed: "5600 MT/s", Configured: "4800 MT/s"}}, 16)}
	vendora.System.PCI = []hardware.PCIDevice{
		{Kind: "storage", Driver: "nvme", VendorID: "0x144d", DeviceID: "0xa80a", LinkSpeed: "32.0 GT/s PCIe", LinkWidth: 2, MaxLinkSpeed: "32.0 GT/s PCIe", MaxLinkWidth: 4, NUMANode: 1},
		{Kind: "network", Driver: "ice", VendorID: "0x8086", DeviceID: "0x159b", NUMANode: 0},
	}
	vendora.System.Disks = []hardware.Disk{{Name: "nvme0n1", Model: "PM9A3", SizeGB: 3840}, {Name: "nvme1n1", Model: "PM9A3", SizeGB: 3840}}
	files := []*result.File{current, vendorc, vendora}
	for _, f := range files {
		f.System.Context.EffectiveCPUs = 16 // 2 sockets × 4 cores × 2 threads
	}
	opts := port.ReportOptions{Lang: "en", Baseline: "current-vm", BaselineGuessed: []string{"current", "vendorc", "vendora"},
		TCO: &compare.TCOInput{TargetRPS: 20000, Servers: map[string]result.Cost{
			"current-vm": {ServerPrice: 1_000_000, Currency: "THB"}, "vendora": {ServerPrice: 1_200_000, Currency: "THB"}}}}
	var md bytes.Buffer
	if err := (Markdown{}).Render(&md, files, opts); err != nil {
		t.Fatal(err)
	}
	out := md.String()
	for _, want := range []string{
		"picked automatically as the first of several machines (current, vendorc, vendora)",
		"different contexts (vm/vmware, vm/kvm, native)", // the hypervisor is part of the layer
		"Whole machine vs current-vm", "| Per machine | +10% | +50% |", "| Per core | +10% | +50% |",
		"| Per server price | – | +25% |", "| Server price | 1,000,000 THB | – | 1,200,000 THB |",
		"| Sockets (bare metal only) | – | – | 2 |", "| Watts at full load | 300 W · power-curve | 273 W · power-curve | 200 W · power-curve |",
		"current · vm/vmware",
		"AMI 5.32 (03/01/2026)", "16 of 32 slots · 16 × 64 GB DDR5 4800 MT/s (rated 5600 MT/s)",
		"1 × nvme 0x144d:0xa80a · 32.0 GT/s x2 (max 32.0 GT/s x4) · node 1", "2 × 3.84 TB SSD PM9A3", "1 × ice 0x8086:0x159b · node 0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "Per-machine and per-watt comparisons need runs") {
		t.Error("limit says per-machine is missing while the report has it")
	}
}
