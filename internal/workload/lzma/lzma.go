// Package lzma is a small LZMA (LZMA1) encoder and decoder written for the
// benchmark: the format's range coder, adaptive probability models and state
// machine, a hash-chain match finder, and greedy parsing with one step of lazy
// matching. It is a benchmark kernel, not a general compressor: the whole input
// is in memory, the dictionary is the input, and the stream has no end marker
// (the size is known). Streams use lc=3, lp=0, pb=2, the defaults of 7-Zip and
// xz, so a stream with a 13-byte .lzma header in front decodes with xz.
package lzma

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

// Literal context bits, literal position bits and position bits.
const (
	lc = 3
	lp = 0
	pb = 2
)

// Props is the properties byte of the stream, (pb*5+lp)*9+lc.
const Props = (pb*5+lp)*9 + lc

const (
	numStates          = 12
	posBitsMax         = 4
	posStateMask       = 1<<pb - 1
	minMatch           = 2
	maxMatch           = 273
	numLenToPosStates  = 4
	numAlignBits       = 4
	endPosModelIndex   = 14
	numFullDistances   = 1 << (endPosModelIndex >> 1)
	numBitModelTotal   = 11
	numMoveBits        = 5
	probInit           = 1 << numBitModelTotal >> 1
	topValue           = 1 << 24
	literalCoderSize   = 0x300
	literalStateShift  = 8 - lc
	firstMatchedLitSym = 0x100
)

type prob uint16

type lenModel struct {
	choice, choice2 prob
	low             [1 << posBitsMax][1 << 3]prob
	mid             [1 << posBitsMax][1 << 3]prob
	high            [1 << 8]prob
}

// model is every adaptive probability of the format.
type model struct {
	literal    [literalCoderSize << (lc + lp)]prob
	isMatch    [numStates << posBitsMax]prob
	isRep      [numStates]prob
	isRepG0    [numStates]prob
	isRepG1    [numStates]prob
	isRepG2    [numStates]prob
	isRep0Long [numStates << posBitsMax]prob
	posSlot    [numLenToPosStates][1 << 6]prob
	posSpecial [1 + numFullDistances - endPosModelIndex]prob
	align      [1 << numAlignBits]prob
	length     lenModel
	repLength  lenModel
}

// freshModel has every probability at one half; copying it resets a model.
var freshModel = func() (m model) {
	fill := func(ps []prob) {
		for i := range ps {
			ps[i] = probInit
		}
	}
	fillLen := func(l *lenModel) {
		l.choice, l.choice2 = probInit, probInit
		for i := range l.low {
			fill(l.low[i][:])
			fill(l.mid[i][:])
		}
		fill(l.high[:])
	}
	fill(m.literal[:])
	fill(m.isMatch[:])
	fill(m.isRep[:])
	fill(m.isRepG0[:])
	fill(m.isRepG1[:])
	fill(m.isRepG2[:])
	fill(m.isRep0Long[:])
	for i := range m.posSlot {
		fill(m.posSlot[i][:])
	}
	fill(m.posSpecial[:])
	fill(m.align[:])
	fillLen(&m.length)
	fillLen(&m.repLength)
	return m
}()

func stateAfterLiteral(s uint32) uint32 {
	switch {
	case s < 4:
		return 0
	case s < 10:
		return s - 3
	}
	return s - 6
}

func stateAfterMatch(s uint32) uint32    { return 7 + 3*min(s/7, 1) }
func stateAfterRep(s uint32) uint32      { return 8 + 3*min(s/7, 1) }
func stateAfterShortRep(s uint32) uint32 { return 9 + 2*min(s/7, 1) }

// posSlot is the slot a distance (minus one) is coded in: the top two bits.
func posSlot(d uint32) uint32 {
	if d < 4 {
		return d
	}
	n := uint32(bits.Len32(d)) - 1
	return n<<1 | (d>>(n-1))&1
}

// Header is the 13-byte .lzma (LZMA_Alone) header: properties, dictionary
// size and uncompressed size, which xz and 7-Zip read in front of a stream.
func Header(dictSize uint32, size uint64) []byte {
	h := make([]byte, 13)
	h[0] = Props
	binary.LittleEndian.PutUint32(h[1:], dictSize)
	binary.LittleEndian.PutUint64(h[5:], size)
	return h
}

// DictSize is the dictionary size a header must state for a stream of n
// input bytes: n rounded up to a power of two, at least 4 KiB.
func DictSize(n int) uint32 {
	return uint32(1) << max(12, bits.Len32(uint32(max(n, 1)-1)))
}

// ---- range encoder ----

type rangeEncoder struct {
	low       uint64
	rng       uint32
	cache     byte
	cacheSize int
	out       []byte
}

func (e *rangeEncoder) reset(out []byte) {
	*e = rangeEncoder{rng: 0xFFFFFFFF, cacheSize: 1, out: out[:0]}
}

func (e *rangeEncoder) shiftLow() {
	if uint32(e.low) < 0xFF000000 || e.low>>32 != 0 {
		carry := byte(e.low >> 32)
		b := e.cache
		for {
			e.out = append(e.out, b+carry)
			b = 0xFF
			if e.cacheSize--; e.cacheSize == 0 {
				break
			}
		}
		e.cache = byte(e.low >> 24)
	}
	e.cacheSize++
	e.low = (e.low & 0x00FFFFFF) << 8
}

func (e *rangeEncoder) bit(p *prob, b uint32) {
	bound := (e.rng >> numBitModelTotal) * uint32(*p)
	if b == 0 {
		e.rng = bound
		*p += (1<<numBitModelTotal - *p) >> numMoveBits
	} else {
		e.low += uint64(bound)
		e.rng -= bound
		*p -= *p >> numMoveBits
	}
	if e.rng < topValue {
		e.rng <<= 8
		e.shiftLow()
	}
}

func (e *rangeEncoder) direct(v uint32, n int) {
	for n > 0 {
		n--
		e.rng >>= 1
		if (v>>n)&1 != 0 {
			e.low += uint64(e.rng)
		}
		if e.rng < topValue {
			e.rng <<= 8
			e.shiftLow()
		}
	}
}

func (e *rangeEncoder) flush() {
	for range 5 {
		e.shiftLow()
	}
}

func (e *rangeEncoder) tree(probs []prob, n int, sym uint32) {
	m := uint32(1)
	for i := n - 1; i >= 0; i-- {
		b := (sym >> i) & 1
		e.bit(&probs[m], b)
		m = m<<1 | b
	}
}

func (e *rangeEncoder) reverseTree(probs []prob, n int, sym uint32) {
	m := uint32(1)
	for range n {
		b := sym & 1
		sym >>= 1
		e.bit(&probs[m], b)
		m = m<<1 | b
	}
}

// length codes l = match length - 2.
func (e *rangeEncoder) length(lm *lenModel, l, posState uint32) {
	switch {
	case l < 8:
		e.bit(&lm.choice, 0)
		e.tree(lm.low[posState][:], 3, l)
	case l < 16:
		e.bit(&lm.choice, 1)
		e.bit(&lm.choice2, 0)
		e.tree(lm.mid[posState][:], 3, l-8)
	default:
		e.bit(&lm.choice, 1)
		e.bit(&lm.choice2, 1)
		e.tree(lm.high[:], 8, l-16)
	}
}

// ---- encoder ----

// Encoder holds the match finder and models, so one per thread can be reused
// without allocating in the measured loop.
type Encoder struct {
	rc       rangeEncoder
	m        model
	head     []int32 // newest position+1 per hash, 0 = empty
	prev     []int32 // previous position+1 with the same hash
	hashBits int
	inserted int // positions below this are in the chains
	// Depth is how many chain entries a search looks at, Nice the length at
	// which it stops looking for a longer match.
	Depth, Nice int
	// the lazy step searches one position ahead; its result is kept here
	lookPos, lookLen, lookDist int
}

// NewEncoder sizes the match finder for inputs of up to n bytes. Its hash
// table grows with the dictionary as in 7-Zip's HC4 finder (n/2 entries, at
// least 64K, at most 16M), so the search reaches into memory the way a
// compressor with that dictionary does.
func NewEncoder(n int) *Encoder {
	hb := min(max(bits.Len32(uint32(max(n, 1)-1))-1, 16), 24)
	return &Encoder{head: make([]int32, 1<<hb), prev: make([]int32, n), hashBits: hb, Depth: 32, Nice: 32}
}

func (z *Encoder) hash(src []byte, i int) uint32 {
	return (binary.LittleEndian.Uint32(src[i:]) * 0x9E3779B1) >> (32 - z.hashBits)
}

func (z *Encoder) insertTo(src []byte, end int) {
	for i := z.inserted; i < end; i++ {
		if i+4 <= len(src) {
			h := z.hash(src, i)
			z.prev[i] = z.head[h]
			z.head[h] = int32(i + 1)
		}
	}
	z.inserted = max(z.inserted, end)
}

func matchLen(a, b []byte, limit int) int {
	n := 0
	for n+8 <= limit {
		if x := binary.LittleEndian.Uint64(a[n:]) ^ binary.LittleEndian.Uint64(b[n:]); x != 0 {
			return n + bits.TrailingZeros64(x)>>3
		}
		n += 8
	}
	for n < limit && a[n] == b[n] {
		n++
	}
	return n
}

// longest finds the longest earlier match at pos (length, distance) and adds
// pos to the chains.
func (z *Encoder) longest(src []byte, pos int) (int, int) {
	if pos == z.lookPos {
		return z.lookLen, z.lookDist
	}
	z.insertTo(src, pos)
	best, dist := 0, 0
	if limit := min(maxMatch, len(src)-pos); limit >= 4 {
		cand := int(z.head[z.hash(src, pos)]) - 1
		for d := z.Depth; cand >= 0 && d > 0; d-- {
			if src[cand+best] == src[pos+best] {
				if l := matchLen(src[cand:], src[pos:], limit); l > best {
					best, dist = l, pos-cand
					if l >= z.Nice || l == limit {
						break
					}
				}
			}
			cand = int(z.prev[cand]) - 1
		}
	}
	z.insertTo(src, pos+1)
	if best < 3 || best == 3 && dist > 1<<12 {
		best, dist = 0, 0 // too short to pay for its distance
	}
	z.lookPos, z.lookLen, z.lookDist = pos, best, dist
	return best, dist
}

// Encode compresses src into dst (reusing its storage) and returns the stream.
func (z *Encoder) Encode(dst, src []byte) []byte {
	if len(src) > len(z.prev) {
		z.prev = make([]int32, len(src))
	}
	z.m = freshModel
	clear(z.head)
	z.inserted, z.lookPos = 0, -1
	z.rc.reset(dst)
	e, m := &z.rc, &z.m
	var state uint32
	var reps [4]uint32

	literal := func(pos int) {
		posState := uint32(pos) & posStateMask
		e.bit(&m.isMatch[state<<posBitsMax+posState], 0)
		var prevByte byte
		if pos > 0 {
			prevByte = src[pos-1]
		}
		probs := m.literal[literalCoderSize*uint32(prevByte>>literalStateShift):][:literalCoderSize]
		b := uint32(src[pos])
		if state < 7 {
			e.tree(probs, 8, b)
		} else {
			match := uint32(src[pos-int(reps[0])-1])
			sym, i := uint32(1), 7
			for ; i >= 0; i-- {
				mb, bit := (match>>i)&1, (b>>i)&1
				e.bit(&probs[(1+mb)<<8+sym], bit)
				sym = sym<<1 | bit
				if mb != bit {
					i--
					break
				}
			}
			for ; i >= 0; i-- {
				bit := (b >> i) & 1
				e.bit(&probs[sym], bit)
				sym = sym<<1 | bit
			}
		}
		state = stateAfterLiteral(state)
	}
	match := func(pos, length, dist int) {
		posState := uint32(pos) & posStateMask
		e.bit(&m.isMatch[state<<posBitsMax+posState], 1)
		e.bit(&m.isRep[state], 0)
		l := uint32(length - minMatch)
		e.length(&m.length, l, posState)
		state = stateAfterMatch(state)
		d := uint32(dist - 1)
		slot := posSlot(d)
		e.tree(m.posSlot[min(l, numLenToPosStates-1)][:], 6, slot)
		if slot >= 4 {
			nd := int(slot>>1) - 1
			base := (2 | slot&1) << nd
			if slot < endPosModelIndex {
				e.reverseTree(m.posSpecial[base-slot:], nd, d-base)
			} else {
				e.direct((d-base)>>numAlignBits, nd-numAlignBits)
				e.reverseTree(m.align[:], numAlignBits, d-base)
			}
		}
		reps = [4]uint32{d, reps[0], reps[1], reps[2]}
	}
	rep := func(pos, idx, length int) {
		posState := uint32(pos) & posStateMask
		e.bit(&m.isMatch[state<<posBitsMax+posState], 1)
		e.bit(&m.isRep[state], 1)
		if idx == 0 {
			e.bit(&m.isRepG0[state], 0)
			e.bit(&m.isRep0Long[state<<posBitsMax+posState], 1)
		} else {
			e.bit(&m.isRepG0[state], 1)
			if idx == 1 {
				e.bit(&m.isRepG1[state], 0)
			} else {
				e.bit(&m.isRepG1[state], 1)
				e.bit(&m.isRepG2[state], uint32(idx-2))
			}
			d := reps[idx]
			copy(reps[1:idx+1], reps[:idx])
			reps[0] = d
		}
		e.length(&m.repLength, uint32(length-minMatch), posState)
		state = stateAfterRep(state)
	}
	shortRep := func(pos int) {
		posState := uint32(pos) & posStateMask
		e.bit(&m.isMatch[state<<posBitsMax+posState], 1)
		e.bit(&m.isRep[state], 1)
		e.bit(&m.isRepG0[state], 0)
		e.bit(&m.isRep0Long[state<<posBitsMax+posState], 0)
		state = stateAfterShortRep(state)
	}

	for pos := 0; pos < len(src); {
		limit := min(maxMatch, len(src)-pos)
		repLen, repIdx := 0, 0
		for i, r := range reps {
			if int(r) < pos && limit >= minMatch {
				if l := matchLen(src[pos-int(r)-1:], src[pos:], limit); l > repLen {
					repLen, repIdx = l, i
				}
			}
		}
		if repLen >= z.Nice {
			rep(pos, repIdx, repLen)
			pos += repLen
			continue
		}
		mainLen, mainDist := z.longest(src, pos)
		if repLen >= minMatch && repLen+1 >= mainLen {
			rep(pos, repIdx, repLen)
			pos += repLen
			continue
		}
		if mainLen > 0 {
			if mainLen < z.Nice && pos+1 < len(src) {
				if next, _ := z.longest(src, pos+1); next > mainLen+1 {
					literal(pos) // a longer match starts one byte later
					pos++
					continue
				}
			}
			match(pos, mainLen, mainDist)
			pos += mainLen
			continue
		}
		if int(reps[0]) < pos && src[pos] == src[pos-int(reps[0])-1] {
			shortRep(pos)
		} else {
			literal(pos)
		}
		pos++
	}
	e.flush()
	return e.out
}

// ---- decoder ----

// ErrCorrupt is returned for a stream that does not decode to the size asked.
var ErrCorrupt = errors.New("lzma: corrupt stream")

type rangeDecoder struct {
	src  []byte
	i    int
	rng  uint32
	code uint32
}

func (d *rangeDecoder) next() uint32 {
	var b byte
	if d.i < len(d.src) {
		b = d.src[d.i]
	}
	d.i++
	return uint32(b)
}

func (d *rangeDecoder) bit(p *prob) uint32 {
	bound := (d.rng >> numBitModelTotal) * uint32(*p)
	var b uint32
	if d.code < bound {
		d.rng = bound
		*p += (1<<numBitModelTotal - *p) >> numMoveBits
	} else {
		d.rng -= bound
		d.code -= bound
		*p -= *p >> numMoveBits
		b = 1
	}
	if d.rng < topValue {
		d.rng <<= 8
		d.code = d.code<<8 | d.next()
	}
	return b
}

func (d *rangeDecoder) direct(n int) uint32 {
	var v uint32
	for range n {
		d.rng >>= 1
		d.code -= d.rng
		t := 0 - (d.code >> 31)
		d.code += d.rng & t
		v = v<<1 + (t + 1)
		if d.rng < topValue {
			d.rng <<= 8
			d.code = d.code<<8 | d.next()
		}
	}
	return v
}

func (d *rangeDecoder) tree(probs []prob, n int) uint32 {
	m := uint32(1)
	for range n {
		m = m<<1 | d.bit(&probs[m])
	}
	return m - 1<<n
}

func (d *rangeDecoder) reverseTree(probs []prob, n int) uint32 {
	m, v := uint32(1), uint32(0)
	for i := range n {
		b := d.bit(&probs[m])
		m = m<<1 | b
		v |= b << i
	}
	return v
}

func (d *rangeDecoder) length(lm *lenModel, posState uint32) uint32 {
	if d.bit(&lm.choice) == 0 {
		return d.tree(lm.low[posState][:], 3)
	}
	if d.bit(&lm.choice2) == 0 {
		return 8 + d.tree(lm.mid[posState][:], 3)
	}
	return 16 + d.tree(lm.high[:], 8)
}

// Decoder holds the models, so one per thread can be reused.
type Decoder struct{ m model }

// Decode decompresses src into dst, filling exactly len(dst) bytes. Data
// after that (such as an end marker) is ignored.
func (z *Decoder) Decode(dst, src []byte) error {
	z.m = freshModel
	m := &z.m
	if len(src) < 5 || src[0] != 0 {
		return ErrCorrupt
	}
	d := rangeDecoder{src: src, i: 5, rng: 0xFFFFFFFF, code: binary.BigEndian.Uint32(src[1:])}
	var state, rep0, rep1, rep2, rep3 uint32
	n := len(dst)
	for pos := 0; pos < n; {
		posState := uint32(pos) & posStateMask
		if d.bit(&m.isMatch[state<<posBitsMax+posState]) == 0 {
			var prevByte byte
			if pos > 0 {
				prevByte = dst[pos-1]
			}
			probs := m.literal[literalCoderSize*uint32(prevByte>>literalStateShift):][:literalCoderSize]
			sym := uint32(1)
			if state >= 7 {
				if int(rep0) >= pos {
					return ErrCorrupt
				}
				match := uint32(dst[pos-int(rep0)-1])
				for sym < firstMatchedLitSym {
					mb := (match >> 7) & 1
					match <<= 1
					b := d.bit(&probs[(1+mb)<<8+sym])
					sym = sym<<1 | b
					if mb != b {
						break
					}
				}
			}
			for sym < firstMatchedLitSym {
				sym = sym<<1 | d.bit(&probs[sym])
			}
			dst[pos] = byte(sym)
			pos++
			state = stateAfterLiteral(state)
			continue
		}
		var l uint32
		if d.bit(&m.isRep[state]) == 0 {
			rep3, rep2, rep1 = rep2, rep1, rep0
			l = d.length(&m.length, posState)
			state = stateAfterMatch(state)
			slot := d.tree(m.posSlot[min(l, numLenToPosStates-1)][:], 6)
			rep0 = slot
			if slot >= 4 {
				nd := int(slot>>1) - 1
				rep0 = (2 | slot&1) << nd
				if slot < endPosModelIndex {
					rep0 += d.reverseTree(m.posSpecial[rep0-slot:], nd)
				} else {
					rep0 += d.direct(nd-numAlignBits) << numAlignBits
					rep0 += d.reverseTree(m.align[:], numAlignBits)
				}
			}
			if rep0 == 0xFFFFFFFF {
				return ErrCorrupt // end marker before the expected size
			}
		} else {
			if d.bit(&m.isRepG0[state]) == 0 {
				if d.bit(&m.isRep0Long[state<<posBitsMax+posState]) == 0 {
					if int(rep0) >= pos {
						return ErrCorrupt
					}
					dst[pos] = dst[pos-int(rep0)-1]
					pos++
					state = stateAfterShortRep(state)
					continue
				}
			} else {
				var dist uint32
				if d.bit(&m.isRepG1[state]) == 0 {
					dist = rep1
				} else {
					if d.bit(&m.isRepG2[state]) == 0 {
						dist = rep2
					} else {
						dist, rep3 = rep3, rep2
					}
					rep2 = rep1
				}
				rep1, rep0 = rep0, dist
			}
			l = d.length(&m.repLength, posState)
			state = stateAfterRep(state)
		}
		length := int(l) + minMatch
		if int(rep0) >= pos || length > n-pos {
			return ErrCorrupt
		}
		from := pos - int(rep0) - 1
		if int(rep0) >= length {
			copy(dst[pos:pos+length], dst[from:from+length])
		} else {
			for i := range length {
				dst[pos+i] = dst[from+i]
			}
		}
		pos += length
	}
	if d.i > len(src) {
		return ErrCorrupt
	}
	return nil
}
