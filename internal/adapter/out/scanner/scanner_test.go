package scanner

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseManifest(t *testing.T) {
	raw := []byte(`{"manifests":[{"platform":{"os":"linux","architecture":"amd64"}},
		{"platform":{"os":"linux","architecture":"arm64","variant":"v8"}},
		{"platform":{"os":"unknown","architecture":"unknown"}}]}`)
	p, err := ParseManifest(raw)
	if err != nil || len(p) != 2 || p[1] != "linux/arm64/v8" {
		t.Fatalf("%v %v", p, err)
	}
}

func TestFilesScan(t *testing.T) {
	dir := t.TempDir()
	exe, _ := os.Executable() // the test binary itself is an ELF for this machine
	b, _ := os.ReadFile(exe)
	os.WriteFile(filepath.Join(dir, "app"), b, 0o755)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o644)
	jar, _ := os.Create(filepath.Join(dir, "sqlite-jdbc.jar"))
	zw := zip.NewWriter(jar)
	w, _ := zw.Create("org/sqlite/native/Linux/x86_64/libsqlitejdbc.so")
	w.Write([]byte("x"))
	zw.Close()
	jar.Close()

	got, err := Files{}.Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, b := range got {
		byPath[b.Path] = b.Kind + "/" + b.Arch
	}
	if byPath["app"] != "go-binary/"+runtime.GOARCH {
		t.Errorf("app: %q", byPath["app"])
	}
	if byPath["sqlite-jdbc.jar"] != "jar-native/amd64" {
		t.Errorf("jar: %q", byPath["sqlite-jdbc.jar"])
	}
	if _, ok := byPath["notes.txt"]; ok || len(got) != 2 {
		t.Errorf("unexpected entries %v", byPath)
	}
}
