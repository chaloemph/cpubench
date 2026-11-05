package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chaloemph/cpubench/internal/app"
	"github.com/chaloemph/cpubench/internal/domain/scan"
	"github.com/chaloemph/cpubench/internal/port"
)

// ReadinessDeps are the adapters for density/startup/scan.
type ReadinessDeps struct {
	Process port.ProcessRunner
	Images  port.ImageInspector
	Files   port.BinaryScanner
}

func init() {
	register("density", "how many VM/pod-sized instances fit before each slows below 90%", cmdDensity)
	register("startup", "time an app's startup and JIT warmup", cmdStartup)
	register("scan", "check whether images and native code have builds for amd64 or arm64", cmdScan)
}

func cmdDensity(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "density", "[--workload int/json] [--group 8]")
	var o outputFlags
	o.register(fs, false)
	wl := fs.String("workload", "int/json", "workload each instance runs")
	group := fs.Int("group", 8, "threads per instance (the vCPU count of one VM or pod)")
	iters := fs.Int("iterations", 3, "measured iterations per step")
	scale := fs.Float64("scale", 0.5, "work per iteration")
	pin := fs.Bool("pin", true, "pin threads")
	if err := fs.Parse(args); err != nil {
		return err
	}
	f, err := d.App.Density(ctx, app.DensityOptions{Label: o.label, Machine: o.machine, Workload: *wl, GroupSize: *group,
		Iterations: *iters, Scale: *scale, Pin: *pin})
	if err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "max instances of %d threads with ≥90%% of solo speed: %d\n", *group, f.Density.MaxInstances)
	return o.finish(ctx, d, f)
}

func cmdStartup(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "startup", `--cmd "node server.js" --ready http://127.0.0.1:3000/health`)
	var o outputFlags
	o.register(fs, false)
	command := fs.String("cmd", "", "shell command that starts the app")
	ready := fs.String("ready", "", "URL that returns 200 once the app is ready")
	warmURL := fs.String("warm-url", "", "URL hit sequentially after ready to measure warmup (default: --ready)")
	runs := fs.Int("runs", 5, "number of cold starts")
	warm := fs.Duration("warmup", 30*time.Second, "how long to measure warmup on the last run (0 = skip)")
	timeout := fs.Duration("timeout", 2*time.Minute, "max time to wait for ready")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *command == "" || *ready == "" {
		fs.Usage()
		return fmt.Errorf("--cmd and --ready are required")
	}
	f, err := d.App.Startup(ctx, app.StartupOptions{Label: o.label, Machine: o.machine, Command: *command, ReadyURL: *ready,
		WarmURL: *warmURL, Runs: *runs, Warmup: *warm, Timeout: *timeout, Process: d.Ready.Process, HTTP: d.Web.NewTarget(1, true)})
	if err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "ready median %.0f ms · warm after %.0f s\n", f.Startup.ReadyMedianMs, f.Startup.WarmupSec)
	return o.finish(ctx, d, f)
}

func cmdScan(ctx context.Context, d *Deps, args []string) error {
	fs := newFlags(d, "scan", "--arch amd64|arm64 [--image ref ...] [--dir path ...]")
	var images, dirs multiFlag
	arch := fs.String("arch", "", "target architecture to check against: amd64 or arm64")
	fs.Var(&images, "image", "container image to check (repeatable), e.g. registry.local/shop/api:1.4")
	fs.Var(&dirs, "dir", "directory to scan for native code (repeatable): app checkout, node_modules, venv")
	imageList := fs.String("images-from", "", "file with one image ref per line (e.g. from: kubectl get pods -A -o jsonpath=...)")
	asJSON := fs.String("json", "", "write the full report as JSON")
	all := fs.Bool("all", false, "list every binary, not only blocked/unknown ones")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *imageList != "" {
		b, err := os.ReadFile(*imageList)
		if err != nil {
			return err
		}
		for l := range strings.SplitSeq(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				images = append(images, l)
			}
		}
	}
	if len(images) == 0 && len(dirs) == 0 {
		fs.Usage()
		return fmt.Errorf("give at least one --image or --dir")
	}
	if *arch == "" {
		fs.Usage()
		return fmt.Errorf("give the target architecture with --arch amd64 or --arch arm64")
	}
	rep, err := d.App.Scan(ctx, d.Ready.Images, d.Ready.Files, *arch, images, dirs)
	if err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "target architecture: %s\n\n", rep.Arch)
	if len(rep.Images) > 0 {
		fmt.Fprintf(d.Stdout, "| image | status | platforms | note |\n|---|---|---|---|\n")
		for _, i := range rep.Images {
			fmt.Fprintf(d.Stdout, "| %s | %s | %s | %s |\n", i.Ref, i.Status, strings.Join(i.Platforms, ", "), i.Note)
		}
		fmt.Fprintln(d.Stdout)
	}
	if len(rep.Binaries) > 0 {
		sum := scan.Summary(rep.Binaries, func(b scan.Binary) scan.Status { return b.Status })
		fmt.Fprintf(d.Stdout, "native artifacts for %s: %d ready, %d blocked, %d to check\n\n", rep.Arch, sum[scan.Ready], sum[scan.Blocked], sum[scan.Check])
		fmt.Fprintf(d.Stdout, "| path | kind | arch | status |\n|---|---|---|---|\n")
		for _, b := range rep.Binaries {
			if *all || b.Status != scan.Ready {
				fmt.Fprintf(d.Stdout, "| %s | %s | %s | %s %s |\n", b.Path, b.Kind, b.Arch, b.Status, b.Note)
			}
		}
	}
	if *asJSON != "" {
		b, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*asJSON, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }
