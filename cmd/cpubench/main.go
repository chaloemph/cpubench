// Command cpubench is the composition root: it builds the adapters and hands
// them to the CLI.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/chaloemph/cpubench/internal/adapter/in/cli"
	affinity "github.com/chaloemph/cpubench/internal/adapter/out/affinity/linux"
	"github.com/chaloemph/cpubench/internal/adapter/out/power"
	probe "github.com/chaloemph/cpubench/internal/adapter/out/probe/linux"
	"github.com/chaloemph/cpubench/internal/adapter/out/report"
	"github.com/chaloemph/cpubench/internal/adapter/out/store/jsonfile"
	"github.com/chaloemph/cpubench/internal/app"
	"github.com/chaloemph/cpubench/internal/domain/build"
)

// limitHeapToCgroup sets Go's soft memory limit below the container's
// cgroup limit (unless GOMEMLIMIT is set), so the GC works harder instead of
// the pod being OOM-killed. Go adapts GOMAXPROCS to cgroups but not memory.
func limitHeapToCgroup(ctx context.Context, pr *probe.Probe) {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	if sys, err := pr.System(ctx); err == nil && sys.Context.Cgroup != nil && sys.Context.Cgroup.MemMaxMB > 0 {
		debug.SetMemoryLimit(int64(float64(sys.Context.Cgroup.MemMaxMB)*0.85) << 20)
	}
}

// version is set with -ldflags "-X main.version=..."
var version = "dev"

func currentBuild() build.Info {
	bi, _ := debug.ReadBuildInfo()
	return build.FromBuildInfo(bi, version)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pr := probe.New()
	limitHeapToCgroup(ctx, pr)
	resultsDir := os.Getenv("CPUBENCH_RESULTS")
	if resultsDir == "" {
		resultsDir = "results"
	}
	d := &cli.Deps{
		App: &app.App{
			Probe:   pr,
			Sensors: pr,
			Pinner:  affinity.Pinner{},
			Log:     cli.Logger(os.Stderr),
			Version: version,
			Build:   currentBuild(),
		},
		Store:     jsonfile.Store{Dir: resultsDir},
		HTML:      report.HTML{},
		Markdown:  report.Markdown{},
		OpenPower: power.Open,
		WriteFile: jsonfile.Write,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Version:   version,
	}
	wire(d)
	os.Exit(cli.Run(ctx, d, os.Args[1:]))
}
