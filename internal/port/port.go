// Package port declares the interfaces the application core needs from the
// outside world. Adapters in internal/adapter/out implement them; the
// composition root in cmd/cpubench wires them together.
package port

import (
	"context"
	"io"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/compare"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/load"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/domain/scan"
)

// SystemProbe inspects the machine and execution context.
type SystemProbe interface {
	System(ctx context.Context) (hardware.System, error)
}

// Sensors are cheap readings sampled repeatedly during long runs.
type Sensors interface {
	// AvgMHz is the average current frequency over the given CPUs (all when nil).
	AvgMHz(cpus []int) (float64, bool)
	MaxTempC() (float64, bool)
	// CgroupThrottle returns cumulative cgroup throttle counters.
	CgroupThrottle() (nr int64, usec int64, ok bool)
	// KernelErrors returns hardware error lines (MCE/RAS/EDAC) logged since boot, if readable.
	KernelErrors() []string
}

// Pinner binds the calling OS thread to a CPU. Callers must hold
// runtime.LockOSThread.
type Pinner interface {
	PinCurrentThread(cpu int) error
}

// PowerMeter reads whole-system (or package) power.
type PowerMeter interface {
	Source() string
	Watts(ctx context.Context) (float64, error)
}

// ResultStore persists and loads result files.
type ResultStore interface {
	Save(ctx context.Context, f *result.File) (string, error)
	Load(ctx context.Context, ref string) (*result.File, error)
	List(ctx context.Context) ([]result.File, error)
}

// ReportRenderer writes a human report for one or more result files.
type ReportRenderer interface {
	Render(w io.Writer, files []*result.File, opts ReportOptions) error
}

type ReportOptions struct {
	Baseline string
	// BaselineGuessed lists the machines the baseline was picked from
	// automatically; set when nobody named it and there were several.
	BaselineGuessed []string
	// Shared marks a copy prepared for one vendor: other machines were left
	// out, or kept under the aliases in Anonymized.
	Shared     bool
	Anonymized []string
	TCO        *compare.TCOInput
	Title      string
	// Lang is the language the report is written in: "th" or "en". Empty means Thai.
	Lang string
}

// Logger receives progress lines for the terminal.
type Logger interface {
	Printf(format string, args ...any)
}

// LoadTarget sends one HTTP request and reports the status code.
type LoadTarget interface {
	Do(ctx context.Context, r load.Request) (status int, err error)
}

// SUTStats reads CPU usage and the system snapshot from a `cpubench web serve`
// target, so load tests can report server CPU and identify the machine.
type SUTStats interface {
	CPUSeconds(ctx context.Context) (cpuSec float64, numCPU int, err error)
	System(ctx context.Context) (hardware.System, error)
	// Watts is the server's own power reading (`web serve --power`), or an error.
	Watts(ctx context.Context) (float64, error)
}

// LoadAgent runs a share of a load test on a remote load generator.
type LoadAgent interface {
	Name() string
	Run(ctx context.Context, job LoadJob) (*LoadOutcome, error)
}

// LoadJob is what a coordinator asks an agent to do.
type LoadJob struct {
	Scenario  load.Scenario `json:"scenario"`
	Phases    []load.Phase  `json:"phases"`
	Share     float64       `json:"share"` // fraction of each phase's rate
	StartAt   time.Time     `json:"start_at"`
	WindowSec float64       `json:"window_sec"`
	Seed      uint64        `json:"seed"`
	Bodies    [][]byte      `json:"bodies"`
}

// LoadOutcome is the raw, mergeable result of a job.
type LoadOutcome struct {
	Phases  []*load.Agg `json:"phases"`
	Windows []*load.Agg `json:"windows"`
}

// ProcessRunner starts an application under test (for startup/warmup timing).
type ProcessRunner interface {
	// Start runs a shell command; stop terminates it and its children.
	Start(ctx context.Context, command string) (stop func() error, err error)
}

// ImageInspector lists the platforms a container image is published for.
type ImageInspector interface {
	Platforms(ctx context.Context, ref string) ([]string, error)
}

// BinaryScanner finds native code under a directory and reports its architecture.
type BinaryScanner interface {
	Scan(ctx context.Context, root string) ([]scan.Binary, error)
}
