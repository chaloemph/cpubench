package report

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
)

// Every key the code asks for must exist, in both languages. A missing key
// shows up in the report as "!!key", which no reader should ever see.
func TestCatalogCoversEveryKeyUsed(t *testing.T) {
	// a key is passed as T("k"), $.T "k" in the template, v.T("k"), or as the
	// second argument of the caveat helper: add(severity, "k", ...)
	use := regexp.MustCompile(`(?:\bT\(|\$\.T |\bv\.T\(|\badd\(doctor\.\w+, )"([a-z0-9_.]+)"`)
	files, _ := os.ReadDir(".")
	used := map[string]bool{}
	for _, f := range files {
		name := f.Name()
		if strings.HasSuffix(name, "_test.go") || (!strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, ".tmpl")) {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range use.FindAllStringSubmatch(string(src), -1) {
			used[m[1]] = true
		}
	}
	if len(used) < 100 {
		t.Fatalf("found only %d keys in use; the pattern no longer matches the code", len(used))
	}
	for k := range used {
		m, ok := catalog[k]
		if !ok {
			t.Errorf("key %q is used but not in the catalog", k)
			continue
		}
		if m.en == "" || m.th == "" {
			t.Errorf("key %q lacks a language: en=%q th=%q", k, m.en, m.th)
		}
	}
	for k := range catalog {
		if !used[k] {
			t.Errorf("key %q is in the catalog but nothing uses it", k)
		}
	}
}

// A Thai string must actually be Thai, and carry the same % verbs as its
// English twin, or Sprintf will print garbage when the language is switched.
func TestCatalogLanguagesAgree(t *testing.T) {
	verbs := regexp.MustCompile(`%[-+#0 ]*[0-9]*(?:\.[0-9]+)?[sdfvgq]`)
	for k, m := range catalog {
		if a, b := verbs.FindAllString(m.en, -1), verbs.FindAllString(m.th, -1); strings.Join(a, " ") != strings.Join(b, " ") {
			t.Errorf("%q: format verbs differ, en %v th %v", k, a, b)
		}
	}
	// Thai prose should hold Thai letters; short technical tokens may not.
	for k, m := range catalog {
		if len([]rune(m.en)) > 40 && !strings.ContainsFunc(m.th, func(r rune) bool { return unicode.Is(unicode.Thai, r) }) {
			t.Errorf("%q: long text with no Thai in it: %q", k, m.th)
		}
	}
}

func TestThaiIsTheDefault(t *testing.T) {
	files, opts := fullSet()
	opts.Lang = ""
	var md, html bytes.Buffer
	if err := (Markdown{}).Render(&md, files, opts); err != nil {
		t.Fatal(err)
	}
	if err := (HTML{}).Render(&html, files, opts); err != nil {
		t.Fatal(err)
	}
	for name, out := range map[string]string{"markdown": md.String(), "html": html.String()} {
		for _, want := range []string{"เงื่อนไขการทดสอบ", "สรุปตัวเลขหลัก", "สิ่งที่รายงานนี้ตอบไม่ได้"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: default report lacks %q", name, want)
			}
		}
		if strings.Contains(out, "!!") {
			t.Errorf("%s: an untranslated key leaked into the report", name)
		}
	}
	if !strings.Contains(html.String(), `<html lang="th">`) {
		t.Error("html lang attribute is not th")
	}
	// the data stays as it is: labels, benchmark names, units
	for _, keep := range []string{"x86-01", "int/json", "arm-01"} {
		if !strings.Contains(md.String(), keep) {
			t.Errorf("label or benchmark name %q was lost", keep)
		}
	}
}

// Both languages render every section, with no key missing, for a report that
// shows everything the renderer can show.
func TestBothLanguagesRenderEverySection(t *testing.T) {
	for _, lang := range []string{"th", "en"} {
		files := []*result.File{
			full("x86-01", "x86-01", "amd64", hardware.ModeNative, 1),
			full("arm-01-pinned", "arm-01", "arm64", hardware.ModeNative, 1.3),
			full("arm-01-unpinned", "arm-01", "arm64", hardware.ModeVM, 1.2),
		}
		_, opts := fullSet()
		opts.Lang = lang
		var md, html bytes.Buffer
		if err := (Markdown{}).Render(&md, files, opts); err != nil {
			t.Fatal(err)
		}
		if err := (HTML{}).Render(&html, files, opts); err != nil {
			t.Fatal(err)
		}
		for name, out := range map[string]string{"markdown": md.String(), "html": html.String()} {
			if strings.Contains(out, "!!") {
				t.Errorf("%s/%s: an untranslated key leaked into the report", lang, name)
			}
		}
		// English words must be gone from the report's own prose in Thai
		if lang == "th" {
			for _, gone := range []string{"Read before any number", "Conditions", "What this report does not answer", "Show all", "Terms used in this report"} {
				if strings.Contains(md.String(), gone) || strings.Contains(html.String(), gone) {
					t.Errorf("th report still contains the English %q", gone)
				}
			}
		}
	}
}

// Thai text carries the same checks as English ones: the unpinned warning must
// come above the numbers it limits, in either language.
func TestThaiCaveatsKeepTheirPlace(t *testing.T) {
	files := []*result.File{
		full("x86-host", "x86-host", "amd64", hardware.ModeVM, 1),
		full("arm64-unpinned-1", "arm64-unpinned-1", "arm64", hardware.ModeVM, 1),
	}
	var md bytes.Buffer
	if err := (Markdown{}).Render(&md, files, port.ReportOptions{Baseline: "x86-host", Lang: "th"}); err != nil {
		t.Fatal(err)
	}
	out := md.String()
	i, j := strings.Index(out, "รันโดยไม่ได้ผูก NUMA"), strings.Index(out, "## 1.")
	if i < 0 || i > j {
		t.Errorf("Thai unpinned warning at %d must come before the first section at %d", i, j)
	}
	if !strings.Contains(out, "สารบัญ") || !strings.Contains(out, "อ่านก่อน:") || !strings.Contains(out, "เปิดดูเมื่อสงสัย:") {
		t.Error("Thai contents list is missing")
	}
	if !strings.Contains(out, "## 8. ความหมายของคำที่ใช้ในรายงาน") && !strings.Contains(out, ". ความหมายของคำที่ใช้ในรายงาน") {
		t.Error("Thai terms section is missing")
	}
}

// An unknown language must not give a half-translated report.
func TestUnknownLanguageFallsBackToThai(t *testing.T) {
	if normLang("fr") != LangTH || normLang("") != LangTH || normLang("en") != LangEN {
		t.Error("normLang does not map to th/en")
	}
}

// Thai vowel and tone marks are Unicode marks, not letters. An anchor that
// drops them points at no heading, so the contents list would be dead links.
func TestThaiAnchorsKeepMarks(t *testing.T) {
	for in, want := range map[string]string{
		"1. เงื่อนไขการทดสอบ":             "1-เงื่อนไขการทดสอบ",
		"7. สิ่งที่รายงานนี้ตอบไม่ได้":    "7-สิ่งที่รายงานนี้ตอบไม่ได้",
		"4. Stress (ค่าต่อเนื่อง)":        "4-stress-ค่าต่อเนื่อง",
		"4. Virtualisation / container x": "4-virtualisation--container-x",
	} {
		if got := mdAnchor(in); got != want {
			t.Errorf("mdAnchor(%q) = %q, want %q", in, got, want)
		}
	}
}
