package benchmark

import (
	"math"
	"testing"
)

func TestSummarize(t *testing.T) {
	s := Summarize([]float64{10, 12, 11, 9, 8})
	if s.Median != 10 || s.Min != 8 || s.Max != 12 || s.N != 5 {
		t.Fatalf("%+v", s)
	}
	if math.Abs(s.Mean-10) > 1e-9 || math.Abs(s.StdDev-math.Sqrt(2.5)) > 1e-9 {
		t.Fatalf("%+v", s)
	}
	if s.Noisy() != true { // cv ≈ 15.8%
		t.Fatalf("expected noisy, cv=%v", s.CV)
	}
}

func TestGeoMean(t *testing.T) {
	if g := GeoMean([]float64{2, 8}); math.Abs(g-4) > 1e-9 {
		t.Fatal(g)
	}
	if g := GeoMean(nil); g != 0 {
		t.Fatal(g)
	}
}

func TestKey(t *testing.T) {
	if k := KeyOf("int/sort", 128); k != "int/sort@128" {
		t.Fatal(k)
	}
}
