// Package benchmark defines what a workload is and how its measurements are
// summarised. Workload kernels live in internal/workload; the runner that
// times them lives in internal/app.
package benchmark

import (
	"strconv"

	"github.com/chaloemph/cpubench/internal/domain/hardware"
)

type Category string

const (
	Integer     Category = "integer"
	Float       Category = "float"
	Crypto      Category = "crypto"
	Memory      Category = "memory"
	Concurrency Category = "concurrency"
)

// Kind says how work and time turn into the reported value.
type Kind string

const (
	// Throughput: value = total work / wall time (higher is better).
	Throughput Kind = "throughput"
	// Latency: value = per-thread time / per-thread work, averaged (lower is better).
	Latency Kind = "latency"
)

type Info struct {
	Name        string   `json:"name"`
	Category    Category `json:"category"`
	Kind        Kind     `json:"kind"`
	Unit        string   `json:"unit"`
	Description string   `json:"description"`
	// UnitScale converts raw work/second into Unit (e.g. 1e-6 for bytes → MB).
	UnitScale float64 `json:"-"`
	// SingleThread workloads ignore the thread list and always run on one thread.
	SingleThread bool `json:"single_thread,omitempty"`
	// FootprintMB is the memory the workload allocates at scale 1: per thread
	// when PerThread, otherwise in total. FixedSize footprints ignore scale.
	FootprintMB float64 `json:"-"`
	PerThread   bool    `json:"-"`
	FixedSize   bool    `json:"-"`
}

// MemoryMB estimates the allocation for a run, so the runner can skip runs
// that would exceed a container's memory limit instead of being OOM-killed.
func (i Info) MemoryMB(threads int, scale float64) float64 {
	m := i.FootprintMB
	if !i.FixedSize {
		m *= scale
	}
	if i.PerThread {
		m *= float64(threads)
	}
	return m
}

// HigherIsBetter is true for throughput workloads.
func (i Info) HigherIsBetter() bool { return i.Kind != Latency }

// Workload is a deterministic kernel. The runner calls Setup once per thread
// count, then RunThread concurrently on every thread for each iteration. Each
// call must do a fixed amount of work so results are comparable across
// machines, and must return the same checksum every time for the same id.
type Workload interface {
	Info() Info
	Setup(threads int, scale float64) error
	RunThread(id int) (work float64, checksum uint64)
	Teardown()
}

// Result is one workload at one thread count.
type Result struct {
	Name     string    `json:"name"`
	Category Category  `json:"category"`
	Kind     Kind      `json:"kind"`
	Unit     string    `json:"unit"`
	Threads  int       `json:"threads"`
	CPUs     []int     `json:"cpus,omitempty"`
	Samples  []float64 `json:"samples"`
	Stats    Stats     `json:"stats"`
	Checksum string    `json:"checksum"`
	// ChecksumStable is false when iterations disagreed: a correctness problem.
	ChecksumStable bool `json:"checksum_stable"`
	// Backend is the implementation the kernel ran on this CPU, for kernels
	// whose code path depends on CPU features (crypto).
	Backend *Backend `json:"backend,omitempty"`
}

// BackendKind says what kind of code a kernel ran: crypto kernels use
// dedicated instructions on one CPU and fall back to plain code on another,
// which changes what a comparison measures.
type BackendKind string

const (
	Hardware BackendKind = "hardware" // dedicated instructions (AES-NI, SHA-NI, ARMv8 crypto)
	SIMD     BackendKind = "simd"     // vector code without dedicated instructions
	Assembly BackendKind = "asm"      // hand-written scalar assembly
	Software BackendKind = "software" // generic Go
)

type Backend struct {
	Kind   BackendKind `json:"kind"`
	Detail string      `json:"detail"`
}

func (b Backend) String() string { return string(b.Kind) + " (" + b.Detail + ")" }

// BackendReporter is implemented by workloads whose code path depends on CPU
// features. Backend returns nil when the features are unknown.
type BackendReporter interface {
	Backend(cpu hardware.CPUInfo) *Backend
}

// Key identifies a result across files: same workload at same thread count.
func (r Result) Key() string { return KeyOf(r.Name, r.Threads) }

func KeyOf(name string, threads int) string {
	return name + "@" + strconv.Itoa(threads)
}
