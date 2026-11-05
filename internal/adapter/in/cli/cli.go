// Package cli is the command-line driving adapter. It parses flags, calls the
// use cases in internal/app and hands results to the output ports.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/chaloemph/cpubench/internal/app"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
)

type Deps struct {
	App       *app.App
	Store     port.ResultStore
	HTML      port.ReportRenderer
	Markdown  port.ReportRenderer
	OpenPower func(spec string) (port.PowerMeter, error)
	WriteFile func(path string, f *result.File) error
	// Upload sends a result to a hub; nil disables --upload.
	Upload func(ctx context.Context, hubURL, token string, f *result.File) error
	// FetchHub downloads all results from a hub, for `compare http://hub:8090`.
	FetchHub func(ctx context.Context, hubURL string) ([]*result.File, error)
	Web      WebDeps
	Ready    ReadinessDeps
	Node     NodeDeps
	Stdout   io.Writer
	Stderr   io.Writer
	Version  string
}

type command struct {
	summary string
	run     func(ctx context.Context, d *Deps, args []string) error
}

var commands = map[string]command{}

func register(name, summary string, run func(ctx context.Context, d *Deps, args []string) error) {
	commands[name] = command{summary: summary, run: run}
}

// Run dispatches args[0] and returns a process exit code.
func Run(ctx context.Context, d *Deps, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		usage(d.Stdout)
		return 0
	}
	name := args[0]
	if len(args) > 1 && commands[name+" "+args[1]].run != nil {
		name, args = name+" "+args[1], args[1:]
	}
	c, ok := commands[name]
	if !ok {
		fmt.Fprintf(d.Stderr, "unknown command %q\n\n", args[0])
		usage(d.Stderr)
		return 2
	}
	if err := c.run(ctx, d, args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(d.Stderr, "cpubench %s: %v\n", name, err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "cpubench — CPU and web benchmark with comparison reports for x86-64 and arm64 servers\n\nUsage: cpubench <command> [flags]\n\nCommands:")
	var names []string
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "  %-16s %s\n", n, commands[n].summary)
	}
	fmt.Fprintln(w, "\nRun 'cpubench <command> -h' for flags.")
}

func newFlags(d *Deps, name, args string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(d.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(d.Stderr, "Usage: cpubench %s %s\n\n", name, args)
		fs.PrintDefaults()
	}
	return fs
}

// outputFlags are shared by every command that produces a result file.
type outputFlags struct {
	label, machine, out, html, md, upload, token, power string
}

func (o *outputFlags) register(fs *flag.FlagSet, withPower bool) {
	fs.StringVar(&o.label, "label", "", "name of this run in reports (default: hostname)")
	fs.StringVar(&o.machine, "machine", "", "physical machine id, to group native/vm/container runs (default: label)")
	fs.StringVar(&o.out, "o", "", "write the result JSON here (default: results/<id>.json)")
	fs.StringVar(&o.html, "html", "", "also render an HTML report to this path")
	fs.StringVar(&o.md, "md", "", "also render a Markdown report to this path")
	fs.StringVar(&o.upload, "upload", "", "upload the result to a cpubench hub, e.g. http://hub:8090")
	fs.StringVar(&o.token, "token", os.Getenv("CPUBENCH_TOKEN"), "hub token ($CPUBENCH_TOKEN)")
	if withPower {
		fs.StringVar(&o.power, "power", "", "power source: redfish://user@bmc, ipmi://local, rapl, hwmon, const:W")
	}
}

func (o *outputFlags) meter(d *Deps) (port.PowerMeter, error) {
	if o.power == "" || d.OpenPower == nil {
		return nil, nil
	}
	return d.OpenPower(o.power)
}

// finish saves the result and produces any requested reports/uploads.
func (o *outputFlags) finish(ctx context.Context, d *Deps, f *result.File) error {
	var path string
	var err error
	if o.out != "" {
		path, err = o.out, d.WriteFile(o.out, f)
	} else {
		path, err = d.Store.Save(ctx, f)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "result: %s\n", path)
	files := []*result.File{f}
	if o.html != "" {
		if err := renderTo(o.html, d.HTML, files, port.ReportOptions{}); err != nil {
			return err
		}
		fmt.Fprintf(d.Stdout, "report: %s\n", o.html)
	}
	if o.md != "" {
		if err := renderTo(o.md, d.Markdown, files, port.ReportOptions{}); err != nil {
			return err
		}
		fmt.Fprintf(d.Stdout, "report: %s\n", o.md)
	}
	if o.upload != "" {
		if d.Upload == nil {
			return fmt.Errorf("upload not available")
		}
		if err := d.Upload(ctx, o.upload, o.token, f); err != nil {
			return fmt.Errorf("upload: %w", err)
		}
		fmt.Fprintf(d.Stdout, "uploaded: %s\n", o.upload)
	}
	return nil
}

func renderTo(path string, r port.ReportRenderer, files []*result.File, opts port.ReportOptions) error {
	w, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := r.Render(w, files, opts); err != nil {
		w.Close()
		return err
	}
	return w.Close()
}

type logger struct{ w io.Writer }

func (l logger) Printf(format string, args ...any) {
	fmt.Fprintf(l.w, "%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// Logger returns a port.Logger writing timestamped lines to w.
func Logger(w io.Writer) port.Logger { return logger{w} }

func parseFloats(s string) ([]float64, error) {
	var out []float64
	for p := range strings.SplitSeq(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var v float64
		if _, err := fmt.Sscan(p, &v); err != nil {
			return nil, fmt.Errorf("bad number %q", p)
		}
		out = append(out, v)
	}
	return out, nil
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}
