package workload

import (
	"strings"
	"testing"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
)

func TestCryptoBackends(t *testing.T) {
	cpu := func(arch, features string) hardware.CPUInfo {
		return hardware.CPUInfo{Arch: arch, Features: strings.Fields(features)}
	}
	icelake := cpu("amd64", "sse4_1 ssse3 aes pclmulqdq avx avx2 bmi2 adx sha_ni")
	skylake := cpu("amd64", "sse4_1 ssse3 aes pclmulqdq avx avx2 bmi2 adx")
	kvm64 := cpu("amd64", "sse4_1 ssse3")
	armv8crypto := cpu("arm64", "fp asimd aes pmull sha1 sha2 crc32 atomics")
	noCrypto := cpu("arm64", "fp asimd atomics")

	cases := []struct {
		w    benchmark.BackendReporter
		cpu  hardware.CPUInfo
		want benchmark.BackendKind
	}{
		{&aesGCMWL{}, icelake, benchmark.Hardware},
		{&aesGCMWL{}, kvm64, benchmark.Software},
		{&aesGCMWL{}, armv8crypto, benchmark.Hardware},
		{&aesGCMWL{}, noCrypto, benchmark.Software},
		{&sha256WL{}, icelake, benchmark.Hardware},
		{&sha256WL{}, skylake, benchmark.SIMD},
		{&sha256WL{}, kvm64, benchmark.Assembly},
		{&sha256WL{}, armv8crypto, benchmark.Hardware},
		{&sha256WL{}, noCrypto, benchmark.Software},
		{&ecdsaWL{}, armv8crypto, benchmark.Assembly},
		{&rsaWL{}, skylake, benchmark.Assembly},
	}
	for _, c := range cases {
		b := c.w.Backend(c.cpu)
		name := c.w.(benchmark.Workload).Info().Name
		if b == nil || b.Kind != c.want {
			t.Errorf("%s on %s %v: got %v want %s", name, c.cpu.Arch, c.cpu.Features, b, c.want)
		}
	}
	if b := (&sha256WL{}).Backend(hardware.CPUInfo{Arch: "arm64"}); b != nil {
		t.Errorf("unknown features: got %v", b)
	}
	for _, w := range All() {
		if w.Info().Category == benchmark.Crypto {
			if _, ok := w.(benchmark.BackendReporter); !ok {
				t.Errorf("%s does not report its backend", w.Info().Name)
			}
		}
	}
}
