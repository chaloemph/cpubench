// Package power implements port.PowerMeter for BMCs (Redfish, IPMI) and
// local sensors (RAPL, hwmon).
//
// Specs:
//
//	redfish://user@bmc-host      password from $CPUBENCH_BMC_PASSWORD (or user:pass@)
//	ipmi://local                 in-band via /dev/ipmi0 (needs root)
//	ipmi://user@bmc-host         out-of-band lanplus, password from $CPUBENCH_BMC_PASSWORD
//	rapl                         Intel/AMD package energy counters (x86 only, CPU package power only)
//	hwmon[:name]                 sum of hwmon power*_input sensors
//	const:250                    fixed value, for dry runs
package power

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chaloemph/cpubench/internal/port"
)

const passwordEnv = "CPUBENCH_BMC_PASSWORD"

// Open parses a spec and returns a meter.
func Open(spec string) (port.PowerMeter, error) {
	switch {
	case spec == "":
		return nil, nil
	case strings.HasPrefix(spec, "const:"):
		w, err := strconv.ParseFloat(strings.TrimPrefix(spec, "const:"), 64)
		if err != nil {
			return nil, fmt.Errorf("power %q: %w", spec, err)
		}
		return Const(w), nil
	case spec == "rapl":
		return &RAPL{Root: "/"}, nil
	case spec == "hwmon" || strings.HasPrefix(spec, "hwmon:"):
		return &Hwmon{Root: "/", Name: strings.TrimPrefix(strings.TrimPrefix(spec, "hwmon"), ":")}, nil
	}
	u, err := url.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("power %q: %w", spec, err)
	}
	user := u.User.Username()
	pass, _ := u.User.Password()
	if pass == "" {
		pass = os.Getenv(passwordEnv)
	}
	switch u.Scheme {
	case "redfish":
		scheme := "https"
		if u.Query().Get("http") == "1" {
			scheme = "http"
		}
		return &Redfish{Base: scheme + "://" + u.Host, User: user, Pass: pass, Verify: u.Query().Get("verify") == "1"}, nil
	case "ipmi":
		return &IPMI{Host: u.Host, User: user, Pass: pass}, nil
	}
	return nil, fmt.Errorf("unknown power source %q", spec)
}

type Const float64

func (c Const) Source() string                         { return fmt.Sprintf("const:%g", float64(c)) }
func (c Const) Watts(context.Context) (float64, error) { return float64(c), nil }

// ---- Redfish ----

type Redfish struct {
	Base, User, Pass string
	Verify           bool

	once   sync.Once
	client *http.Client
	path   string // discovered Power or EnvironmentMetrics resource
	envM   bool
}

func (r *Redfish) Source() string { return "redfish " + r.Base }

func (r *Redfish) get(ctx context.Context, p string, v any) error {
	r.once.Do(func() {
		r.client = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: !r.Verify}}} // BMCs ship self-signed certs
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.Base+p, nil)
	if err != nil {
		return err
	}
	if r.User != "" {
		req.SetBasicAuth(r.User, r.Pass)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", p, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

type rfMembers struct {
	Members []struct {
		ID string `json:"@odata.id"`
	} `json:"Members"`
}

type rfPower struct {
	PowerControl []struct {
		PowerConsumedWatts *float64 `json:"PowerConsumedWatts"`
	} `json:"PowerControl"`
}

type rfEnv struct {
	PowerWatts struct {
		Reading *float64 `json:"Reading"`
	} `json:"PowerWatts"`
}

func (r *Redfish) Watts(ctx context.Context) (float64, error) {
	if r.path == "" {
		if err := r.discover(ctx); err != nil {
			return 0, err
		}
	}
	if r.envM {
		var e rfEnv
		if err := r.get(ctx, r.path, &e); err != nil || e.PowerWatts.Reading == nil {
			return 0, fmt.Errorf("redfish %s: no PowerWatts reading (%v)", r.path, err)
		}
		return *e.PowerWatts.Reading, nil
	}
	var p rfPower
	if err := r.get(ctx, r.path, &p); err != nil {
		return 0, err
	}
	for _, pc := range p.PowerControl {
		if pc.PowerConsumedWatts != nil {
			return *pc.PowerConsumedWatts, nil
		}
	}
	return 0, fmt.Errorf("redfish %s: no PowerConsumedWatts", r.path)
}

// discover finds the first chassis that reports power, trying the classic
// Power resource first and the newer EnvironmentMetrics second.
func (r *Redfish) discover(ctx context.Context) error {
	var ch rfMembers
	if err := r.get(ctx, "/redfish/v1/Chassis", &ch); err != nil {
		return fmt.Errorf("redfish: %w", err)
	}
	for _, m := range ch.Members {
		var p rfPower
		if err := r.get(ctx, m.ID+"/Power", &p); err == nil {
			for _, pc := range p.PowerControl {
				if pc.PowerConsumedWatts != nil {
					r.path = m.ID + "/Power"
					return nil
				}
			}
		}
		var e rfEnv
		if err := r.get(ctx, m.ID+"/EnvironmentMetrics", &e); err == nil && e.PowerWatts.Reading != nil {
			r.path, r.envM = m.ID+"/EnvironmentMetrics", true
			return nil
		}
	}
	return fmt.Errorf("redfish: no chassis reports power")
}

// ---- IPMI (ipmitool dcmi) ----

type IPMI struct{ Host, User, Pass string }

func (i *IPMI) Source() string { return "ipmi " + i.Host }

var dcmiRe = regexp.MustCompile(`Instantaneous power reading:\s+([0-9.]+)\s+Watts`)

func (i *IPMI) Watts(ctx context.Context) (float64, error) {
	args := []string{"dcmi", "power", "reading"}
	cmd := exec.CommandContext(ctx, "ipmitool", args...)
	if i.Host != "" && i.Host != "local" {
		cmd = exec.CommandContext(ctx, "ipmitool", append([]string{"-I", "lanplus", "-H", i.Host, "-U", i.User, "-E"}, args...)...)
		cmd.Env = append(os.Environ(), "IPMI_PASSWORD="+i.Pass) // -E keeps the password out of ps
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("ipmitool: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return ParseDCMI(string(out))
}

func ParseDCMI(out string) (float64, error) {
	m := dcmiRe.FindStringSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("ipmitool: no power reading in output")
	}
	return strconv.ParseFloat(m[1], 64)
}

// ---- RAPL ----

type RAPL struct {
	Root string

	mu     sync.Mutex
	last   float64
	lastAt time.Time
}

func (r *RAPL) Source() string { return "rapl (CPU package only)" }

func (r *RAPL) energyJ() (float64, error) {
	zones, _ := filepath.Glob(filepath.Join(r.Root, "sys/class/powercap/intel-rapl:[0-9]"))
	zones2, _ := filepath.Glob(filepath.Join(r.Root, "sys/class/powercap/intel-rapl:[0-9][0-9]"))
	zones = append(zones, zones2...)
	if len(zones) == 0 {
		return 0, fmt.Errorf("rapl: no powercap zones (x86 bare metal only; may need root)")
	}
	var total float64
	for _, z := range zones {
		b, err := os.ReadFile(filepath.Join(z, "energy_uj"))
		if err != nil {
			return 0, fmt.Errorf("rapl: %w", err)
		}
		uj, _ := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		total += uj / 1e6
	}
	return total, nil
}

func (r *RAPL) Watts(ctx context.Context) (float64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastAt.IsZero() {
		e, err := r.energyJ()
		if err != nil {
			return 0, err
		}
		r.last, r.lastAt = e, time.Now()
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	e, err := r.energyJ()
	if err != nil {
		return 0, err
	}
	now := time.Now()
	dt := now.Sub(r.lastAt).Seconds()
	de := e - r.last
	r.last, r.lastAt = e, now
	if de < 0 || dt <= 0 { // counter wrapped
		return 0, fmt.Errorf("rapl: counter wrapped, retry")
	}
	return de / dt, nil
}

// ---- hwmon ----

type Hwmon struct{ Root, Name string }

func (h *Hwmon) Source() string { return "hwmon " + h.Name }

func (h *Hwmon) Watts(context.Context) (float64, error) {
	devs, _ := filepath.Glob(filepath.Join(h.Root, "sys/class/hwmon/hwmon*"))
	var total float64
	var found bool
	for _, d := range devs {
		if h.Name != "" {
			if b, _ := os.ReadFile(filepath.Join(d, "name")); strings.TrimSpace(string(b)) != h.Name {
				continue
			}
		}
		inputs, _ := filepath.Glob(filepath.Join(d, "power*_input"))
		for _, in := range inputs {
			b, err := os.ReadFile(in)
			if err != nil {
				continue
			}
			uw, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
			if err == nil {
				total += uw / 1e6
				found = true
			}
		}
	}
	if !found {
		return 0, fmt.Errorf("hwmon: no power*_input sensors%s", map[bool]string{true: " named " + h.Name}[h.Name != ""])
	}
	return total, nil
}
