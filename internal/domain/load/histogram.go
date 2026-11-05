// Package load models HTTP load tests: latency histograms, scenarios, the
// phase plan for each profile and how results are judged against an SLO.
package load

import (
	"math"
	"math/bits"
	"time"
)

// Histogram is a log-linear latency histogram in nanoseconds with <1%
// relative error (HDR-style: 64 linear sub-buckets per power of two).
// It is mergeable, which is what lets several load agents be combined.
type Histogram struct {
	Counts []uint64 `json:"-"`
	Total  uint64   `json:"total"`
	MaxNs  int64    `json:"max_ns"`
	// Sparse is the JSON form: bucket index → count.
	Sparse map[int]uint64 `json:"buckets,omitempty"`
}

const (
	subBits  = 7
	subCount = 1 << subBits       // 128 exact buckets for 0..127ns
	half     = subCount / 2       // 64 sub-buckets per octave above that
	nBuckets = subCount + 40*half // covers > 1e12 ns
)

func NewHistogram() *Histogram { return &Histogram{Counts: make([]uint64, nBuckets)} }

func bucketOf(v int64) int {
	if v < subCount {
		return int(max(v, 0))
	}
	shift := bits.Len64(uint64(v)) - subBits
	top := int(uint64(v) >> shift) // in [half, subCount)
	return subCount + (shift-1)*half + (top - half)
}

// lowerBound and upperBound of a bucket in ns.
func bucketRange(i int) (lo, hi int64) {
	if i < subCount {
		return int64(i), int64(i)
	}
	k := i - subCount
	shift := k/half + 1
	top := int64(k%half + half)
	return top << shift, (top+1)<<shift - 1
}

// BucketOf returns the bucket index for a latency.
func BucketOf(d time.Duration) int { return min(bucketOf(d.Nanoseconds()), nBuckets-1) }

func (h *Histogram) Record(d time.Duration) {
	ns := d.Nanoseconds()
	i := min(bucketOf(ns), nBuckets-1)
	h.Counts[i]++
	h.Total++
	h.MaxNs = max(h.MaxNs, ns)
}

func (h *Histogram) Merge(o *Histogram) {
	if o == nil {
		return
	}
	o.expand()
	for i, c := range o.Counts {
		h.Counts[i] += c
	}
	h.Total += o.Total
	h.MaxNs = max(h.MaxNs, o.MaxNs)
}

// Quantile returns the latency at q in [0,1] as the bucket midpoint.
func (h *Histogram) Quantile(q float64) time.Duration {
	if h.Total == 0 {
		return 0
	}
	rank := uint64(math.Ceil(q * float64(h.Total)))
	rank = max(rank, 1)
	var seen uint64
	for i, c := range h.Counts {
		seen += c
		if seen >= rank {
			lo, hi := bucketRange(i)
			return time.Duration(min((lo+hi)/2, h.MaxNs))
		}
	}
	return time.Duration(h.MaxNs)
}

// Compact fills Sparse for JSON transport.
func (h *Histogram) Compact() *Histogram {
	h.Sparse = map[int]uint64{}
	for i, c := range h.Counts {
		if c > 0 {
			h.Sparse[i] = c
		}
	}
	return h
}

// expand rebuilds Counts after JSON decoding.
func (h *Histogram) expand() {
	if len(h.Counts) == nBuckets {
		return
	}
	h.Counts = make([]uint64, nBuckets)
	for i, c := range h.Sparse {
		if i >= 0 && i < nBuckets {
			h.Counts[i] = c
		}
	}
}

func Ms(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }
