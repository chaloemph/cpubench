// Package app holds cpubench's use cases. It talks to the machine only
// through the interfaces in internal/port.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/build"
	"github.com/chaloemph/cpubench/internal/domain/doctor"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
)

type App struct {
	Probe   port.SystemProbe
	Sensors port.Sensors
	Pinner  port.Pinner
	Log     port.Logger
	Version string
	Build   build.Info
}

// Info returns the system snapshot plus doctor findings.
func (a *App) Info(ctx context.Context) (hardware.System, []doctor.Finding, error) {
	s, err := a.Probe.System(ctx)
	if err != nil {
		return s, nil, err
	}
	return s, append(doctor.Check(s), build.Self(a.Build)...), nil
}

func (a *App) logf(format string, args ...any) {
	if a.Log != nil {
		a.Log.Printf(format, args...)
	}
}

// newFile starts a result file for a command.
func (a *App) newFile(ctx context.Context, label, machine, command string) (*result.File, error) {
	sys, err := a.Probe.System(ctx)
	if err != nil {
		return nil, err
	}
	if label == "" {
		label = sys.Hostname
	}
	if machine == "" {
		machine = label
	}
	b := a.Build
	return &result.File{
		Schema: result.Schema, ID: NewID(label), Label: label, Machine: machine,
		Tool: "cpubench " + a.Version, Command: command, CreatedAt: time.Now().UTC(), Build: &b,
		System: sys, Findings: append(doctor.Check(sys), build.Self(b)...),
	}, nil
}

// NewID is label-timestamp-random, safe for file names.
func NewID(label string) string {
	b := make([]byte, 3)
	rand.Read(b)
	clean := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, label)
	return fmt.Sprintf("%s-%s-%s", clean, time.Now().UTC().Format("20060102T150405"), hex.EncodeToString(b))
}

// cpuOrder returns the CPUs threads will be pinned to, in order.
func cpuOrder(sys hardware.System, numaNode int) []int {
	if numaNode >= 0 {
		return hardware.CPUsOnNode(sys.Topology, numaNode)
	}
	order := hardware.PlacementOrder(sys.Topology)
	if n := sys.Context.EffectiveCPUs; n > 0 && n < len(order) {
		order = order[:n]
	}
	return order
}

// ResolveThreads turns a thread list where 0 means "all usable CPUs" into
// sorted, de-duplicated counts no larger than usable.
func ResolveThreads(req []int, usable int) []int {
	if len(req) == 0 {
		req = []int{1, 0}
	}
	var out []int
	for _, t := range req {
		if t <= 0 || t > usable {
			t = usable
		}
		out = append(out, t)
	}
	slices.Sort(out)
	return slices.Compact(out)
}
