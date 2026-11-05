package benchmark

import (
	"math"
	"slices"
)

type Stats struct {
	N      int     `json:"n"`
	Median float64 `json:"median"`
	Mean   float64 `json:"mean"`
	StdDev float64 `json:"stddev"`
	CV     float64 `json:"cv_pct"` // coefficient of variation, %
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

// NoisyCV is the CV% above which a result is flagged as unreliable.
const NoisyCV = 5.0

func (s Stats) Noisy() bool { return s.CV > NoisyCV }

func Summarize(samples []float64) Stats {
	n := len(samples)
	if n == 0 {
		return Stats{}
	}
	sorted := slices.Clone(samples)
	slices.Sort(sorted)
	var sum float64
	for _, v := range sorted {
		sum += v
	}
	mean := sum / float64(n)
	var sq float64
	for _, v := range sorted {
		sq += (v - mean) * (v - mean)
	}
	sd := 0.0
	if n > 1 {
		sd = math.Sqrt(sq / float64(n-1))
	}
	cv := 0.0
	if mean != 0 {
		cv = sd / math.Abs(mean) * 100
	}
	return Stats{N: n, Median: Median(sorted), Mean: mean, StdDev: sd, CV: cv, Min: sorted[0], Max: sorted[n-1]}
}

// Median of an already sorted slice.
func Median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// GeoMean of positive values; non-positive values are skipped.
func GeoMean(vals []float64) float64 {
	var s float64
	var n int
	for _, v := range vals {
		if v > 0 {
			s += math.Log(v)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return math.Exp(s / float64(n))
}
