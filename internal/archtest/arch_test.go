// Package archtest enforces the hexagonal dependency rules with plain go test,
// so they hold even where golangci-lint/depguard is not installed.
package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/chaloemph/cpubench/"

type rule struct {
	dir    string   // package tree the rule applies to
	banned []string // import prefixes not allowed there
	why    string
}

var rules = []rule{
	{"internal/domain", []string{"os", "os/exec", "net", "net/http", "syscall", "golang.org/x/sys", module + "internal/adapter", module + "internal/app", module + "internal/port", module + "internal/workload"},
		"domain is pure: no I/O and no dependency on outer layers"},
	{"internal/port", []string{module + "internal/adapter", module + "internal/app"},
		"ports are interfaces over the domain only"},
	{"internal/app", []string{module + "internal/adapter", "os/exec", "net/http"},
		"use cases reach the outside world only through ports"},
	{"internal/workload", []string{"os", "os/exec", "net", "net/http", module + "internal/adapter", module + "internal/app"},
		"kernels must not do I/O inside the measured code"},
	{"internal/adapter/out", []string{module + "internal/adapter/in", module + "internal/app"},
		"driven adapters must not call back into the app"},
}

func TestDependencyRules(t *testing.T) {
	root := "../.."
	for _, r := range rules {
		filepath.WalkDir(filepath.Join(root, r.dir), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			test := strings.HasSuffix(path, "_test.go")
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				for _, b := range r.banned {
					if test && (b == "os" || b == "net/http") {
						continue // tests may use temp dirs and httptest
					}
					if p == b || strings.HasPrefix(p, b+"/") {
						rel, _ := filepath.Rel(root, path)
						t.Errorf("%s imports %q: %s", rel, p, r.why)
					}
				}
			}
			return nil
		})
	}
}
