package archtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// The manuals come in two languages. X.md is Thai, X.en.md is the English
// twin. These tests keep the pairs complete and every relative link working,
// so a guide cannot be added in one language only or point at a missing file.

// bilingualInOneFile are documents that hold both languages in one file.
var bilingualInOneFile = map[string]bool{"SECURITY.md": true}

// docs lists the manuals of the repository (not fixtures or vendored files).
func docs(t *testing.T) []string {
	t.Helper()
	root := "../.."
	var out []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if rel == ".git" || rel == "testdata" || rel == "dist" || rel == "results" || rel == "hubdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(rel, ".md") && rel != "LICENSE" {
			out = append(out, rel)
		}
		return nil
	})
	if len(out) == 0 {
		t.Fatal("no markdown files found")
	}
	return out
}

func hasThai(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Thai, r) {
			return true
		}
	}
	return false
}

func read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEveryManualHasBothLanguages(t *testing.T) {
	all := docs(t)
	set := map[string]bool{}
	for _, d := range all {
		set[d] = true
	}
	for _, d := range all {
		if bilingualInOneFile[d] {
			text := read(t, d)
			if !hasThai(text) || !regexp.MustCompile(`[A-Za-z]{4,} [a-z]{3,} [a-z]{3,}`).MatchString(text) {
				t.Errorf("%s must contain both Thai and English", d)
			}
			continue
		}
		if strings.HasSuffix(d, ".en.md") {
			thai := strings.TrimSuffix(d, ".en.md") + ".md"
			if !set[thai] {
				t.Errorf("%s has no Thai twin %s", d, thai)
			}
			// The only Thai allowed in an English manual is the name of the
			// language switch ("ภาษาไทย") that links to the twin.
			for i, line := range strings.Split(read(t, d), "\n") {
				if hasThai(line) && !strings.Contains(line, filepath.Base(thai)) && !strings.Contains(line, "(../README.md)") {
					t.Errorf("%s:%d: Thai text in an English manual: %.60s", d, i+1, line)
				}
			}
			continue
		}
		en := strings.TrimSuffix(d, ".md") + ".en.md"
		if !set[en] {
			t.Errorf("%s has no English twin %s", d, en)
		}
		if !hasThai(read(t, d)) {
			t.Errorf("%s is the Thai manual but has no Thai text", d)
		}
	}
}

var (
	linkRe   = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	fenceRe  = regexp.MustCompile("(?s)```.*?```")
	headerRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*$`)
)

// slug follows GitHub's rule for heading anchors: lower case, drop everything
// except letters, digits, spaces and hyphens, then turn spaces into hyphens.
func slug(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	var b strings.Builder
	for _, r := range h {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

func anchors(text string) map[string]bool {
	text = fenceRe.ReplaceAllString(text, "")
	out := map[string]bool{}
	for _, m := range headerRe.FindAllStringSubmatch(text, -1) {
		h := strings.NewReplacer("`", "", "*", "").Replace(m[1])
		out[slug(h)] = true
	}
	return out
}

func TestManualLinksResolve(t *testing.T) {
	for _, d := range docs(t) {
		text := fenceRe.ReplaceAllString(read(t, d), "")
		for _, m := range linkRe.FindAllStringSubmatch(text, -1) {
			link := m[1]
			if strings.Contains(link, "://") || strings.HasPrefix(link, "mailto:") {
				continue
			}
			file, frag, _ := strings.Cut(link, "#")
			target := d
			if file != "" {
				target = filepath.ToSlash(filepath.Join(filepath.Dir(d), file))
				if _, err := os.Stat(filepath.Join("../..", target)); err != nil {
					t.Errorf("%s: link %q points at a missing file", d, link)
					continue
				}
			}
			if frag == "" || !strings.HasSuffix(target, ".md") {
				continue
			}
			if !anchors(read(t, target))[frag] {
				t.Errorf("%s: link %q: no heading %q in %s", d, link, frag, target)
			}
		}
	}
}
