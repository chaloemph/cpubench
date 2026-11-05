// Package build describes how the cpubench binary itself was built, so a
// comparison can tell CPU differences from source or compiler differences.
package build

import (
	"fmt"
	"runtime/debug"

	"github.com/chaloemph/cpubench/internal/domain/doctor"
)

type Info struct {
	// Version is what -X main.version was set to (git describe in the Makefile).
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Modified  bool   `json:"modified,omitempty"`
	GoVersion string `json:"go_version"`
	GOARCH    string `json:"goarch"`
	// ArchLevel is the instruction set level the compiler targeted:
	// GOAMD64 (v1..v4) on amd64, GOARM64 (v8.0..v9.5) on arm64.
	ArchLevel  string `json:"arch_level,omitempty"`
	CGO        bool   `json:"cgo"`
	Tags       string `json:"tags,omitempty"`
	Experiment string `json:"goexperiment,omitempty"`
}

// defaultLevel is what Go targets when GOAMD64/GOARM64 is not set.
var defaultLevel = map[string]string{"amd64": "GOAMD64=v1", "arm64": "GOARM64=v8.0"}

// FromBuildInfo reads the settings Go embeds in every binary. bi may be nil
// (e.g. a binary built without module support).
func FromBuildInfo(bi *debug.BuildInfo, version string) Info {
	i := Info{Version: version}
	if bi == nil {
		return i
	}
	i.GoVersion = bi.GoVersion
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			i.Commit = s.Value
		case "vcs.modified":
			i.Modified = s.Value == "true"
		case "GOARCH":
			i.GOARCH = s.Value
		case "GOAMD64", "GOARM64":
			i.ArchLevel = s.Key + "=" + s.Value
		case "CGO_ENABLED":
			i.CGO = s.Value == "1"
		case "-tags":
			i.Tags = s.Value
		case "GOEXPERIMENT":
			i.Experiment = s.Value
		}
	}
	return i
}

// BaselineLevel reports whether the compiler targeted the architecture's
// default level, i.e. generated code any CPU of that architecture can run.
func (i Info) BaselineLevel() bool {
	d, ok := defaultLevel[i.GOARCH]
	return !ok || i.ArchLevel == "" || i.ArchLevel == d
}

// ShortCommit is the first 12 characters of the commit, or "".
func (i Info) ShortCommit() string {
	if len(i.Commit) > 12 {
		return i.Commit[:12]
	}
	return i.Commit
}

func (i Info) String() string {
	s := i.Version
	if c := i.ShortCommit(); c != "" {
		s += " · " + c
	}
	if i.Modified {
		s += " (modified)"
	}
	s += " · " + i.GoVersion
	if i.ArchLevel != "" {
		s += " · " + i.ArchLevel
	}
	if i.CGO {
		s += " · cgo"
	}
	if i.Tags != "" {
		s += " · tags " + i.Tags
	}
	return s
}

// Self reports problems with one build on its own.
func Self(i Info) []doctor.Finding {
	if !i.Modified {
		return nil
	}
	return []doctor.Finding{{ID: "build-modified", Severity: doctor.Warn,
		Message: fmt.Sprintf("cpubench was built from uncommitted changes (%s): another binary with the same version may run different code", i),
		Fix:     "commit, then rebuild every binary from that commit (make build-all)"}}
}

// Comparable reports differences between two builds that make their results
// differ for reasons other than the CPU. Go version differences are reported
// by doctor.Comparable from the system snapshot.
func Comparable(a, b Info) []doctor.Finding {
	var out []doctor.Finding
	switch {
	case a.Version != b.Version:
		out = append(out, doctor.Finding{ID: "build-version", Severity: doctor.Warn,
			Message: fmt.Sprintf("cpubench version differs: %s vs %s", a.Version, b.Version),
			Fix:     "run binaries built from the same commit on every machine (make build-all)"})
	case a.Commit != "" && b.Commit != "" && a.Commit != b.Commit:
		out = append(out, doctor.Finding{ID: "build-version", Severity: doctor.Warn,
			Message: fmt.Sprintf("cpubench built from different commits: %s vs %s", a.ShortCommit(), b.ShortCommit()),
			Fix:     "run binaries built from the same commit on every machine (make build-all)"})
	}
	switch {
	case a.GOARCH == b.GOARCH && a.ArchLevel != b.ArchLevel:
		out = append(out, doctor.Finding{ID: "build-level", Severity: doctor.Warn,
			Message: fmt.Sprintf("compiled for different instruction set levels: %s vs %s", a.ArchLevel, b.ArchLevel),
			Fix:     "build both with the same level (make build-all uses the default)"})
	case !a.BaselineLevel() || !b.BaselineLevel():
		out = append(out, doctor.Finding{ID: "build-level", Severity: doctor.Warn,
			Message: fmt.Sprintf("compiled for %s on %s vs %s on %s: the compiler may use newer instructions on one side only",
				a.ArchLevel, a.GOARCH, b.ArchLevel, b.GOARCH),
			Fix: "build both with the default level (make build-all), or note the levels next to the result"})
	}
	if a.Tags != b.Tags {
		out = append(out, doctor.Finding{ID: "build-tags", Severity: doctor.Warn,
			Message: fmt.Sprintf("built with different tags: %q vs %q (e.g. purego turns off crypto assembly)", a.Tags, b.Tags)})
	}
	if a.Experiment != b.Experiment {
		out = append(out, doctor.Finding{ID: "build-experiment", Severity: doctor.Warn,
			Message: fmt.Sprintf("built with different GOEXPERIMENT: %q vs %q", a.Experiment, b.Experiment)})
	}
	if a.CGO != b.CGO {
		out = append(out, doctor.Finding{ID: "build-cgo", Severity: doctor.Info,
			Message: fmt.Sprintf("cgo differs: %v vs %v (affects DNS resolution in web tests, not the micro suite)", a.CGO, b.CGO)})
	}
	return out
}
