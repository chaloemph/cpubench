package linux

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// AvgMHz averages scaling_cur_freq over cpus (all online CPUs when nil).
func (p *Probe) AvgMHz(cpus []int) (float64, bool) {
	if cpus == nil {
		cpus = ParseCPUList(p.read("sys/devices/system/cpu/online"))
	}
	var sum float64
	var n int
	for _, c := range cpus {
		khz := p.readInt(0, fmt.Sprintf("sys/devices/system/cpu/cpu%d/cpufreq/scaling_cur_freq", c))
		if khz > 0 {
			sum += float64(khz) / 1000
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}

// MaxTempC is the hottest thermal zone or hwmon temperature sensor.
func (p *Probe) MaxTempC() (float64, bool) {
	files, _ := filepath.Glob(p.path("sys/class/thermal/thermal_zone*/temp"))
	hw, _ := filepath.Glob(p.path("sys/class/hwmon/hwmon*/temp*_input"))
	best, ok := 0.0, false
	for _, f := range append(files, hw...) {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if err != nil || v <= 0 {
			continue
		}
		if c := v / 1000; c > best && c < 150 {
			best, ok = c, true
		}
	}
	return best, ok
}

func (p *Probe) CgroupThrottle() (int64, int64, bool) {
	s := p.read(p.cgroupDir(), "cpu.stat")
	if s == "" {
		return 0, 0, false
	}
	nr, usec := parseCPUStat(s)
	return nr, usec, true
}

var hwErrorMarkers = []string{"mce:", "machine check", "edac", "hardware error", "ras:", "ghes", "sei", "corrected error"}

// KernelErrors returns hardware-error lines from the kernel log. Needs root on
// Ubuntu (kernel.dmesg_restrict=1); returns nil when it cannot read the log.
func (p *Probe) KernelErrors() []string {
	if !p.Live || !haveBin("dmesg") {
		return nil
	}
	out, err := exec.Command("dmesg", "--level=emerg,alert,crit,err,warn").Output()
	if err != nil {
		return nil
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		l := strings.ToLower(sc.Text())
		for _, m := range hwErrorMarkers {
			if strings.Contains(l, m) {
				lines = append(lines, sc.Text())
				break
			}
		}
	}
	return lines
}
