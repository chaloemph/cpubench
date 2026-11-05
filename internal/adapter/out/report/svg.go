package report

import (
	"fmt"
	"html/template"
	"math"
	"strings"
)

// Charts are rendered server-side as inline SVG so the report is one
// self-contained HTML file that opens offline.

type series struct {
	Name string
	X, Y []float64
}

type lineOpts struct {
	Title, XLabel, YLabel string
	LogX                  bool
	ZeroY                 bool
	XTicks                []float64 // optional explicit ticks
	XTickFmt              func(float64) string
	RefY                  float64 // dashed horizontal line (e.g. SLO), 0 = none
}

const (
	cw, ch           = 640.0, 260.0
	padL, padR       = 64.0, 16.0
	padT, padB       = 28.0, 44.0
	plotW, plotH     = cw - padL - padR, ch - padT - padB
	maxSeriesPalette = 6
)

func lineChart(ss []series, o lineOpts) template.HTML {
	var xs, ys []float64
	for _, s := range ss {
		xs = append(xs, s.X...)
		ys = append(ys, s.Y...)
	}
	if len(xs) == 0 {
		return ""
	}
	xmin, xmax := minmax(xs)
	ymin, ymax := minmax(ys)
	if o.RefY > 0 {
		ymax = math.Max(ymax, o.RefY)
	}
	if o.ZeroY || ymin > 0 && ymin < ymax*0.5 {
		ymin = 0
	}
	if ymax == ymin {
		ymax = ymin + 1
	}
	ymax = niceCeil(ymax)
	tx := func(x float64) float64 {
		if o.LogX {
			lmin, lmax := math.Log(math.Max(xmin, 1e-9)), math.Log(math.Max(xmax, 1e-9))
			if lmax == lmin {
				return padL + plotW/2
			}
			return padL + (math.Log(math.Max(x, 1e-9))-lmin)/(lmax-lmin)*plotW
		}
		if xmax == xmin {
			return padL + plotW/2
		}
		return padL + (x-xmin)/(xmax-xmin)*plotW
	}
	ty := func(y float64) float64 { return padT + plotH - (y-ymin)/(ymax-ymin)*plotH }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %.0f %.0f" role="img"><title>%s</title>`, cw, ch, template.HTMLEscapeString(o.Title))
	fmt.Fprintf(&b, `<text class="ct" x="%.0f" y="16">%s</text>`, padL, template.HTMLEscapeString(o.Title))
	for i := 0; i <= 4; i++ {
		y := ymin + (ymax-ymin)*float64(i)/4
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, padL, padL+plotW, ty(y), ty(y))
		fmt.Fprintf(&b, `<text class="ax" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, padL-6, ty(y)+4, num(y))
	}
	ticks := o.XTicks
	if len(ticks) == 0 {
		ticks = uniqueSorted(xs)
		if len(ticks) > 10 {
			ticks = spread(ticks, 8)
		}
	}
	for _, x := range ticks {
		label := num(x)
		if o.XTickFmt != nil {
			label = o.XTickFmt(x)
		}
		fmt.Fprintf(&b, `<text class="ax" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, tx(x), padT+plotH+16, label)
	}
	fmt.Fprintf(&b, `<text class="ax" x="%.1f" y="%.0f" text-anchor="middle">%s</text>`, padL+plotW/2, ch-6, template.HTMLEscapeString(o.XLabel))
	fmt.Fprintf(&b, `<text class="ax" x="12" y="%.1f" transform="rotate(-90 12 %.1f)" text-anchor="middle">%s</text>`, padT+plotH/2, padT+plotH/2, template.HTMLEscapeString(o.YLabel))
	if o.RefY > 0 {
		fmt.Fprintf(&b, `<line class="ref" x1="%.1f" x2="%.1f" y1="%.1f" y2="%.1f"/>`, padL, padL+plotW, ty(o.RefY), ty(o.RefY))
	}
	for i, s := range ss {
		cls := fmt.Sprintf("s%d", i%maxSeriesPalette)
		var pts []string
		for j := range s.X {
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", tx(s.X[j]), ty(s.Y[j])))
		}
		fmt.Fprintf(&b, `<polyline class="ln %s" points="%s"/>`, cls, strings.Join(pts, " "))
		if len(s.X) <= 24 {
			for j := range s.X {
				fmt.Fprintf(&b, `<circle class="pt %s" cx="%.1f" cy="%.1f" r="3"><title>%s: %s, %s</title></circle>`,
					cls, tx(s.X[j]), ty(s.Y[j]), template.HTMLEscapeString(s.Name), num(s.X[j]), num(s.Y[j]))
			}
		}
	}
	b.WriteString(`</svg>`)
	b.WriteString(legend(names(ss)))
	return template.HTML(b.String())
}

// speedupBars draws a diverging bar chart centred on 1.0 (log scale), one
// group of bars per label.
func speedupBars(groups []string, cats []string, val func(cat, group string) (float64, bool), T tfn) template.HTML {
	if len(cats) == 0 || len(groups) == 0 {
		return ""
	}
	rowH := 18.0
	h := padT + float64(len(cats)*len(groups))*rowH + float64(len(cats))*8 + 10
	labelW := 130.0
	mid := labelW + (cw-labelW-60)/2
	half := (cw - labelW - 60) / 2
	maxLog := math.Log(2) // ±2x spans the half width
	for _, c := range cats {
		for _, g := range groups {
			if v, ok := val(c, g); ok && v > 0 {
				maxLog = math.Max(maxLog, math.Abs(math.Log(v)))
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart" viewBox="0 0 %.0f %.0f" role="img"><title>%s</title>`, cw, h, template.HTMLEscapeString(T("chart.speedup")))
	fmt.Fprintf(&b, `<line class="axis" x1="%.1f" x2="%.1f" y1="%.0f" y2="%.1f"/>`, mid, mid, padT-8, h-6)
	fmt.Fprintf(&b, `<text class="ax" x="%.1f" y="14" text-anchor="middle">%s</text>`, mid, template.HTMLEscapeString(T("chart.baseline")))
	fmt.Fprintf(&b, `<text class="ax" x="%.1f" y="14" text-anchor="end">%s</text><text class="ax" x="%.1f" y="14">%s</text>`, mid-12, template.HTMLEscapeString(T("chart.slower")), mid+12, template.HTMLEscapeString(T("chart.faster")))
	y := padT
	for _, c := range cats {
		fmt.Fprintf(&b, `<text class="cl" x="0" y="%.1f">%s</text>`, y+rowH*float64(len(groups))/2+4, template.HTMLEscapeString(c))
		for gi, g := range groups {
			v, ok := val(c, g)
			if ok && v > 0 {
				w := math.Log(v) / maxLog * half
				x := mid
				cls := "good"
				if w < 0 {
					x, w, cls = mid+w, -w, "bad"
				}
				fmt.Fprintf(&b, `<rect class="%s g%d" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2"><title>%s %s: %s</title></rect>`,
					cls, gi%maxSeriesPalette, x, y+2, math.Max(w, 1), rowH-5, template.HTMLEscapeString(g), template.HTMLEscapeString(c), pctChange(v))
				tx, anchor := mid+math.Log(v)/maxLog*half+4, "start"
				if v < 1 {
					tx, anchor = mid+math.Log(v)/maxLog*half-4, "end"
				}
				fmt.Fprintf(&b, `<text class="ax" x="%.1f" y="%.1f" text-anchor="%s">%s</text>`, tx, y+rowH-5, anchor, pctChange(v))
			}
			y += rowH
		}
		y += 8
	}
	b.WriteString(`</svg>`)
	if len(groups) > 1 {
		b.WriteString(legend(groups))
	}
	return template.HTML(b.String())
}

func legend(ns []string) string {
	if len(ns) < 2 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="legend">`)
	for i, n := range ns {
		fmt.Fprintf(&b, `<span><i class="sw s%d"></i>%s</span>`, i%maxSeriesPalette, template.HTMLEscapeString(n))
	}
	b.WriteString(`</div>`)
	return b.String()
}

func names(ss []series) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Name)
	}
	return out
}

func minmax(v []float64) (float64, float64) {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, x := range v {
		lo, hi = math.Min(lo, x), math.Max(hi, x)
	}
	return lo, hi
}

func niceCeil(v float64) float64 {
	if v <= 0 {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 1.2, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10} {
		if m*p >= v {
			return m * p
		}
	}
	return 10 * p
}

func uniqueSorted(v []float64) []float64 {
	m := map[float64]bool{}
	var out []float64
	for _, x := range v {
		if !m[x] {
			m[x] = true
			out = append(out, x)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func spread(v []float64, n int) []float64 {
	var out []float64
	for i := range n {
		out = append(out, v[i*(len(v)-1)/(n-1)])
	}
	return out
}

func num(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1e6:
		return fmt.Sprintf("%.1fM", v/1e6)
	case a >= 1e4:
		return fmt.Sprintf("%.1fk", v/1e3)
	case a >= 100:
		return fmt.Sprintf("%.0f", v)
	case a >= 10:
		return fmt.Sprintf("%.1f", v)
	case a == 0:
		return "0"
	default:
		return fmt.Sprintf("%.2f", v)
	}
}

func pctChange(speedup float64) string {
	return fmt.Sprintf("%+.0f%%", (speedup-1)*100)
}
