package scan

import (
	"errors"
	"testing"
)

func TestJudgeImage(t *testing.T) {
	for _, target := range Arches {
		if JudgeImage("nginx", []string{"linux/amd64", "linux/arm64/v8"}, nil, target).Status != Ready {
			t.Fatalf("multi-arch should be ready for %s", target)
		}
		if JudgeImage("x", nil, errors.New("unauthorized"), target).Status != Check {
			t.Fatal("errors need a manual check")
		}
	}
	if JudgeImage("old", []string{"linux/amd64"}, nil, "arm64").Status != Blocked {
		t.Fatal("amd64-only should be blocked for arm64")
	}
	if JudgeImage("new", []string{"linux/arm64"}, nil, "amd64").Status != Blocked {
		t.Fatal("arm64-only should be blocked for amd64")
	}
	if img := JudgeImage("new", []string{"linux/arm64"}, nil, "arm64"); img.Status != Ready || img.Note != "" {
		t.Fatalf("arm64-only should be ready for arm64: %+v", img)
	}
}

func TestJudgeBinary(t *testing.T) {
	cases := []struct {
		arch, target string
		want         Status
	}{
		{"amd64", "amd64", Ready}, {"amd64", "arm64", Blocked},
		{"arm64", "arm64", Ready}, {"arm64", "amd64", Blocked},
		{"multi", "amd64", Ready}, {"multi", "arm64", Ready},
		{"i386", "amd64", Check}, {"i386", "arm64", Blocked},
		{"arm", "arm64", Check}, {"arm", "amd64", Blocked},
		{"unknown", "arm64", Check},
	}
	for _, c := range cases {
		if got := JudgeBinary(Binary{Arch: c.arch}, c.target).Status; got != c.want {
			t.Errorf("%s on %s: %s want %s", c.arch, c.target, got, c.want)
		}
	}
	if ValidArch("riscv64") == nil || ValidArch("arm64") != nil {
		t.Fatal("ValidArch")
	}
}

func TestArchFromName(t *testing.T) {
	cases := map[string]string{
		"numpy-2.1.0-cp312-cp312-manylinux_2_17_aarch64.whl": "arm64",
		"orjson-3.10-cp312-cp312-manylinux_2_17_x86_64.whl":  "amd64",
		"requests-2.32.3-py3-none-any.whl":                   "multi",
		"lib/linux-x64/libsqlite.so":                         "amd64",
		"README.md":                                          "unknown",
	}
	for in, want := range cases {
		if got := ArchFromName(in); got != want {
			t.Errorf("%s: %s want %s", in, got, want)
		}
	}
}
