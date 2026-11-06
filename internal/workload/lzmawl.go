package workload

import (
	"math/bits"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
	"github.com/chaloemph/cpubench/internal/workload/lzma"
)

// ---- int/lzma-compress, int/lzma-decompress ----

// lzmaInputMB is the input, and so the dictionary, per thread at scale 1.
const lzmaInputMB = 16

// lzmaFootprintMB is what a compressing thread holds at scale 1: input,
// match-finder chains (4 bytes per input byte), hash table and output.
const lzmaFootprintMB = lzmaInputMB*(1+4) + 32 + lzmaInputMB/2

// lzInput generates data in the manner of 7-Zip's built-in benchmark: runs of
// random bytes between repeats of earlier data, with lengths and distances
// drawn log-uniformly, so matches reach back across the whole dictionary and
// the match finder reads memory all over it.
func lzInput(n int, seed int) []byte {
	r := rng(seed)
	b := make([]byte, 0, n+300)
	for len(b) < n {
		if len(b) < 16 || r.IntN(3) == 0 {
			for k := 1 + r.IntN(8); k > 0; k-- {
				b = append(b, byte(r.Uint32()))
			}
			continue
		}
		length := 2 + r.IntN(1<<r.IntN(8))
		span := 1 << (1 + r.IntN(bits.Len(uint(len(b)))))
		from := len(b) - 1 - r.IntN(min(len(b), span))
		for k := range length {
			b = append(b, b[from+k])
		}
	}
	return b[:n]
}

type lzmaCompressWL struct {
	input []byte
	enc   []*lzma.Encoder
	out   [][]byte
}

func (*lzmaCompressWL) Info() benchmark.Info {
	return benchmark.Info{Name: "int/lzma-compress", Category: benchmark.Integer, Kind: benchmark.Throughput,
		Unit: "MB/s", UnitScale: 1e-6,
		Description: "LZMA compression with a 16MB dictionary of 7-Zip-style data (archives, packages, backups)",
		FootprintMB: lzmaFootprintMB, PerThread: true}
}

func (w *lzmaCompressWL) Setup(threads int, scale float64) error {
	w.input = lzInput(scaled(lzmaInputMB<<20, scale), 7)
	w.enc, w.out = make([]*lzma.Encoder, threads), make([][]byte, threads)
	for t := range threads {
		w.enc[t] = lzma.NewEncoder(len(w.input))
		w.out[t] = make([]byte, 0, len(w.input)/2)
	}
	return nil
}

func (w *lzmaCompressWL) RunThread(id int) (float64, uint64) {
	w.out[id] = w.enc[id].Encode(w.out[id], w.input)
	return float64(len(w.input)), crc(w.out[id])
}

func (w *lzmaCompressWL) Teardown() { w.input, w.enc, w.out = nil, nil, nil }

type lzmaDecompressWL struct {
	packed []byte
	dec    []*lzma.Decoder
	out    [][]byte
}

func (*lzmaDecompressWL) Info() benchmark.Info {
	return benchmark.Info{Name: "int/lzma-decompress", Category: benchmark.Integer, Kind: benchmark.Throughput,
		Unit: "MB/s", UnitScale: 1e-6,
		Description: "LZMA decompression of 16MB of 7-Zip-style data (unpacking archives and packages)",
		FootprintMB: lzmaInputMB, PerThread: true}
}

func (w *lzmaDecompressWL) Setup(threads int, scale float64) error {
	in := lzInput(scaled(lzmaInputMB<<20, scale), 8)
	w.packed = lzma.NewEncoder(len(in)).Encode(nil, in)
	w.dec, w.out = make([]*lzma.Decoder, threads), make([][]byte, threads)
	for t := range threads {
		w.dec[t], w.out[t] = new(lzma.Decoder), make([]byte, len(in))
	}
	return nil
}

func (w *lzmaDecompressWL) RunThread(id int) (float64, uint64) {
	if err := w.dec[id].Decode(w.out[id], w.packed); err != nil {
		return float64(len(w.out[id])), 0 // checksum 0 differs from every other machine's
	}
	return float64(len(w.out[id])), crc(w.out[id])
}

func (w *lzmaDecompressWL) Teardown() { w.packed, w.dec, w.out = nil, nil, nil }
