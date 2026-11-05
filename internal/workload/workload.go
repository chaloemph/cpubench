// Package workload contains the compute kernels cpubench measures. Kernels are
// plain Go with no interfaces or I/O in their inner loops; each RunThread call
// does a fixed, deterministic amount of work so machines can be compared.
package workload

import (
	"hash/crc32"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
)

// All returns every built-in workload in a stable order.
func All() []benchmark.Workload {
	ws := []benchmark.Workload{
		&sortWL{}, &compressWL{}, &decompressWL{}, &jsonWL{}, &regexWL{}, &hashmapWL{},
		&matmulWL{}, &nbodyWL{}, &fftWL{},
		&aesGCMWL{}, &sha256WL{}, &ecdsaWL{}, &rsaWL{},
		&triadWL{}, &copyWL{},
		&atomicContendedWL{}, &atomicLocalWL{}, &mutexWL{}, &pingPongWL{},
	}
	for _, kb := range LatencySizesKB {
		ws = append(ws, &latencyWL{sizeKB: kb})
	}
	return ws
}

// LatencySizesKB are the working-set sizes of the pointer-chase ladder.
var LatencySizesKB = []int{32, 256, 1024, 8 * 1024, 32 * 1024, 128 * 1024, 512 * 1024}

// Select filters workloads by name prefix or category ("" or "all" = all).
func Select(ws []benchmark.Workload, filter string) []benchmark.Workload {
	if filter == "" || filter == "all" {
		return ws
	}
	var out []benchmark.Workload
	for _, w := range ws {
		i := w.Info()
		for f := range strings.SplitSeq(filter, ",") {
			f = strings.TrimSpace(f)
			if f == string(i.Category) || i.Name == f || strings.HasPrefix(i.Name, f) {
				out = append(out, w)
				break
			}
		}
	}
	return out
}

// Find returns a workload by exact name.
func Find(name string) benchmark.Workload {
	i := slices.IndexFunc(All(), func(w benchmark.Workload) bool { return w.Info().Name == name })
	if i < 0 {
		return nil
	}
	return All()[i]
}

func rng(id int) *rand.Rand { return rand.New(rand.NewPCG(0x5eed, uint64(id)+1)) }

func scaled(base int, scale float64) int { return max(1, int(float64(base)*scale)) }

func mix(h, v uint64) uint64 {
	h ^= v + 0x9e3779b97f4a7c15 + (h << 6) + (h >> 2)
	return h
}

func crc(b []byte) uint64 { return uint64(crc32.ChecksumIEEE(b)) }

// fsum hashes a float rounded to 6 significant digits, so results stay
// comparable when one compiler fuses multiply-add (arm64) and another does not.
func fsum(h uint64, v float64) uint64 {
	return mix(h, crc([]byte(strconv.FormatFloat(v, 'e', 5, 64))))
}

// text generates deterministic log-like text.
func text(n int, seed int) []byte {
	words := []string{"GET", "POST", "/api/v1/products", "/api/v1/orders", "200", "404", "user", "session",
		"cache", "miss", "hit", "latency", "ms", "json", "token", "checkout", "cart", "item", "price", "the"}
	r := rng(seed)
	b := make([]byte, 0, n+64)
	for len(b) < n {
		b = append(b, words[r.IntN(len(words))]...)
		if r.IntN(8) == 0 {
			b = append(b, '\n')
		} else {
			b = append(b, ' ')
		}
		if r.IntN(5) == 0 {
			b = strconv.AppendInt(b, int64(r.IntN(100000)), 10)
			b = append(b, ' ')
		}
	}
	return b[:n]
}
