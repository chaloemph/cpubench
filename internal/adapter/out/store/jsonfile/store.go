// Package jsonfile stores result files as JSON documents in a directory.
package jsonfile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/chaloemph/cpubench/internal/domain/result"
)

type Store struct{ Dir string }

func (s Store) Save(_ context.Context, f *result.File) (string, error) {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(s.Dir, f.ID+".json")
	return p, Write(p, f)
}

// Load accepts a path to a file or an ID inside Dir.
func (s Store) Load(_ context.Context, ref string) (*result.File, error) {
	if _, err := os.Stat(ref); err == nil {
		return Read(ref)
	}
	if strings.ContainsAny(ref, `/\`) || strings.Contains(ref, "..") {
		return nil, fmt.Errorf("result %q not found", ref)
	}
	return Read(filepath.Join(s.Dir, strings.TrimSuffix(ref, ".json")+".json"))
}

func (s Store) List(_ context.Context) ([]result.File, error) {
	paths, _ := filepath.Glob(filepath.Join(s.Dir, "*.json"))
	var out []result.File
	for _, p := range paths {
		f, err := Read(p)
		if err != nil {
			continue
		}
		out = append(out, *f)
	}
	slices.SortFunc(out, func(a, b result.File) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return out, nil
}

func Write(path string, f *result.File) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func Read(path string) (*result.File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f result.File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if !strings.HasPrefix(f.Schema, "cpubench/") {
		return nil, fmt.Errorf("%s: not a cpubench result (schema %q)", path, f.Schema)
	}
	return &f, nil
}
