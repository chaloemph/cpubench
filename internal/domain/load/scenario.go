package load

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/chaloemph/cpubench/internal/domain/result"
)

type Profile string

const (
	Ramp     Profile = "ramp"     // step up until p99 breaks the SLO → max RPS @ SLO
	Constant Profile = "constant" // fixed rate (soak when long)
	Stress   Profile = "stress"   // overload, then measure recovery
	Spike    Profile = "spike"    // base → sudden peak → base
)

type Scenario struct {
	Target      string        `yaml:"target"`
	Profile     Profile       `yaml:"profile"`
	SLO         result.SLO    `yaml:"slo"`
	Concurrency int           `yaml:"concurrency"`
	KeepAlive   *bool         `yaml:"keepalive"`
	Warmup      time.Duration `yaml:"warmup"`
	Ramp        RampSpec      `yaml:"ramp"`
	Rate        float64       `yaml:"rate"`     // constant/stress/spike base rate
	Duration    time.Duration `yaml:"duration"` // constant/stress phase length
	Spike       SpikeSpec     `yaml:"spike"`
	Requests    []RequestSpec `yaml:"requests"`
	Name        string        `yaml:"name"`
}

type RampSpec struct {
	Start        float64       `yaml:"start"`
	Step         float64       `yaml:"step"`
	Max          float64       `yaml:"max"`
	StepDuration time.Duration `yaml:"step_duration"`
	// StopAfterFails ends the ramp after this many consecutive SLO failures.
	StopAfterFails int `yaml:"stop_after_fails"`
	// Adaptive doubles the rate from Start until the SLO fails, then refines
	// between the last pass and the failure: far fewer steps on big servers.
	Adaptive bool `yaml:"adaptive"`
}

type SpikeSpec struct {
	Peak   float64       `yaml:"peak"`
	Before time.Duration `yaml:"before"`
	Hold   time.Duration `yaml:"hold"`
	After  time.Duration `yaml:"after"`
}

type RequestSpec struct {
	Weight   int               `yaml:"weight"`
	Method   string            `yaml:"method"`
	Path     string            `yaml:"path"`
	Body     string            `yaml:"body"`
	BodyFile string            `yaml:"body_file"`
	Headers  map[string]string `yaml:"headers"`
}

// Request is one concrete HTTP request.
type Request struct {
	Method  string
	URL     string
	Body    []byte
	Headers map[string]string
}

// Phase is a span of constant arrival rate.
type Phase struct {
	Name     string        `json:"name"`
	Rate     float64       `json:"rate"`
	Duration time.Duration `json:"duration"`
}

// Defaults fills unset fields with values suitable for a web API.
func (s *Scenario) Defaults() {
	if s.Profile == "" {
		s.Profile = Ramp
	}
	if s.SLO.P99Ms == 0 {
		s.SLO.P99Ms = 100
	}
	if s.SLO.ErrorRate == 0 {
		s.SLO.ErrorRate = 0.001
	}
	if s.Concurrency == 0 {
		s.Concurrency = 512
	}
	if s.KeepAlive == nil {
		t := true
		s.KeepAlive = &t
	}
	if s.Ramp.Start == 0 {
		s.Ramp.Start = 500
	}
	if s.Ramp.Step == 0 {
		s.Ramp.Step = s.Ramp.Start
	}
	if s.Ramp.Max == 0 {
		s.Ramp.Max = 200_000
	}
	if s.Ramp.StepDuration == 0 {
		s.Ramp.StepDuration = 20 * time.Second
	}
	if s.Ramp.StopAfterFails == 0 {
		s.Ramp.StopAfterFails = 2
	}
	if s.Duration == 0 {
		s.Duration = time.Minute
	}
	if s.Spike.Before == 0 {
		s.Spike.Before = 30 * time.Second
	}
	if s.Spike.Hold == 0 {
		s.Spike.Hold = 30 * time.Second
	}
	if s.Spike.After == 0 {
		s.Spike.After = 60 * time.Second
	}
	if s.Spike.Peak == 0 {
		s.Spike.Peak = 3 * s.Rate
	}
	for i := range s.Requests {
		if s.Requests[i].Weight == 0 {
			s.Requests[i].Weight = 1
		}
		if s.Requests[i].Method == "" {
			s.Requests[i].Method = "GET"
		}
	}
}

func (s *Scenario) Validate() error {
	if s.Target == "" {
		return fmt.Errorf("scenario: target is required")
	}
	if len(s.Requests) == 0 {
		return fmt.Errorf("scenario: at least one request is required")
	}
	switch s.Profile {
	case Ramp:
	case Constant, Stress, Spike:
		if s.Rate <= 0 {
			return fmt.Errorf("scenario: profile %s needs rate > 0", s.Profile)
		}
	default:
		return fmt.Errorf("scenario: unknown profile %q", s.Profile)
	}
	return nil
}

// Phases lays out the arrival-rate schedule for non-ramp profiles. Ramp is
// adaptive (it stops after the knee), so the app drives it step by step.
func (s *Scenario) Phases() []Phase {
	switch s.Profile {
	case Constant:
		return []Phase{{Name: "constant", Rate: s.Rate, Duration: s.Duration}}
	case Stress:
		return []Phase{
			{Name: "overload", Rate: s.Rate, Duration: s.Duration},
			{Name: "recovery", Rate: s.Rate / 3, Duration: max(s.Duration/2, 30*time.Second)},
		}
	case Spike:
		return []Phase{
			{Name: "before", Rate: s.Rate, Duration: s.Spike.Before},
			{Name: "spike", Rate: s.Spike.Peak, Duration: s.Spike.Hold},
			{Name: "after", Rate: s.Rate, Duration: s.Spike.After},
		}
	}
	return nil
}

// RampRates lists the ramp's target rates.
func (s *Scenario) RampRates() []float64 {
	var out []float64
	for r := s.Ramp.Start; r <= s.Ramp.Max; r += s.Ramp.Step {
		out = append(out, r)
	}
	return out
}

// Builder turns RequestSpecs into concrete requests with weighted choice and
// {{rand a b}} substitution. Not safe for concurrent use: one per worker.
type Builder struct {
	target string
	specs  []RequestSpec
	bodies [][]byte
	total  int
	rng    *rand.Rand
}

// NewBuilder expects bodies[i] to be the resolved body of specs[i] (BodyFile
// already read by an adapter).
func NewBuilder(target string, specs []RequestSpec, bodies [][]byte, seed uint64) *Builder {
	b := &Builder{target: strings.TrimRight(target, "/"), specs: specs, bodies: bodies, rng: rand.New(rand.NewPCG(seed, 99))}
	for _, s := range specs {
		b.total += s.Weight
	}
	return b
}

func (b *Builder) Next() Request {
	n := b.rng.IntN(b.total)
	i := 0
	for ; i < len(b.specs)-1; i++ {
		if n < b.specs[i].Weight {
			break
		}
		n -= b.specs[i].Weight
	}
	s := b.specs[i]
	body := b.bodies[i]
	if len(body) > 0 && strings.Contains(string(body), "{{") {
		body = []byte(b.expand(string(body)))
	}
	return Request{Method: s.Method, URL: b.target + b.expand(s.Path), Body: body, Headers: s.Headers}
}

// expand replaces {{rand a b}} with a uniform integer in [a, b].
func (b *Builder) expand(s string) string {
	for {
		i := strings.Index(s, "{{rand ")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], "}}")
		if j < 0 {
			return s
		}
		f := strings.Fields(s[i+7 : i+j])
		v := ""
		if len(f) == 2 {
			lo, e1 := strconv.Atoi(f[0])
			hi, e2 := strconv.Atoi(f[1])
			if e1 == nil && e2 == nil && hi >= lo {
				v = strconv.Itoa(lo + b.rng.IntN(hi-lo+1))
			}
		}
		s = s[:i] + v + s[i+j+2:]
	}
}
