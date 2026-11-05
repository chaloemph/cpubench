package report

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"slices"
	"strings"
	"unicode"

	"github.com/chaloemph/cpubench/internal/domain/doctor"
	"github.com/chaloemph/cpubench/internal/domain/result"
	"github.com/chaloemph/cpubench/internal/port"
)

//go:embed report.html.tmpl
var htmlSrc string

var htmlTmpl = template.Must(template.New("report").Funcs(funcs).Parse(htmlSrc))

// HTML renders a single self-contained page.
type HTML struct{}

func (HTML) Render(w io.Writer, files []*result.File, opts port.ReportOptions) error {
	if len(files) == 0 {
		return fmt.Errorf("no result files")
	}
	return htmlTmpl.Execute(w, buildView(files, opts))
}

// Markdown renders the summary tables for wikis and pull requests.
type Markdown struct{}

func (Markdown) Render(w io.Writer, files []*result.File, opts port.ReportOptions) error {
	if len(files) == 0 {
		return fmt.Errorf("no result files")
	}
	v := buildView(files, opts)
	T := translator(v.Lang)
	// The sections are written to body first, because the contents list that
	// precedes them can only be built once every section that exists is known.
	var head, body bytes.Buffer
	ph := func(format string, a ...any) { fmt.Fprintf(&head, format, a...) }
	p := func(format string, a ...any) { fmt.Fprintf(&body, format, a...) }

	// Sections are numbered so the report can be cited a section at a time,
	// and ordered conclusion → conditions → numbers → limits: a reader who
	// stops early should stop on the caveats, not on a bare ratio.
	n := 0
	var toc []tocItem
	h2 := func(title string, first bool, hint string) {
		n++
		full := fmt.Sprintf("%d. %s", n, title)
		toc = append(toc, tocItem{Title: full, Anchor: mdAnchor(full), First: first, Hint: hint})
		p("## %s\n\n", full)
	}

	ph("# %s\n\n", v.Title)
	if v.Verdict != "" {
		ph("%s\n\n", v.Verdict)
	}
	ph("%s", T("hdr.generated", v.Generated))
	if len(v.Others) > 0 {
		ph(" · %s **%s**", T("hdr.baseline"), v.Baseline)
	}
	ph(" · %s\n\n", T("hdr.files", len(v.Files)))

	if blocking := filterSev(v.Caveats, doctor.Fail, doctor.Warn); len(blocking) > 0 {
		ph("> [!IMPORTANT]\n> %s\n>\n", T("callout.title"))
		for _, c := range blocking {
			ph("> - **%s** %s\n", c.Severity, c.Text)
		}
		ph("\n")
	}
	if notes := filterSev(v.Caveats, doctor.Info); len(notes) > 0 {
		ph("%s:\n\n", T("callout.notes"))
		for _, c := range notes {
			ph("- %s\n", c.Text)
		}
		ph("\n")
	}

	if len(v.Env) > 0 {
		h2(T("sec.conditions"), true, T("hint.conditions"))
		p("| | %s |\n|---|%s\n", strings.Join(v.Labels, " | "), strings.Repeat("---|", len(v.Labels)))
		for _, r := range v.Env {
			p("| %s | %s |\n", r.Key, strings.Join(r.Values, " | "))
		}
		p("\n")
	}
	if len(v.Cards) > 0 {
		h2(T("sec.headline"), true, T("hint.headline"))
		p("| metric | value | detail |\n|---|---|---|\n")
		for _, c := range v.Cards {
			p("| %s | %s | %s |\n", c.Label, c.Value, c.Sub)
		}
		p("\n")
	}
	if len(v.Others) > 0 && len(v.Cats) > 0 {
		h2(T("sec.category", v.Baseline), true, T("hint.category"))
		p("| %s | %s | %s |\n|---|%s---|\n", T("tbl.category"), strings.Join(v.Others, " | "), T("tbl.care"), strings.Repeat("---|", len(v.Others)))
		for _, c := range v.Cats {
			// The category's own rows are numbered with the benchmark section,
			// which is not written yet; the link is filled in at the end.
			p("| %s |", catLinkMark(c.Category))
			for _, cell := range c.Cells {
				if cell.Range != "" {
					p(" %s (%s) |", cell.Text, cell.Range)
				} else {
					p(" %s |", cell.Text)
				}
			}
			p(" %s |\n", orDash(c.Note))
		}
		p("\n%s\n\n", T("tbl.geomean"))
	}
	if w := v.Whole; w != nil {
		h2(T("sec.whole", v.Baseline), true, T("hint.whole"))
		p("| | %s |\n|---|%s\n", strings.Join(v.Others, " | "), strings.Repeat("---|", len(v.Others)))
		for _, r := range w.Rows {
			p("| %s |", r.Basis)
			for _, c := range r.Cells {
				p(" %s |", c.Text)
			}
			p("\n")
		}
		p("\n**%s**\n\n| | %s |\n|---|%s\n", T("whole.divisors"), strings.Join(v.Labels, " | "), strings.Repeat("---|", len(v.Labels)))
		for _, r := range w.Divisors {
			p("| %s | %s |\n", r.Key, strings.Join(r.Values, " | "))
		}
		p("\n%s\n\n", T("whole.note"))
	}
	if len(v.Layers) > 0 {
		h2(T("sec.layers"), true, T("hint.layers"))
		p("| %s | %s | %s | %s | %s |\n|---|---|---|---|---|\n", T("lay.machine"), T("col.run"), T("lay.cmp"), T("lay.micro"), T("lay.web"))
		for _, l := range v.Layers {
			for _, label := range sortedKeys(l.MicroPct) {
				web := "–"
				if wp, ok := l.WebPct[label]; ok {
					web = fmt.Sprintf("%+.1f%%", wp)
				}
				p("| %s | %s | %s (%s) | %+.1f%% | %s |\n", l.Machine, label, l.Base, l.BaseLayer, l.MicroPct[label], web)
			}
		}
		p("\n")
	}
	if len(v.WebFiles) > 0 {
		h2(T("sec.web"), true, T("hint.web"))
		for _, f := range v.WebFiles {
			p("### %s (%s, %s)\n\n%s: **%s**\n\n", f.Label, f.Web.Profile, f.Web.Target, T("web.knee", f.Web.SLO.P99Ms), num(f.Web.KneeRPS))
			p("| %s | %s | p50 | p99 | %s | %s | SLO |\n|---|---|---|---|---|---|---|\n", T("web.target"), T("web.achieved"), T("col.errors"), T("web.sut"))
			for _, s := range f.Web.Steps {
				ok := T("web.fail")
				if s.PassSLO {
					ok = T("web.pass")
				}
				cpu := "–"
				if s.SUTCPUPct > 0 {
					cpu = fmt.Sprintf("%.0f%%", s.SUTCPUPct)
				}
				p("| %s | %s | %.1f | %.1f | %d | %s | %s |\n", num(s.TargetRPS), num(s.AchievedRPS), s.P50Ms, s.P99Ms, s.Errors, cpu, ok)
			}
			p("\n")
		}
		p("%s\n\n", T("web.ceiling"))
	}
	if v.TCO != nil {
		h2(T("sec.tco", num(v.TCO.TargetRPS), v.TCO.Redundancy), true, T("hint.tco"))
		p("| %s | RPS@SLO | %s | %s | %s |\n|---|---|---|---|---|\n", T("tco.server"), T("tco.watts"), T("tco.servers"), T("tco.total"))
		for _, r := range v.TCO.Rows {
			p("| %s | %s | %.0f | %d | %s %s |\n", r.Label, num(r.KneeRPS), r.Watts, r.Servers, money(r.Total), r.Currency)
		}
		p("\n")
	}
	if len(v.StressFiles) > 0 {
		h2(T("sec.stress"), false, T("hint.stress"))
		p("| %s | %s | %s | %s | %s | %s | %s |\n|---|---|---|---|---|---|---|\n", T("col.run"), T("str.threads"), T("str.duration"), T("str.peak"), T("str.sustained"), T("str.change"), T("col.errors"))
		for _, f := range v.StressFiles {
			s := f.Stress
			p("| %s | %d | %.0fs | %s | %s | %+.1f%% | %d |\n",
				f.Label, s.Threads, s.DurationSec, num(s.PeakRate), num(s.SustainedRate), s.DegradationPct, s.Errors)
		}
		p("\n")
	}
	catLinks := map[string]string{}
	if len(v.Rows) > 0 {
		h2(T("sec.benchmarks"), false, T("hint.benchmarks"))
		// One folded table per category, each under a heading that stays
		// visible, so a link from the category table lands on its rows.
		for i, g := range v.Groups {
			title := fmt.Sprintf("%d.%d %s", n, i+1, g.Category)
			catLinks[g.Category] = mdAnchor(title)
			p("### %s\n\n<details>\n<summary>%s</summary>\n\n", title, T("bm.show", len(g.Rows)))
			p("| %s | %s | %s |", T("bm.name"), T("bm.threads"), T("bm.unit"))
			for _, l := range v.Labels {
				if l == v.Baseline && len(v.Others) > 0 {
					l = T("bm.base", l)
				}
				p(" %s |", l)
			}
			for _, l := range v.Others {
				p(" %s |", T("bm.vsbase", l))
			}
			p("\n|---|---|---|%s%s\n", strings.Repeat("---|", len(v.Labels)), strings.Repeat("---|", len(v.Others)))
			for _, r := range g.Rows {
				name := r.Name
				if r.Mismatch {
					name += T("bm.md_checksum")
				}
				if r.BackendDiffers {
					name += T("bm.md_impl")
				}
				p("| %s | %d | %s |", name, r.Threads, r.Unit)
				for _, c := range r.Cells {
					if c.Backend != "" {
						p(" %s (%s) |", c.Value, c.Backend)
					} else {
						p(" %s |", c.Value)
					}
				}
				for _, s := range r.Speedups {
					p(" %s |", s.Text)
				}
				p("\n")
			}
			p("\n</details>\n\n")
		}
	}
	if len(v.Findings) > 0 {
		h2(T("sec.doctor"), false, T("hint.doctor"))
		// Open when anything warns: the comparison's own findings (such as a
		// different load generator) appear nowhere else.
		open := ""
		if hasWarn(v.Findings) {
			open = " open"
		}
		p("<details%s>\n<summary>%s</summary>\n\n", open, T("doc.show", countFindings(v.Findings)))
		for _, g := range v.Findings {
			for _, f := range g.Findings {
				p("- **%s** [%s] %s\n", g.Label, f.Severity, f.Message)
			}
		}
		p("\n</details>\n\n")
	}
	if len(v.Limits) > 0 {
		h2(T("sec.limits"), true, T("hint.limits"))
		for _, l := range v.Limits {
			p("- %s\n", l)
		}
		p("\n")
	}
	if len(v.Terms) > 0 {
		h2(T("sec.terms"), false, T("hint.terms"))
		for _, t := range v.Terms {
			p("**%s**\n\n%s\n\n", t.Name, t.Intro)
			for _, pt := range t.Points {
				p("- %s\n", pt)
			}
			p("\n")
		}
	}

	out := body.String()
	for _, c := range v.Cats {
		link := c.Category
		if a, ok := catLinks[c.Category]; ok {
			link = fmt.Sprintf("[%s](#%s)", c.Category, a)
		}
		out = strings.ReplaceAll(out, catLinkMark(c.Category), link)
	}

	if _, err := w.Write(head.Bytes()); err != nil {
		return err
	}
	if len(toc) > 1 {
		if _, err := io.WriteString(w, mdContents(toc, T)); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, out)
	return err
}

// catLinkMark stands in for a category's link until its rows are numbered.
func catLinkMark(cat string) string { return "\x00cat:" + cat + "\x00" }

// tocItem is one section of the Markdown report in its contents list.
type tocItem struct {
	Title, Anchor, Hint string
	// First marks the sections to read in every case; the rest are reference.
	First bool
}

// mdContents lists the sections twice over: the ones to read, then the ones
// to open when something needs checking. The numbering stays the document's.
func mdContents(items []tocItem, T tfn) string {
	var b strings.Builder
	b.WriteString("**" + T("toc.title") + "**\n\n")
	for _, grp := range []struct {
		head  string
		first bool
	}{{T("toc.first") + ":", true}, {T("toc.later") + ":", false}} {
		var lines []string
		for _, it := range items {
			if it.First == grp.first {
				lines = append(lines, fmt.Sprintf("- [%s](#%s) — %s\n", it.Title, it.Anchor, it.Hint))
			}
		}
		if len(lines) > 0 {
			b.WriteString(grp.head + "\n\n" + strings.Join(lines, "") + "\n")
		}
	}
	return b.String()
}

// mdAnchor is the heading slug GitHub and GitLab give a Markdown heading:
// lower-cased, spaces and hyphens become hyphens, other punctuation is dropped.
// Marks stay: Thai vowels and tone marks are marks, not letters, and dropping
// them gives an anchor that matches no heading.
func mdAnchor(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) || r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '-':
			b.WriteByte('-')
		}
	}
	return b.String()
}

// hasWarn reports a finding at warn or fail in any group.
func hasWarn(gs []findingGroup) bool {
	for _, g := range gs {
		for _, f := range g.Findings {
			if f.Severity != doctor.Info {
				return true
			}
		}
	}
	return false
}

func countFindings(gs []findingGroup) int {
	n := 0
	for _, g := range gs {
		n += len(g.Findings)
	}
	return n
}

func filterSev(cs []caveat, keep ...doctor.Severity) []caveat {
	var out []caveat
	for _, c := range cs {
		if slices.Contains(keep, c.Severity) {
			out = append(out, c)
		}
	}
	return out
}

func sortedKeys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
