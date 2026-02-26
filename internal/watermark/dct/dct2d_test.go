package dct_test

import (
	"math"
	"math/rand"
	"testing"

	"github.com/YannKr/downloadonce/internal/watermark/dct"
)

const roundTripEpsilon = 1e-9

func makeBlock(rows, cols int, rng *rand.Rand) [][]float64 {
	b := make([][]float64, rows)
	for y := 0; y < rows; y++ {
		b[y] = make([]float64, cols)
		for x := 0; x < cols; x++ {
			b[y][x] = rng.Float64()*512.0 - 256.0
		}
	}
	return b
}

func maxAbsDiff(a, b [][]float64) float64 {
	max := 0.0
	for y := range a {
		for x := range a[y] {
			d := math.Abs(a[y][x] - b[y][x])
			if d > max {
				max = d
			}
		}
	}
	return max
}

func TestRoundTrip4x4(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	b := makeBlock(4, 4, rng)
	rec := dct.Inverse2D(dct.Forward2D(b))
	if d := maxAbsDiff(b, rec); d > roundTripEpsilon {
		t.Errorf("4x4 round-trip max diff = %e, want < %e", d, roundTripEpsilon)
	}
}

func TestRoundTrip8x8(t *testing.T) {
	rng := rand.New(rand.NewSource(1337))
	b := makeBlock(8, 8, rng)
	rec := dct.Inverse2D(dct.Forward2D(b))
	if d := maxAbsDiff(b, rec); d > roundTripEpsilon {
		t.Errorf("8x8 round-trip max diff = %e, want < %e", d, roundTripEpsilon)
	}
}

func TestRoundTrip64x64(t *testing.T) {
	rng := rand.New(rand.NewSource(99999))
	b := makeBlock(64, 64, rng)
	rec := dct.Inverse2D(dct.Forward2D(b))
	if d := maxAbsDiff(b, rec); d > roundTripEpsilon {
		t.Errorf("64x64 round-trip max diff = %e, want < %e", d, roundTripEpsilon)
	}
}

// TestKnown4x4 checks that a known flat 4x4 input produces the expected DCT.
// For a constant input x[n] = c, the DCT-II gives X[0] = c * N * scale(0) = c*sqrt(N),
// and X[k>0] = 0.
func TestKnown4x4Constant(t *testing.T) {
	const c = 10.0
	const N = 4
	b := make([][]float64, N)
	for y := 0; y < N; y++ {
		b[y] = make([]float64, N)
		for x := 0; x < N; x++ {
			b[y][x] = c
		}
	}
	out := dct.Forward2D(b)

	// X[0][0] should be c * N * scale(0) * N * scale(0) = c * N
	// (each 1D pass: sum of c * cos(0) * scale(0) = c * N * sqrt(1/N) = c*sqrt(N))
	// 2D: c * sqrt(N) * sqrt(N) = c * N
	wantDC := c * float64(N)
	if math.Abs(out[0][0]-wantDC) > 1e-9 {
		t.Errorf("DC coefficient = %v, want %v", out[0][0], wantDC)
	}
	// All other coefficients should be zero.
	for y := 0; y < N; y++ {
		for x := 0; x < N; x++ {
			if y == 0 && x == 0 {
				continue
			}
			if math.Abs(out[y][x]) > 1e-9 {
				t.Errorf("out[%d][%d] = %v, want ~0 for constant input", y, x, out[y][x])
			}
		}
	}
}

// TestKnown4x4Reference checks that a specific input round-trips correctly and
// verifies the DC coefficient matches the analytical formula: X[0][0] = sqrt(N) * mean(x).
// For a separable orthonormal DCT-II with N=4: X[0][0] = (1/N) * sum(x) = 0.25 * sum.
func TestKnown4x4Reference(t *testing.T) {
	input := [][]float64{
		{16, 11, 10, 16},
		{12, 12, 14, 19},
		{14, 13, 16, 24},
		{14, 17, 22, 29},
	}
	// DC coefficient check: X[0][0] = scale(0)^2 * sum = (1/N) * sum = 0.25 * 259 = 64.75
	sumAll := 0.0
	for _, row := range input {
		for _, v := range row {
			sumAll += v
		}
	}
	expectedDC := sumAll / float64(4) // = 0.25 * 259 = 64.75

	out := dct.Forward2D(input)
	if math.Abs(out[0][0]-expectedDC) > 1e-9 {
		t.Errorf("DC out[0][0] = %v, want %v (analytical)", out[0][0], expectedDC)
	}

	// Round-trip verification.
	rec := dct.Inverse2D(out)
	if d := maxAbsDiff(input, rec); d > roundTripEpsilon {
		t.Errorf("4x4 reference round-trip max diff = %e, want < %e", d, roundTripEpsilon)
	}
}
