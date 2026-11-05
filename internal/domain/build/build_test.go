package build

import (
	"runtime/debug"
	"testing"

	"github.com/chaloemph/cpubench/internal/domain/doctor"
)

func ids(fs []doctor.Finding) map[string]doctor.Severity {
	m := map[string]doctor.Severity{}
	for _, f := range fs {
		m[f.ID] = f.Severity
	}
	return m
}

func TestFromBuildInfo(t *testing.T) {
	bi := &debug.BuildInfo{GoVersion: "go1.26.3", Settings: []debug.BuildSetting{
		{Key: "CGO_ENABLED", Value: "0"}, {Key: "GOARCH", Value: "arm64"}, {Key: "GOARM64", Value: "v8.0"},
		{Key: "vcs.revision", Value: "c2ac45a14753ea8ddcd9e2c89f2c0a857da218b8"}, {Key: "vcs.modified", Value: "true"},
	}}
	got := FromBuildInfo(bi, "v1.2.0")
	want := Info{Version: "v1.2.0", Commit: "c2ac45a14753ea8ddcd9e2c89f2c0a857da218b8", Modified: true,
		GoVersion: "go1.26.3", GOARCH: "arm64", ArchLevel: "GOARM64=v8.0"}
	if got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if !got.BaselineLevel() || got.ShortCommit() != "c2ac45a14753" {
		t.Fatalf("baseline %v short %q", got.BaselineLevel(), got.ShortCommit())
	}
	if ids(Self(got))["build-modified"] != doctor.Warn {
		t.Fatal("modified build not flagged")
	}
	if FromBuildInfo(nil, "dev") != (Info{Version: "dev"}) {
		t.Fatal("nil build info")
	}
}

func TestComparable(t *testing.T) {
	x86 := Info{Version: "v1", Commit: "aaa", GoVersion: "go1.26.3", GOARCH: "amd64", ArchLevel: "GOAMD64=v1"}
	arm := Info{Version: "v1", Commit: "aaa", GoVersion: "go1.26.3", GOARCH: "arm64", ArchLevel: "GOARM64=v8.0"}
	if fs := Comparable(x86, arm); len(fs) != 0 {
		t.Fatalf("same source, default levels: %v", fs)
	}

	v3 := x86
	v3.ArchLevel = "GOAMD64=v3"
	if ids(Comparable(v3, arm))["build-level"] != doctor.Warn {
		t.Fatal("non-default level across arches not flagged")
	}
	if ids(Comparable(x86, v3))["build-level"] != doctor.Warn {
		t.Fatal("different level on the same arch not flagged")
	}

	other := arm
	other.Commit = "bbb"
	if ids(Comparable(x86, other))["build-version"] != doctor.Warn {
		t.Fatal("different commit not flagged")
	}
	other.Version = "v2"
	if ids(Comparable(x86, other))["build-version"] != doctor.Warn {
		t.Fatal("different version not flagged")
	}

	pure := arm
	pure.Tags, pure.CGO = "purego", true
	got := ids(Comparable(x86, pure))
	if got["build-tags"] != doctor.Warn || got["build-cgo"] != doctor.Info {
		t.Fatalf("%v", got)
	}
}
