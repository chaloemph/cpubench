package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/chaloemph/cpubench/internal/domain/load"
)

func TestExampleScenariosParse(t *testing.T) {
	paths, _ := filepath.Glob("../../../../examples/scenario-*.yaml")
	if len(paths) < 3 {
		t.Fatalf("found %d scenarios", len(paths))
	}
	for _, p := range paths {
		b, _ := os.ReadFile(p)
		var sc load.Scenario
		if err := yaml.Unmarshal(b, &sc); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if sc.SLO.P99Ms == 0 || len(sc.Requests) == 0 {
			t.Errorf("%s: slo or requests not parsed: %+v", p, sc)
		}
		if sc.Target == "" {
			sc.Target = "http://x"
		}
		sc.Defaults()
		if err := sc.Validate(); err != nil {
			t.Errorf("%s: %v", p, err)
		}
		if _, err := resolveBodies(sc.Requests, filepath.Dir(p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	b, _ := os.ReadFile("../../../../examples/scenario-webapp.yaml")
	var sc load.Scenario
	yaml.Unmarshal(b, &sc)
	if sc.Ramp.StepDuration != 30*time.Second || sc.Warmup != 30*time.Second || sc.Ramp.Max != 100000 {
		t.Errorf("durations/ramp not parsed: %+v", sc.Ramp)
	}
}

func TestExampleTCO(t *testing.T) {
	in, err := loadTCO("../../../../examples/tco.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c := in.Servers["arm-01"]
	if in.TargetRPS != 20000 || in.Redundancy != 1 || c.ServerPrice == 0 || c.PUE != 1.5 || c.Currency != "THB" {
		t.Fatalf("%+v %+v", in, c)
	}
}
