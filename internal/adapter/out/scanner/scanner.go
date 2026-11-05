// Package scanner finds the architectures of container images and of the
// native code in directories.
package scanner

import (
	"archive/zip"
	"context"
	"debug/elf"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/chaloemph/cpubench/internal/domain/scan"
)

// Docker reads image manifests with `docker buildx imagetools inspect`
// (falls back to `docker manifest inspect`). Works against any registry the
// local docker is logged in to; nothing is pulled.
type Docker struct{}

func (Docker) Platforms(ctx context.Context, ref string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "docker", "buildx", "imagetools", "inspect", "--raw", ref).Output()
	if err != nil {
		out, err = exec.CommandContext(ctx, "docker", "manifest", "inspect", ref).Output()
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	return ParseManifest(out)
}

// ParseManifest extracts os/arch from an OCI index / Docker manifest list.
// A single-platform manifest has no platform list, which is reported as empty.
func ParseManifest(raw []byte) ([]string, error) {
	var m struct {
		Manifests []struct {
			Platform struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
				Variant      string `json:"variant"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	var out []string
	for _, x := range m.Manifests {
		p := x.Platform
		if p.OS == "" || p.OS == "unknown" { // attestation manifests
			continue
		}
		s := p.OS + "/" + p.Architecture
		if p.Variant != "" {
			s += "/" + p.Variant
		}
		out = append(out, s)
	}
	return out, nil
}

// Files walks a directory (an app checkout, node_modules, a venv, an
// unpacked image) and reports native code with its architecture.
type Files struct {
	// MaxFiles stops the walk on huge trees.
	MaxFiles int
}

var skipDirs = map[string]bool{".git": true, ".hg": true, ".svn": true}

func (s Files) Scan(ctx context.Context, root string) ([]scan.Binary, error) {
	limit := s.MaxFiles
	if limit == 0 {
		limit = 500_000
	}
	var out []scan.Binary
	seen := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if seen++; seen > limit {
			return fs.SkipAll
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		name := d.Name()
		switch {
		case strings.HasSuffix(name, ".whl"):
			out = append(out, scan.Binary{Path: rel, Kind: "python-wheel", Arch: scan.ArchFromName(name)})
			return nil
		case strings.HasSuffix(name, ".jar"):
			if b, ok := scanJar(path, rel); ok {
				out = append(out, b)
			}
			return nil
		}
		if b, ok := scanELF(path, rel); ok {
			out = append(out, b)
		}
		return nil
	})
	return out, err
}

func scanELF(path, rel string) (scan.Binary, bool) {
	f, err := os.Open(path)
	if err != nil {
		return scan.Binary{}, false
	}
	defer f.Close()
	var magic [4]byte
	if _, err := f.ReadAt(magic[:], 0); err != nil || string(magic[:]) != elf.ELFMAG {
		return scan.Binary{}, false
	}
	ef, err := elf.NewFile(f)
	if err != nil {
		return scan.Binary{}, false
	}
	defer ef.Close()
	kind := "elf"
	switch {
	case strings.HasSuffix(rel, ".node"):
		kind = "node-addon"
	case strings.Contains(rel, ".so"):
		kind = "shared-lib"
	case ef.Section(".go.buildinfo") != nil:
		kind = "go-binary"
	}
	return scan.Binary{Path: rel, Kind: kind, Arch: elfArch(ef.Machine)}, true
}

func elfArch(m elf.Machine) string {
	switch m {
	case elf.EM_X86_64:
		return "amd64"
	case elf.EM_AARCH64:
		return "arm64"
	case elf.EM_386:
		return "i386"
	case elf.EM_ARM:
		return "arm"
	}
	return "unknown"
}

// scanJar reports JARs that bundle native libraries and which arches they cover.
func scanJar(path, rel string) (scan.Binary, bool) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return scan.Binary{}, false
	}
	defer zr.Close()
	arches := map[string]bool{}
	for _, f := range zr.File {
		n := strings.ToLower(f.Name)
		if strings.HasSuffix(n, ".so") {
			arches[scan.ArchFromName(n)] = true
		}
	}
	if len(arches) == 0 {
		return scan.Binary{}, false
	}
	b := scan.Binary{Path: rel, Kind: "jar-native"}
	switch {
	case arches["arm64"] && arches["amd64"]:
		b.Arch = "multi"
	case arches["arm64"]:
		b.Arch, b.Note = "arm64", "bundles arm64 .so only"
	case arches["amd64"]:
		b.Arch, b.Note = "amd64", "bundles x86-64 .so only"
	default:
		b.Arch, b.Note = "unknown", "bundles native .so of unknown arch"
	}
	return b, true
}
