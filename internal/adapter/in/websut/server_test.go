package websut

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chaloemph/cpubench/internal/adapter/out/httpload"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
	"github.com/chaloemph/cpubench/internal/domain/load"
)

type probe struct{}

func (probe) System(context.Context) (hardware.System, error) {
	return hardware.System{Hostname: "sut-1"}, nil
}

func TestEndpoints(t *testing.T) {
	srv := httptest.NewServer((&Server{Probe: probe{}}).Handler())
	defer srv.Close()
	tg := httpload.NewTarget(4, true)
	cases := []load.Request{
		{Method: "GET", URL: srv.URL + "/plaintext"},
		{Method: "GET", URL: srv.URL + "/json?id=7"},
		{Method: "POST", URL: srv.URL + "/json/parse", Body: []byte(`[{"price":2,"stock":3}]`)},
		{Method: "GET", URL: srv.URL + "/html"},
		{Method: "GET", URL: srv.URL + "/gzip"},
		{Method: "POST", URL: srv.URL + "/login", Body: []byte("user=1"), Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}},
		{Method: "GET", URL: srv.URL + "/cache?key=5&write=1"},
	}
	for _, c := range cases {
		if code, err := tg.Do(context.Background(), c); err != nil || code != 200 {
			t.Errorf("%s %s: %d %v", c.Method, c.URL, code, err)
		}
	}
	if code, _ := tg.Do(context.Background(), load.Request{Method: "POST", URL: srv.URL + "/json/parse", Body: []byte("nope")}); code != 400 {
		t.Errorf("bad json: %d", code)
	}

	sut := httpload.NewSUT(srv.URL)
	cpu, n, err := sut.CPUSeconds(context.Background())
	if err != nil || cpu <= 0 || n <= 0 {
		t.Fatalf("stats %v %v %v", cpu, n, err)
	}
	sys, err := sut.System(context.Background())
	if err != nil || sys.Hostname != "sut-1" {
		t.Fatalf("info %+v %v", sys, err)
	}
}

func TestJSONShape(t *testing.T) {
	srv := httptest.NewServer((&Server{Probe: probe{}}).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/json?id=42")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	var p product
	if err := json.Unmarshal(b, &p); err != nil || p.ID != 42 || !strings.HasPrefix(p.SKU, "SKU-") {
		t.Fatalf("%s %v", b, err)
	}
	if len(b) < 400 {
		t.Fatalf("response too small for a realistic API payload: %d bytes", len(b))
	}
}

func TestSelfSigned(t *testing.T) {
	for _, k := range []string{"ecdsa", "rsa"} {
		if _, err := selfSigned(k); err != nil {
			t.Fatal(k, err)
		}
	}
	if _, err := selfSigned("dsa"); err == nil {
		t.Fatal("expected error")
	}
}
