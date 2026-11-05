// Package scan judges whether container images and native binaries can run
// on a target architecture (amd64 or arm64).
package scan

import (
	"fmt"
	"slices"
	"strings"
)

type Status string

const (
	Ready   Status = "ready"   // has a build for the target architecture
	Blocked Status = "blocked" // native code or image for another architecture only
	Check   Status = "check"   // could not tell, or needs a closer look
)

// Arches are the target architectures scan judges against.
var Arches = []string{"amd64", "arm64"}

// ValidArch reports an error for a target scan cannot judge.
func ValidArch(arch string) error {
	if !slices.Contains(Arches, arch) {
		return fmt.Errorf("unknown target architecture %q (amd64, arm64)", arch)
	}
	return nil
}

type Image struct {
	Ref       string   `json:"ref"`
	Platforms []string `json:"platforms"`
	Status    Status   `json:"status"`
	Note      string   `json:"note,omitempty"`
}

// Binary is a native artifact found on disk.
type Binary struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"` // elf, node-addon, python-wheel, jar-native, go-binary
	Arch   string `json:"arch"` // amd64, arm64, i386, arm, multi, unknown
	Status Status `json:"status"`
	Note   string `json:"note,omitempty"`
}

// JudgeBinary sets the status of b for the target architecture.
func JudgeBinary(b Binary, target string) Binary {
	switch {
	case b.Arch == target || b.Arch == "multi":
		b.Status = Ready
	case b.Arch == "unknown":
		b.Status = Check
	case target == "amd64" && b.Arch == "i386", target == "arm64" && b.Arch == "arm":
		// 32-bit code of the same family: needs 32-bit libraries, and some
		// arm64 cores have no AArch32 mode at all.
		b.Status = Check
	default:
		b.Status = Blocked
	}
	return b
}

// JudgeImage decides from the published platform list.
func JudgeImage(ref string, platforms []string, err error, target string) Image {
	img := Image{Ref: ref, Platforms: platforms}
	switch {
	case err != nil:
		img.Status, img.Note = Check, strings.Join(strings.Fields(err.Error()), " ")
	case slices.ContainsFunc(platforms, func(p string) bool { return strings.HasPrefix(p, "linux/"+target) }):
		img.Status = Ready
	case len(platforms) == 0:
		img.Status, img.Note = Check, "no platform information"
	default:
		img.Status, img.Note = Blocked, "no linux/"+target+" variant: rebuild with docker buildx --platform linux/amd64,linux/arm64"
	}
	return img
}

// Summary counts statuses.
func Summary[T any](items []T, status func(T) Status) map[Status]int {
	m := map[Status]int{}
	for _, it := range items {
		m[status(it)]++
	}
	return m
}

// ArchFromName guesses the architecture of a packaged artifact from its file
// name (Python wheels, prebuilt Node addons, JNI libraries inside JARs).
func ArchFromName(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "aarch64") || strings.Contains(n, "arm64"):
		return "arm64"
	case strings.Contains(n, "x86_64") || strings.Contains(n, "amd64") || strings.Contains(n, "x64"):
		return "amd64"
	case strings.Contains(n, "universal2") || strings.HasSuffix(n, "-none-any.whl"):
		return "multi"
	}
	return "unknown"
}
