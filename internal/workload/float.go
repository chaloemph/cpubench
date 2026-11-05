package workload

import (
	"math"
	"math/bits"
	"math/cmplx"

	"github.com/chaloemph/cpubench/internal/domain/benchmark"
)

// ---- float/matmul ----

type matmulWL struct {
	n       int
	a, b, c [][]float64
}

func (*matmulWL) Info() benchmark.Info {
	return benchmark.Info{Name: "float/matmul", Category: benchmark.Float, Kind: benchmark.Throughput,
		Unit: "MFLOP/s", UnitScale: 1e-6, Description: "dense 384x384 float64 matrix multiply (i-k-j loop)"}
}

func (w *matmulWL) Setup(threads int, scale float64) error {
	w.n = max(32, int(384*math.Cbrt(scale)))
	n := w.n
	w.a, w.b, w.c = make([][]float64, threads), make([][]float64, threads), make([][]float64, threads)
	for t := range threads {
		r := rng(200 + t)
		w.a[t], w.b[t], w.c[t] = make([]float64, n*n), make([]float64, n*n), make([]float64, n*n)
		for i := range n * n {
			w.a[t][i] = r.Float64() - 0.5
			w.b[t][i] = r.Float64() - 0.5
		}
	}
	return nil
}

func (w *matmulWL) RunThread(id int) (float64, uint64) {
	n, a, b, c := w.n, w.a[id], w.b[id], w.c[id]
	clear(c)
	for i := range n {
		ci := c[i*n : i*n+n]
		for k := range n {
			aik := a[i*n+k]
			bk := b[k*n : k*n+n]
			for j := range ci {
				ci[j] += aik * bk[j]
			}
		}
	}
	var tr float64
	for i := range n {
		tr += c[i*n+i]
	}
	return 2 * float64(n) * float64(n) * float64(n), fsum(0, tr)
}

func (w *matmulWL) Teardown() { w.a, w.b, w.c = nil, nil, nil }

// ---- float/nbody ----

type nbodyWL struct {
	n     int
	steps int
	pos   [][]float64 // x,y,z per body
	vel   [][]float64
	init  []float64
}

func (*nbodyWL) Info() benchmark.Info {
	return benchmark.Info{Name: "float/nbody", Category: benchmark.Float, Kind: benchmark.Throughput,
		Unit: "Minteract/s", UnitScale: 1e-6, Description: "O(n^2) gravity step, 1024 bodies (sqrt, div)"}
}

func (w *nbodyWL) Setup(threads int, scale float64) error {
	w.n, w.steps = 1024, scaled(2, scale)
	r := rng(300)
	w.init = make([]float64, 3*w.n)
	for i := range w.init {
		w.init[i] = r.Float64()*100 - 50
	}
	w.pos, w.vel = make([][]float64, threads), make([][]float64, threads)
	for t := range threads {
		w.pos[t] = make([]float64, 3*w.n)
		w.vel[t] = make([]float64, 3*w.n)
	}
	return nil
}

func (w *nbodyWL) RunThread(id int) (float64, uint64) {
	p, v, n := w.pos[id], w.vel[id], w.n
	copy(p, w.init)
	clear(v)
	const dt, soft = 0.01, 0.01
	for range w.steps {
		for i := range n {
			xi, yi, zi := p[3*i], p[3*i+1], p[3*i+2]
			var ax, ay, az float64
			for j := range n {
				dx, dy, dz := p[3*j]-xi, p[3*j+1]-yi, p[3*j+2]-zi
				d2 := dx*dx + dy*dy + dz*dz + soft
				inv := 1 / (d2 * math.Sqrt(d2))
				ax += dx * inv
				ay += dy * inv
				az += dz * inv
			}
			v[3*i] += ax * dt
			v[3*i+1] += ay * dt
			v[3*i+2] += az * dt
		}
		for i := range p {
			p[i] += v[i] * dt
		}
	}
	return float64(w.steps * n * n), fsum(fsum(0, p[0]), p[len(p)-1])
}

func (w *nbodyWL) Teardown() { w.pos, w.vel, w.init = nil, nil, nil }

// ---- float/fft ----

type fftWL struct {
	n, reps int
	in      []complex128
	buf     [][]complex128
}

func (*fftWL) Info() benchmark.Info {
	return benchmark.Info{Name: "float/fft", Category: benchmark.Float, Kind: benchmark.Throughput,
		Unit: "MFLOP/s", UnitScale: 1e-6, Description: "radix-2 complex FFT, 64k points, forward + inverse"}
}

func (w *fftWL) Setup(threads int, scale float64) error {
	w.n, w.reps = 1<<16, scaled(8, scale)
	r := rng(400)
	w.in = make([]complex128, w.n)
	for i := range w.in {
		w.in[i] = complex(r.Float64(), r.Float64())
	}
	w.buf = make([][]complex128, threads)
	for t := range threads {
		w.buf[t] = make([]complex128, w.n)
	}
	return nil
}

func fft(a []complex128, invert bool) {
	n := len(a)
	shift := 64 - bits.Len(uint(n)) + 1
	for i := range n {
		j := int(bits.Reverse64(uint64(i)) >> shift)
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		ang := 2 * math.Pi / float64(size)
		if invert {
			ang = -ang
		}
		wl := cmplx.Rect(1, ang)
		half := size / 2
		for start := 0; start < n; start += size {
			wn := complex(1, 0)
			for k := range half {
				u, v := a[start+k], a[start+k+half]*wn
				a[start+k], a[start+k+half] = u+v, u-v
				wn *= wl
			}
		}
	}
	if invert {
		inv := complex(1/float64(n), 0)
		for i := range a {
			a[i] *= inv
		}
	}
}

func (w *fftWL) RunThread(id int) (float64, uint64) {
	b := w.buf[id]
	for range w.reps {
		copy(b, w.in)
		fft(b, false)
		fft(b, true)
	}
	logn := float64(bits.Len(uint(w.n)) - 1)
	return float64(w.reps) * 2 * 5 * float64(w.n) * logn, fsum(fsum(0, real(b[1])), imag(b[w.n-1]))
}

func (w *fftWL) Teardown() { w.in, w.buf = nil, nil }
