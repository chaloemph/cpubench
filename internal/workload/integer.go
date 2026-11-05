package workload

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
)

// ---- int/sort ----

type sortWL struct {
	src, dst [][]uint64
}

func (*sortWL) Info() benchmark.Info {
	return benchmark.Info{Name: "int/sort", Category: benchmark.Integer, Kind: benchmark.Throughput,
		Unit: "Melem/s", UnitScale: 1e-6, Description: "sort 1M random uint64 (branchy integer code)",
		FootprintMB: 16, PerThread: true}
}

func (w *sortWL) Setup(threads int, scale float64) error {
	n := scaled(1<<20, scale)
	w.src, w.dst = make([][]uint64, threads), make([][]uint64, threads)
	for t := range threads {
		r := rng(t)
		w.src[t] = make([]uint64, n)
		for i := range w.src[t] {
			w.src[t][i] = r.Uint64()
		}
		w.dst[t] = make([]uint64, n)
	}
	return nil
}

func (w *sortWL) RunThread(id int) (float64, uint64) {
	d := w.dst[id]
	copy(d, w.src[id])
	slices.Sort(d)
	var h uint64
	for i := 0; i < len(d); i += 4096 {
		h = mix(h, d[i])
	}
	return float64(len(d)), h
}

func (w *sortWL) Teardown() { w.src, w.dst = nil, nil }

// ---- int/compress, int/decompress ----

type compressWL struct {
	input []byte
	buf   []*bytes.Buffer
}

func (*compressWL) Info() benchmark.Info {
	return benchmark.Info{Name: "int/compress", Category: benchmark.Integer, Kind: benchmark.Throughput,
		Unit: "MB/s", UnitScale: 1e-6, Description: "deflate level 6 of 8MB log-like text (gzip responses)",
		FootprintMB: 8, PerThread: true}
}

func (w *compressWL) Setup(threads int, scale float64) error {
	w.input = text(scaled(8<<20, scale), 1)
	w.buf = make([]*bytes.Buffer, threads)
	for t := range w.buf {
		w.buf[t] = bytes.NewBuffer(make([]byte, 0, len(w.input)/2))
	}
	return nil
}

func (w *compressWL) RunThread(id int) (float64, uint64) {
	b := w.buf[id]
	b.Reset()
	zw, _ := flate.NewWriter(b, 6)
	zw.Write(w.input)
	zw.Close()
	return float64(len(w.input)), crc(b.Bytes())
}

func (w *compressWL) Teardown() { w.input, w.buf = nil, nil }

type decompressWL struct {
	packed []byte
	size   int
	out    [][]byte
}

func (*decompressWL) Info() benchmark.Info {
	return benchmark.Info{Name: "int/decompress", Category: benchmark.Integer, Kind: benchmark.Throughput,
		Unit: "MB/s", UnitScale: 1e-6, Description: "inflate 8MB of deflated text (request bodies, static assets)",
		FootprintMB: 8, PerThread: true}
}

func (w *decompressWL) Setup(threads int, scale float64) error {
	in := text(scaled(8<<20, scale), 2)
	var b bytes.Buffer
	zw, _ := flate.NewWriter(&b, 6)
	zw.Write(in)
	zw.Close()
	w.packed, w.size = b.Bytes(), len(in)
	w.out = make([][]byte, threads)
	for t := range w.out {
		w.out[t] = make([]byte, len(in))
	}
	return nil
}

func (w *decompressWL) RunThread(id int) (float64, uint64) {
	r := flate.NewReader(bytes.NewReader(w.packed))
	n, _ := io.ReadFull(r, w.out[id])
	r.Close()
	return float64(n), crc(w.out[id][:n])
}

func (w *decompressWL) Teardown() { w.packed, w.out = nil, nil }

// ---- int/json ----

type record struct {
	ID     int               `json:"id"`
	Name   string            `json:"name"`
	Tags   []string          `json:"tags"`
	Price  float64           `json:"price"`
	Active bool              `json:"active"`
	Attrs  map[string]string `json:"attrs"`
}

type jsonWL struct {
	recs []record
}

func (*jsonWL) Info() benchmark.Info {
	return benchmark.Info{Name: "int/json", Category: benchmark.Integer, Kind: benchmark.Throughput,
		Unit: "Krec/s", UnitScale: 1e-3, Description: "encode + decode 20k API records with encoding/json",
		FootprintMB: 20, PerThread: true}
}

func (w *jsonWL) Setup(threads int, scale float64) error {
	r := rng(3)
	w.recs = make([]record, scaled(20000, scale))
	for i := range w.recs {
		w.recs[i] = record{ID: i, Name: fmt.Sprintf("product-%d", r.IntN(1e6)), Tags: []string{"a", "sale", strconv.Itoa(i % 17)},
			Price: float64(r.IntN(100000)) / 100, Active: i%3 == 0, Attrs: map[string]string{"color": "red", "size": strconv.Itoa(i % 5)}}
	}
	return nil
}

func (w *jsonWL) RunThread(id int) (float64, uint64) {
	b, _ := json.Marshal(w.recs)
	var back []record
	json.Unmarshal(b, &back)
	return float64(len(w.recs) * 2), mix(crc(b), uint64(len(back)))
}

func (w *jsonWL) Teardown() { w.recs = nil }

// ---- int/regex ----

var accessLog = regexp.MustCompile(`(GET|POST) (/api/v1/[a-z]+) (\d{3})`)

type regexWL struct{ input []byte }

func (*regexWL) Info() benchmark.Info {
	return benchmark.Info{Name: "int/regex", Category: benchmark.Integer, Kind: benchmark.Throughput,
		Unit: "MB/s", UnitScale: 1e-6, Description: "regexp submatch over 2MB of access-log text"}
}

func (w *regexWL) Setup(threads int, scale float64) error {
	w.input = text(scaled(2<<20, scale), 4)
	return nil
}

func (w *regexWL) RunThread(id int) (float64, uint64) {
	m := accessLog.FindAllSubmatchIndex(w.input, -1)
	h := uint64(len(m))
	for i := 0; i < len(m); i += 64 {
		h = mix(h, uint64(m[i][0]))
	}
	return float64(len(w.input)), h
}

func (w *regexWL) Teardown() { w.input = nil }

// ---- int/hashmap ----

type hashmapWL struct {
	keys [][]uint64
}

func (*hashmapWL) Info() benchmark.Info {
	return benchmark.Info{Name: "int/hashmap", Category: benchmark.Integer, Kind: benchmark.Throughput,
		Unit: "Mops/s", UnitScale: 1e-6, Description: "insert + lookup 512k random keys in a Go map (cache-missy)",
		FootprintMB: 32, PerThread: true}
}

func (w *hashmapWL) Setup(threads int, scale float64) error {
	n := scaled(512<<10, scale)
	w.keys = make([][]uint64, threads)
	for t := range threads {
		r := rng(100 + t)
		w.keys[t] = make([]uint64, n)
		for i := range w.keys[t] {
			w.keys[t][i] = r.Uint64()
		}
	}
	return nil
}

func (w *hashmapWL) RunThread(id int) (float64, uint64) {
	keys := w.keys[id]
	m := make(map[uint64]uint64, len(keys)/4)
	for i, k := range keys {
		m[k] = uint64(i)
	}
	var h uint64
	for _, k := range keys {
		h += m[k]
	}
	return float64(2 * len(keys)), h
}

func (w *hashmapWL) Teardown() { w.keys = nil }
