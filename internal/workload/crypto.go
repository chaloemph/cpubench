package workload

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/domain/hardware"
)

const cryptoChunk = 16 << 10

// The Backend methods mirror the CPU checks in Go 1.26's crypto/internal/fips140
// (aes, aes/gcm, sha256, nistec, bigmod). Features come from /proc/cpuinfo,
// which shows the same CPUID/HWCAP bits Go reads.

func hasAll(cpu hardware.CPUInfo, features ...string) bool {
	for _, f := range features {
		if !cpu.HasFeature(f) {
			return false
		}
	}
	return true
}

func backend(kind benchmark.BackendKind, detail string) *benchmark.Backend {
	return &benchmark.Backend{Kind: kind, Detail: detail}
}

// ---- crypto/aes-gcm ----

type aesGCMWL struct {
	msgs  int
	aead  cipher.AEAD
	plain []byte
	dst   [][]byte
}

func (*aesGCMWL) Info() benchmark.Info {
	return benchmark.Info{Name: "crypto/aes-gcm", Category: benchmark.Crypto, Kind: benchmark.Throughput,
		Unit: "MB/s", UnitScale: 1e-6, Description: "AES-128-GCM seal of 16KB records (TLS bulk data)"}
}

func (*aesGCMWL) Backend(cpu hardware.CPUInfo) *benchmark.Backend {
	switch {
	case len(cpu.Features) == 0:
		return nil
	case cpu.Arch == "amd64" && hasAll(cpu, "aes", "pclmulqdq", "sse4_1", "ssse3"):
		return backend(benchmark.Hardware, "AES-NI + PCLMULQDQ")
	case cpu.Arch == "amd64":
		return backend(benchmark.Software, "no AES-NI/PCLMULQDQ")
	case cpu.Arch == "arm64" && hasAll(cpu, "aes", "pmull"):
		return backend(benchmark.Hardware, "ARMv8 AES + PMULL")
	case cpu.Arch == "arm64":
		return backend(benchmark.Software, "no ARMv8 AES/PMULL")
	}
	return nil
}

func (w *aesGCMWL) Setup(threads int, scale float64) error {
	w.msgs = scaled(2048, scale)
	block, err := aes.NewCipher(make([]byte, 16))
	if err != nil {
		return err
	}
	if w.aead, err = cipher.NewGCM(block); err != nil {
		return err
	}
	w.plain = text(cryptoChunk, 5)
	w.dst = make([][]byte, threads)
	for t := range threads {
		w.dst[t] = make([]byte, 0, cryptoChunk+w.aead.Overhead())
	}
	return nil
}

func (w *aesGCMWL) RunThread(id int) (float64, uint64) {
	nonce := make([]byte, w.aead.NonceSize())
	var out []byte
	for i := range w.msgs {
		nonce[0] = byte(i)
		out = w.aead.Seal(w.dst[id][:0], nonce, w.plain, nil)
	}
	return float64(w.msgs * cryptoChunk), crc(out)
}

func (w *aesGCMWL) Teardown() { w.dst = nil }

// ---- crypto/sha256 ----

type sha256WL struct {
	msgs  int
	plain []byte
}

func (*sha256WL) Info() benchmark.Info {
	return benchmark.Info{Name: "crypto/sha256", Category: benchmark.Crypto, Kind: benchmark.Throughput,
		Unit: "MB/s", UnitScale: 1e-6, Description: "SHA-256 of 16KB blocks (signatures, ETags, HMAC)"}
}

func (*sha256WL) Backend(cpu hardware.CPUInfo) *benchmark.Backend {
	switch {
	case len(cpu.Features) == 0:
		return nil
	case cpu.Arch == "amd64" && hasAll(cpu, "sha_ni", "avx", "sse4_1", "ssse3"):
		return backend(benchmark.Hardware, "SHA-NI")
	case cpu.Arch == "amd64" && hasAll(cpu, "avx", "avx2", "bmi2"):
		return backend(benchmark.SIMD, "AVX2, no SHA-NI")
	case cpu.Arch == "amd64":
		return backend(benchmark.Assembly, "no SHA-NI/AVX2")
	case cpu.Arch == "arm64" && cpu.HasFeature("sha2"):
		return backend(benchmark.Hardware, "ARMv8 SHA2")
	case cpu.Arch == "arm64":
		return backend(benchmark.Software, "no ARMv8 SHA2")
	}
	return nil
}

func (w *sha256WL) Setup(threads int, scale float64) error {
	w.msgs, w.plain = scaled(2048, scale), text(cryptoChunk, 6)
	return nil
}

func (w *sha256WL) RunThread(id int) (float64, uint64) {
	var sum [32]byte
	for range w.msgs {
		sum = sha256.Sum256(w.plain)
	}
	return float64(w.msgs * cryptoChunk), crc(sum[:])
}

func (w *sha256WL) Teardown() {}

// ---- crypto/ecdsa-p256 ----

type ecdsaWL struct {
	ops int
	key *ecdsa.PrivateKey
}

func (*ecdsaWL) Info() benchmark.Info {
	return benchmark.Info{Name: "crypto/ecdsa-p256", Category: benchmark.Crypto, Kind: benchmark.Throughput,
		Unit: "ops/s", UnitScale: 1, Description: "ECDSA P-256 sign + verify (TLS handshakes with ECDSA certs, JWT ES256)"}
}

// P-256 uses assembly on amd64 and arm64 regardless of CPU features.
func (*ecdsaWL) Backend(cpu hardware.CPUInfo) *benchmark.Backend {
	if cpu.Arch == "amd64" || cpu.Arch == "arm64" {
		return backend(benchmark.Assembly, "P-256 assembly")
	}
	return nil
}

func (w *ecdsaWL) Setup(threads int, scale float64) error {
	w.ops = scaled(400, scale)
	var err error
	w.key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	return err
}

func (w *ecdsaWL) RunThread(id int) (float64, uint64) {
	digest := sha256.Sum256([]byte("cpubench"))
	var ok uint64
	for range w.ops {
		sig, err := ecdsa.SignASN1(rand.Reader, w.key, digest[:])
		if err == nil && ecdsa.VerifyASN1(&w.key.PublicKey, digest[:], sig) {
			ok++
		}
	}
	return float64(w.ops), ok
}

func (w *ecdsaWL) Teardown() { w.key = nil }

// ---- crypto/rsa2048 ----

type rsaWL struct {
	ops int
	key *rsa.PrivateKey
}

func (*rsaWL) Info() benchmark.Info {
	return benchmark.Info{Name: "crypto/rsa2048-sign", Category: benchmark.Crypto, Kind: benchmark.Throughput,
		Unit: "ops/s", UnitScale: 1, Description: "RSA-2048 PKCS#1 v1.5 sign (TLS handshakes with RSA certs, JWT RS256)"}
}

func (*rsaWL) Backend(cpu hardware.CPUInfo) *benchmark.Backend {
	switch {
	case cpu.Arch == "amd64" && hasAll(cpu, "adx", "bmi2"):
		return backend(benchmark.Assembly, "bignum with ADX/BMI2")
	case cpu.Arch == "amd64" && len(cpu.Features) > 0:
		return backend(benchmark.Assembly, "bignum, no ADX/BMI2")
	case cpu.Arch == "arm64":
		return backend(benchmark.Assembly, "bignum")
	}
	return nil
}

func (w *rsaWL) Setup(threads int, scale float64) error {
	w.ops = scaled(100, scale)
	var err error
	w.key, err = rsa.GenerateKey(rand.Reader, 2048)
	return err
}

func (w *rsaWL) RunThread(id int) (float64, uint64) {
	digest := sha256.Sum256([]byte("cpubench"))
	var ok uint64
	for range w.ops {
		if _, err := rsa.SignPKCS1v15(nil, w.key, crypto.SHA256, digest[:]); err == nil {
			ok++
		}
	}
	return float64(w.ops), ok
}

func (w *rsaWL) Teardown() { w.key = nil }
