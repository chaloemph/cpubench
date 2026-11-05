package hardware

import (
	"slices"
	"testing"
)

func TestDescribeMIDR(t *testing.T) {
	v, m := DescribeMIDR(MIDR{Implementer: "0x48", Part: "0xd01"})
	if v != "HiSilicon" || m != "TaiShan-v110 (Kunpeng 920)" {
		t.Fatalf("got %q %q", v, m)
	}
	v, m = DescribeMIDR(MIDR{Implementer: "0x48", Part: "0xd03"})
	if v != "HiSilicon" || m != "HiSilicon HIP10 (ARMv9)" {
		t.Fatalf("arm64 part: got %q %q", v, m)
	}
	v, m = DescribeMIDR(MIDR{Implementer: "0x48", Part: "0xdff"})
	if v != "HiSilicon" || m != "HiSilicon part 0xdff" {
		t.Fatalf("unknown part should be hex, got %q %q", v, m)
	}
}

func TestEffectiveCPUs(t *testing.T) {
	cases := []struct {
		allowed int
		cg      *Cgroup
		want    int
	}{
		{8, nil, 8},
		{8, &Cgroup{CPUQuotaCores: 2.5}, 3},
		{8, &Cgroup{Cpuset: []int{0, 1}}, 2},
		{8, &Cgroup{CPUQuotaCores: 0.2}, 1},
	}
	for _, c := range cases {
		if got := EffectiveCPUs(c.allowed, c.cg); got != c.want {
			t.Errorf("EffectiveCPUs(%d,%+v)=%d want %d", c.allowed, c.cg, got, c.want)
		}
	}
}

func TestPlacementOrderSpreadsCoresBeforeSiblings(t *testing.T) {
	// 2 NUMA nodes, 2 cores each, SMT2: core siblings (0,4) (1,5) (2,6) (3,7)
	top := Topology{
		NUMANodes:   []NUMANode{{ID: 0, CPUs: []int{0, 1, 4, 5}}, {ID: 1, CPUs: []int{2, 3, 6, 7}}},
		CoreGroups:  [][]int{{0, 4}, {1, 5}, {2, 6}, {3, 7}},
		AllowedCPUs: []int{0, 1, 2, 3, 4, 5, 6, 7},
	}
	got := PlacementOrder(top)
	want := []int{0, 1, 2, 3, 4, 5, 6, 7}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if n1 := CPUsOnNode(top, 1); !slices.Equal(n1, []int{2, 3, 6, 7}) {
		t.Fatalf("node1 %v", n1)
	}
}

func TestPlacementOrderRespectsAffinity(t *testing.T) {
	top := Topology{
		CoreGroups:  [][]int{{0, 1}, {2, 3}},
		AllowedCPUs: []int{1, 2},
	}
	if got := PlacementOrder(top); !slices.Equal(got, []int{2, 1}) {
		t.Fatalf("got %v", got)
	}
}
