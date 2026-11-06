package lzma

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
)

// sample is text with repeats near and far, so a stream uses literals,
// matched literals, matches with every kind of distance, and rep matches.
func sample(n int) []byte {
	r := rand.New(rand.NewPCG(1, 2))
	words := strings.Fields("the quick brown fox jumps over lazy dog GET POST /api/v1/orders 200 404 cache miss hit")
	var b []byte
	for len(b) < n {
		switch r.IntN(4) {
		case 0:
			if len(b) > 300 {
				from := r.IntN(len(b) - 280)
				b = append(b, b[from:from+2+r.IntN(270)]...)
				continue
			}
			fallthrough
		default:
			b = append(b, words[r.IntN(len(words))]...)
			b = append(b, ' ', byte('0'+r.IntN(10)))
		}
	}
	return b[:n]
}

func roundTrip(t *testing.T, name string, in []byte) []byte {
	t.Helper()
	z := NewEncoder(len(in))
	packed := z.Encode(nil, in)
	out := make([]byte, len(in))
	if err := new(Decoder).Decode(out, packed); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !bytes.Equal(out, in) {
		t.Fatalf("%s: round trip differs", name)
	}
	return packed
}

func TestRoundTrip(t *testing.T) {
	random := make([]byte, 100_000)
	r := rand.New(rand.NewPCG(3, 4))
	for i := range random {
		random[i] = byte(r.Uint32())
	}
	for name, in := range map[string][]byte{
		"empty":  {},
		"one":    {42},
		"zeros":  make([]byte, 1<<20), // rep0 at distance 1, long lengths
		"random": random,              // literals only
		"text":   sample(1 << 20),
		"abab":   bytes.Repeat([]byte("ab"), 5000),
	} {
		packed := roundTrip(t, name, in)
		if name == "text" && len(packed) > len(in)/3 {
			t.Errorf("text compressed to %d of %d bytes", len(packed), len(in))
		}
	}
}

func TestDeterministic(t *testing.T) {
	in := sample(200_000)
	a := NewEncoder(len(in)).Encode(nil, in)
	z := NewEncoder(len(in))
	z.Encode(nil, sample(50_000)) // a used encoder must not remember anything
	if b := z.Encode(nil, in); !bytes.Equal(a, b) {
		t.Fatal("same input, different streams")
	}
}

// The stream is part of the benchmark's checksum, so x86-64 and arm64 (CI runs
// both) must produce the same bytes. Changing the encoder changes this value
// and every lzma-compress checksum: results from before and after the change
// do not compare.
func TestStreamIsPinned(t *testing.T) {
	in := sample(200_000)
	p := NewEncoder(len(in)).Encode(nil, in)
	if sum := crc32.ChecksumIEEE(p); sum != 0xd484912b || len(p) != 17916 {
		t.Fatalf("stream crc %#08x, %d bytes", sum, len(p))
	}
}

// testdata/xz.lzma is sample(65536) compressed by xz (xz --format=lzma -9e),
// whose optimal parser uses every symbol kind; decoding it checks this
// decoder against another implementation. xz writes an end marker after the
// data, which Decode does not need to read.
func TestDecodesXZ(t *testing.T) {
	f, err := os.ReadFile("testdata/xz.lzma")
	if err != nil {
		t.Fatal(err)
	}
	if f[0] != Props {
		t.Fatalf("props %#x", f[0])
	}
	want := sample(65536)
	if size := binary.LittleEndian.Uint64(f[5:]); size != uint64(len(want)) && size != ^uint64(0) {
		t.Fatalf("size %d", size)
	}
	out := make([]byte, len(want))
	if err := new(Decoder).Decode(out, f[13:]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, want) {
		t.Fatal("xz stream decoded to different bytes")
	}
}

func TestCorrupt(t *testing.T) {
	in := sample(10_000)
	packed := NewEncoder(len(in)).Encode(nil, in)
	if err := new(Decoder).Decode(make([]byte, len(in)), packed[:len(packed)/2]); err == nil {
		t.Error("truncated stream decoded")
	}
	if err := new(Decoder).Decode(make([]byte, 10), []byte{1, 0, 0, 0, 0}); err == nil {
		t.Error("stream not starting with 0 decoded")
	}
}

func TestHeader(t *testing.T) {
	h := Header(DictSize(5000), 5000)
	if len(h) != 13 || h[0] != 0x5D || binary.LittleEndian.Uint32(h[1:]) != 8192 {
		t.Fatalf("header % x", h)
	}
	if DictSize(1) != 4096 || DictSize(1<<20) != 1<<20 || DictSize(1<<20+1) != 2<<20 {
		t.Fatal("dict size")
	}
}
