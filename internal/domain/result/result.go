// Package result is the on-disk/over-the-wire format of a cpubench run.
package result

import (
	"time"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/build"
	"github.com/chaloemph/cpubench/internal/domain/doctor"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
)

const Schema = "cpubench/v1"

// File is one run of cpubench on one machine (or one load test against one SUT).
type File struct {
	Schema string `json:"schema"`
	ID     string `json:"id"`
	Label  string `json:"label"`
	// Machine groups runs of the same physical server across contexts
	// (native, VM, container, k8s) for the layer-overhead matrix.
	Machine    string    `json:"machine"`
	Tool       string    `json:"tool"`
	Command    string    `json:"command"`
	CreatedAt  time.Time `json:"created_at"`
	FinishedAt time.Time `json:"finished_at"`
	// Build is how this cpubench binary was built; nil in files from older versions.
	Build *build.Info `json:"build,omitempty"`
	// Runs lists the labels of the repeated runs that `compare --repeats`
	// folded into this file. Each benchmark is then the median of the runs'
	// medians, and its Stats spread is between runs, not between iterations.
	Runs []string `json:"runs,omitempty"`

	System   hardware.System  `json:"system"`
	Config   RunConfig        `json:"config"`
	Findings []doctor.Finding `json:"findings,omitempty"`

	Benchmarks []benchmark.Result `json:"benchmarks,omitempty"`
	NUMA       *NUMAMatrix        `json:"numa,omitempty"`
	Stress     *Stress            `json:"stress,omitempty"`
	PowerCurve *PowerCurve        `json:"power_curve,omitempty"`
	Web        *Web               `json:"web,omitempty"`
	Density    *Density           `json:"density,omitempty"`
	Startup    *Startup           `json:"startup,omitempty"`
	Power      *PowerTrace        `json:"power,omitempty"`
	Cost       *Cost              `json:"cost,omitempty"`
}

type RunConfig struct {
	Iterations int      `json:"iterations,omitempty"`
	Warmup     int      `json:"warmup,omitempty"`
	Scale      float64  `json:"scale,omitempty"`
	Threads    []int    `json:"threads,omitempty"`
	Pinned     bool     `json:"pinned"`
	NUMANode   *int     `json:"numa_node,omitempty"`
	Workloads  []string `json:"workloads,omitempty"`
}

// NUMAMatrix[i][j] is pointer-chase latency with threads on node CPUNodes[i]
// reading memory first-touched on node MemNodes[j].
type NUMAMatrix struct {
	Nodes     []int       `json:"nodes"`
	BufferMB  int         `json:"buffer_mb"`
	LatencyNs [][]float64 `json:"latency_ns"`
}

type Stress struct {
	Workloads      []string       `json:"workloads"`
	Threads        int            `json:"threads"`
	DurationSec    float64        `json:"duration_sec"`
	IntervalSec    float64        `json:"interval_sec"`
	Samples        []StressSample `json:"samples"`
	PeakRate       float64        `json:"peak_rate"`
	SustainedRate  float64        `json:"sustained_rate"`
	DegradationPct float64        `json:"degradation_pct"`
	MinMHz         float64        `json:"min_mhz,omitempty"`
	MaxMHz         float64        `json:"max_mhz,omitempty"`
	MaxTempC       float64        `json:"max_temp_c,omitempty"`
	AvgWatts       float64        `json:"avg_watts,omitempty"`
	PowerSource    string         `json:"power_source,omitempty"`
	Errors         int64          `json:"errors"`
	ThrottledUsec  int64          `json:"throttled_usec"`
	KernelErrors   []string       `json:"kernel_errors,omitempty"`
}

type StressSample struct {
	T             float64 `json:"t"`
	Rate          float64 `json:"rate"` // kernel iterations per second, all threads
	MHz           float64 `json:"mhz,omitempty"`
	TempC         float64 `json:"temp_c,omitempty"`
	Watts         float64 `json:"watts,omitempty"`
	ThrottledUsec int64   `json:"throttled_usec,omitempty"`
	Errors        int64   `json:"errors,omitempty"`
}

type PowerCurve struct {
	Source   string       `json:"source"`
	Workload string       `json:"workload"`
	Levels   []PowerLevel `json:"levels"`
}

type PowerLevel struct {
	UtilPct float64 `json:"util_pct"`
	Watts   float64 `json:"watts"`
	Rate    float64 `json:"rate"`
}

type SLO struct {
	P99Ms     float64 `json:"p99_ms" yaml:"p99_ms"`
	ErrorRate float64 `json:"error_rate" yaml:"error_rate"`
}

type Web struct {
	Target      string      `json:"target"`
	Scenario    string      `json:"scenario"`
	Profile     string      `json:"profile"`
	SLO         SLO         `json:"slo"`
	Concurrency int         `json:"concurrency"`
	Agents      int         `json:"agents"`
	KeepAlive   bool        `json:"keepalive"`
	Steps       []WebStep   `json:"steps"`
	KneeRPS     float64     `json:"knee_rps"`
	Timeline    []WebSecond `json:"timeline,omitempty"`
	RecoverySec float64     `json:"recovery_sec,omitempty"`
	PowerSource string      `json:"power_source,omitempty"`
	// LoadGen identifies the machine that drove the test: web numbers from
	// different load generators or networks are not directly comparable.
	LoadGen *LoadGen `json:"load_gen,omitempty"`
}

type LoadGen struct {
	Hostname    string `json:"hostname"`
	Arch        string `json:"arch"`
	CPU         string `json:"cpu"`
	LogicalCPUs int    `json:"logical_cpus"`
	Agents      int    `json:"agents"`
}

type WebStep struct {
	TargetRPS   float64 `json:"target_rps"`
	AchievedRPS float64 `json:"achieved_rps"`
	Requests    int64   `json:"requests"`
	Errors      int64   `json:"errors"`
	Dropped     int64   `json:"dropped"`
	P50Ms       float64 `json:"p50_ms"`
	P90Ms       float64 `json:"p90_ms"`
	P99Ms       float64 `json:"p99_ms"`
	P999Ms      float64 `json:"p999_ms"`
	MaxMs       float64 `json:"max_ms"`
	SUTCPUPct   float64 `json:"sut_cpu_pct,omitempty"` // % of all SUT CPUs
	Watts       float64 `json:"watts,omitempty"`
	PassSLO     bool    `json:"pass_slo"`
}

type WebSecond struct {
	T      float64 `json:"t"`
	RPS    float64 `json:"rps"`
	P99Ms  float64 `json:"p99_ms"`
	Errors int64   `json:"errors"`
}

type Density struct {
	Workload     string       `json:"workload"`
	GroupSize    int          `json:"group_size"`
	Rows         []DensityRow `json:"rows"`
	MaxInstances int          `json:"max_instances_90"` // most instances with ≥90% of solo per-instance rate
}

type DensityRow struct {
	Instances   int     `json:"instances"`
	PerInstance float64 `json:"per_instance"`
	Total       float64 `json:"total"`
	RelPct      float64 `json:"rel_pct"` // per-instance vs 1 instance
}

type Startup struct {
	Command       string         `json:"command"`
	ReadyURL      string         `json:"ready_url"`
	ReadyMs       []float64      `json:"ready_ms"`
	ReadyMedianMs float64        `json:"ready_median_ms"`
	Warmup        []WarmupSecond `json:"warmup,omitempty"`
	WarmupSec     float64        `json:"warmup_sec"`
}

type WarmupSecond struct {
	Sec   int     `json:"sec"`
	P50Ms float64 `json:"p50_ms"`
}

type PowerTrace struct {
	Source  string        `json:"source"`
	Samples []PowerSample `json:"samples"`
	Avg     float64       `json:"avg"`
	Max     float64       `json:"max"`
}

type PowerSample struct {
	At    time.Time `json:"at"`
	Watts float64   `json:"watts"`
}

// Cost describes how much a server costs to own (on-prem TCO).
type Cost struct {
	ServerPrice   float64 `json:"server_price" yaml:"server_price"`
	Currency      string  `json:"currency" yaml:"currency"`
	LifespanYears float64 `json:"lifespan_years" yaml:"lifespan_years"`
	PowerRate     float64 `json:"power_rate_per_kwh" yaml:"power_rate_per_kwh"`
	PUE           float64 `json:"pue" yaml:"pue"`
	// Watts at typical load; when 0 the measured power at the knee is used.
	Watts float64 `json:"watts,omitempty" yaml:"watts"`
}
