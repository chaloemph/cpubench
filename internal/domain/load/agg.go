package load

import (
	"time"

	"github.com/chaloemph/cpubench/internal/domain/result"
)

// Agg is the raw outcome of a span of load: mergeable across agents.
type Agg struct {
	Hist     *Histogram    `json:"hist"`
	Requests int64         `json:"requests"`
	Errors   int64         `json:"errors"`
	Dropped  int64         `json:"dropped"`
	Elapsed  time.Duration `json:"elapsed"`
}

func NewAgg() *Agg { return &Agg{Hist: NewHistogram()} }

func (a *Agg) Merge(o *Agg) {
	if o == nil {
		return
	}
	a.Hist.Merge(o.Hist)
	a.Requests += o.Requests
	a.Errors += o.Errors
	a.Dropped += o.Dropped
	a.Elapsed = max(a.Elapsed, o.Elapsed)
}

func (a *Agg) Compact() *Agg {
	a.Hist.Compact()
	return a
}

// Step summarises an Agg into a report row and judges it against the SLO.
// Dropped requests (never sent because the generator fell too far behind)
// count as errors: the system did not serve the offered load.
func (a *Agg) Step(target float64, slo result.SLO) result.WebStep {
	s := result.WebStep{TargetRPS: target, Requests: a.Requests, Errors: a.Errors, Dropped: a.Dropped,
		P50Ms: Ms(a.Hist.Quantile(0.50)), P90Ms: Ms(a.Hist.Quantile(0.90)), P99Ms: Ms(a.Hist.Quantile(0.99)),
		P999Ms: Ms(a.Hist.Quantile(0.999)), MaxMs: float64(a.Hist.MaxNs) / 1e6}
	if a.Elapsed > 0 {
		s.AchievedRPS = float64(a.Requests-a.Errors) / a.Elapsed.Seconds()
	}
	offered := a.Requests + a.Dropped
	errRate := 0.0
	if offered > 0 {
		errRate = float64(a.Errors+a.Dropped) / float64(offered)
	}
	s.PassSLO = offered > 0 && s.P99Ms <= slo.P99Ms && errRate <= slo.ErrorRate && s.AchievedRPS >= 0.95*target
	return s
}

// Knee is the highest achieved RPS among steps that met the SLO.
func Knee(steps []result.WebStep) float64 {
	var k float64
	for _, s := range steps {
		if s.PassSLO {
			k = max(k, s.AchievedRPS)
		}
	}
	return k
}

// Recovery returns seconds from the end of the disturbance (at offset
// fromSec) until the start of three consecutive windows back within the SLO,
// or -1. Timeline points are stamped with their window's end time.
func Recovery(tl []result.WebSecond, fromSec float64, windowSec float64, slo result.SLO) float64 {
	ok := 0
	for _, w := range tl {
		if w.T-windowSec < fromSec-1e-9 {
			continue
		}
		if w.P99Ms <= slo.P99Ms && w.Errors == 0 {
			ok++
			if ok == 3 {
				return w.T - 3*windowSec - fromSec
			}
		} else {
			ok = 0
		}
	}
	return -1
}
