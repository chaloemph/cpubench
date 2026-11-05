package power

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRedfishPowerResource(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "admin" || p != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/redfish/v1/Chassis":
			w.Write([]byte(`{"Members":[{"@odata.id":"/redfish/v1/Chassis/Enc"},{"@odata.id":"/redfish/v1/Chassis/1"}]}`))
		case "/redfish/v1/Chassis/1/Power":
			w.Write([]byte(`{"PowerControl":[{"PowerConsumedWatts":312}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	m := &Redfish{Base: srv.URL, User: "admin", Pass: "secret"}
	w, err := m.Watts(context.Background())
	if err != nil || w != 312 {
		t.Fatalf("%v %v", w, err)
	}
}

func TestRedfishEnvironmentMetrics(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redfish/v1/Chassis":
			w.Write([]byte(`{"Members":[{"@odata.id":"/redfish/v1/Chassis/1"}]}`))
		case "/redfish/v1/Chassis/1/EnvironmentMetrics":
			w.Write([]byte(`{"PowerWatts":{"Reading":205.5}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	w, err := (&Redfish{Base: srv.URL}).Watts(context.Background())
	if err != nil || w != 205.5 {
		t.Fatalf("%v %v", w, err)
	}
}

func TestParseDCMI(t *testing.T) {
	out := `
    Instantaneous power reading:                   268 Watts
    Minimum during sampling period:                 92 Watts
`
	if w, err := ParseDCMI(out); err != nil || w != 268 {
		t.Fatalf("%v %v", w, err)
	}
}

func TestHwmon(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, "sys/class/hwmon/hwmon3")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "name"), []byte("acpi_power\n"), 0o644)
	os.WriteFile(filepath.Join(d, "power1_input"), []byte("150000000\n"), 0o644)
	w, err := (&Hwmon{Root: root, Name: "acpi_power"}).Watts(context.Background())
	if err != nil || w != 150 {
		t.Fatalf("%v %v", w, err)
	}
}

func TestOpen(t *testing.T) {
	t.Setenv(passwordEnv, "pw")
	m, err := Open("redfish://admin@10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	r := m.(*Redfish)
	if r.Base != "https://10.0.0.5" || r.User != "admin" || r.Pass != "pw" {
		t.Fatalf("%+v", r)
	}
	if c, _ := Open("const:99"); c.Source() != "const:99" {
		t.Fatal(c.Source())
	}
	if _, err := Open("bogus://x"); err == nil {
		t.Fatal("expected error")
	}
}
