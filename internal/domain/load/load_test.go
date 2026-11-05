package load

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/result"
)

func TestHistogramQuantiles(t *testing.T) {
	h := NewHistogram()
	for i := 1; i <= 1000; i++ {
		h.Record(time.Duration(i) * time.Millisecond)
	}
	for _, c := range []struct {
		q    float64
		want float64
	}{{0.5, 500}, {0.99, 990}, {0.999, 999}} {
		got := Ms(h.Quantile(c.q))
		if math.Abs(got-c.want)/c.want > 0.01 {
			t.Errorf("q%.3f = %.2fms want %.0f", c.q, got, c.want)
		}
	}
	if h.Quantile(1) != time.Second {
		t.Errorf("max %v", h.Quantile(1))
	}
}

func TestBucketsAreContiguous(t *testing.T) {
	prevHi := int64(-1)
	for i := 0; i < nBuckets; i++ {
		lo, hi := bucketRange(i)
		if lo != prevHi+1 {
			t.Fatalf("bucket %d starts at %d, previous ended %d", i, lo, prevHi)
		}
		if bucketOf(lo) != i || bucketOf(hi) != i {
			t.Fatalf("bucket %d [%d,%d] maps to %d/%d", i, lo, hi, bucketOf(lo), bucketOf(hi))
		}
		prevHi = hi
		if hi > 1e12 {
			break
		}
	}
}

func TestHistogramMergeJSON(t *testing.T) {
	a, b := NewHistogram(), NewHistogram()
	a.Record(time.Millisecond)
	b.Record(3 * time.Millisecond)
	raw, _ := json.Marshal(b.Compact())
	var back Histogram
	json.Unmarshal(raw, &back)
	a.Merge(&back)
	if a.Total != 2 || a.MaxNs != 3e6 || Ms(a.Quantile(1)) < 2.9 {
		t.Fatalf("%+v", a)
	}
}

func TestStepAndKnee(t *testing.T) {
	slo := result.SLO{P99Ms: 50, ErrorRate: 0.01}
	ok := NewAgg()
	for range 1000 {
		ok.Hist.Record(10 * time.Millisecond)
	}
	ok.Requests, ok.Elapsed = 1000, time.Second
	s1 := ok.Step(1000, slo)
	slow := NewAgg()
	for range 1000 {
		slow.Hist.Record(80 * time.Millisecond)
	}
	slow.Requests, slow.Elapsed = 1000, time.Second
	s2 := slow.Step(1000, slo)
	if !s1.PassSLO || s2.PassSLO {
		t.Fatalf("%v %v", s1.PassSLO, s2.PassSLO)
	}
	if k := Knee([]result.WebStep{s1, s2}); k != 1000 {
		t.Fatal(k)
	}
	dropped := *ok
	dropped.Dropped = 500
	if dropped.Step(1000, slo).PassSLO {
		t.Fatal("dropped requests must fail the SLO")
	}
}

func TestBuilder(t *testing.T) {
	specs := []RequestSpec{{Weight: 1, Method: "GET", Path: "/item/{{rand 5 5}}"}, {Weight: 3, Method: "POST", Path: "/x"}}
	b := NewBuilder("http://h:1/", specs, [][]byte{nil, []byte(`{"id":{{rand 1 1}}}`)}, 1)
	gets := 0
	for range 4000 {
		r := b.Next()
		if r.Method == "GET" {
			gets++
			if r.URL != "http://h:1/item/5" {
				t.Fatal(r.URL)
			}
		} else if string(r.Body) != `{"id":1}` {
			t.Fatal(string(r.Body))
		}
	}
	if gets < 800 || gets > 1200 {
		t.Fatalf("weights off: %d GETs of 4000", gets)
	}
}

func TestScenarioPhases(t *testing.T) {
	s := Scenario{Target: "http://x", Profile: Spike, Rate: 100, Requests: []RequestSpec{{Path: "/"}}}
	s.Defaults()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	p := s.Phases()
	if len(p) != 3 || p[1].Rate != 300 {
		t.Fatalf("%+v", p)
	}
	r := Scenario{Target: "http://x", Requests: []RequestSpec{{Path: "/"}}, Ramp: RampSpec{Start: 100, Step: 100, Max: 300}}
	r.Defaults()
	if got := r.RampRates(); len(got) != 3 || got[2] != 300 {
		t.Fatal(got)
	}
	bad := Scenario{Target: "x", Profile: "nope", Requests: []RequestSpec{{}}}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Fatal(err)
	}
}

func TestRecovery(t *testing.T) {
	slo := result.SLO{P99Ms: 50}
	tl := []result.WebSecond{{T: 1, P99Ms: 10}, {T: 2, P99Ms: 500}, {T: 3, P99Ms: 300}, {T: 4, P99Ms: 20}, {T: 5, P99Ms: 20}, {T: 6, P99Ms: 20}}
	if r := Recovery(tl, 2, 1, slo); r != 1 {
		t.Fatal(r)
	}
}
