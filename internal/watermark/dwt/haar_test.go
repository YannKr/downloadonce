package dwt_test

import (
	"math"
	"math/rand"
	"testing"

	"github.com/YannKr/downloadonce/internal/watermark/dwt"
)

const epsilon = 1e-10

func makeRandom(h, w int, rng *rand.Rand) [][]float64 {
	src := make([][]float64, h)
	for y := 0; y < h; y++ {
		src[y] = make([]float64, w)
		for x := 0; x < w; x++ {
			src[y][x] = rng.Float64()*512.0 - 256.0
		}
	}
	return src
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

func TestRoundTrip8x8(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	src := makeRandom(8, 8, rng)
	ll, lh, hl, hh := dwt.Forward2D(src)
	rec := dwt.Inverse2D(ll, lh, hl, hh)
	if d := maxAbsDiff(src, rec); d > epsilon {
		t.Errorf("8x8 round-trip max diff = %e, want < %e", d, epsilon)
	}
}

func TestRoundTrip64x64(t *testing.T) {
	rng := rand.New(rand.NewSource(1337))
	src := makeRandom(64, 64, rng)
	ll, lh, hl, hh := dwt.Forward2D(src)
	rec := dwt.Inverse2D(ll, lh, hl, hh)
	if d := maxAbsDiff(src, rec); d > epsilon {
		t.Errorf("64x64 round-trip max diff = %e, want < %e", d, epsilon)
	}
}

func TestRoundTrip256x256(t *testing.T) {
	rng := rand.New(rand.NewSource(999))
	src := makeRandom(256, 256, rng)
	ll, lh, hl, hh := dwt.Forward2D(src)
	rec := dwt.Inverse2D(ll, lh, hl, hh)
	if d := maxAbsDiff(src, rec); d > epsilon {
		t.Errorf("256x256 round-trip max diff = %e, want < %e", d, epsilon)
	}
}

func TestRoundTripNonSquare(t *testing.T) {
	rng := rand.New(rand.NewSource(7777))
	// 128 rows x 64 cols
	src := makeRandom(128, 64, rng)
	ll, lh, hl, hh := dwt.Forward2D(src)
	if len(ll) != 64 || len(ll[0]) != 32 {
		t.Fatalf("unexpected LL size: %dx%d, want 64x32", len(ll), len(ll[0]))
	}
	rec := dwt.Inverse2D(ll, lh, hl, hh)
	if d := maxAbsDiff(src, rec); d > epsilon {
		t.Errorf("128x64 round-trip max diff = %e, want < %e", d, epsilon)
	}
}

func TestSubbandSizes(t *testing.T) {
	src := makeRandom(16, 32, rand.New(rand.NewSource(0)))
	ll, lh, hl, hh := dwt.Forward2D(src)
	for name, s := range map[string][][]float64{"LL": ll, "LH": lh, "HL": hl, "HH": hh} {
		if len(s) != 8 || len(s[0]) != 16 {
			t.Errorf("subband %s: got %dx%d, want 8x16", name, len(s), len(s[0]))
		}
	}
}

// TestKnownValues verifies that a simple 4x4 constant matrix produces an LL
// subband whose values equal the original (since avg of equal values is the same).
func TestKnownValues(t *testing.T) {
	src := [][]float64{
		{4, 4, 4, 4},
		{4, 4, 4, 4},
		{4, 4, 4, 4},
		{4, 4, 4, 4},
	}
	ll, lh, hl, hh := dwt.Forward2D(src)

	// For a constant image, LL should equal the original values (avg of equal=same).
	for y := range ll {
		for x := range ll[y] {
			if math.Abs(ll[y][x]-4.0) > epsilon {
				t.Errorf("LL[%d][%d] = %v, want 4.0", y, x, ll[y][x])
			}
		}
	}
	// All detail subbands should be zero.
	for y := range lh {
		for x := range lh[y] {
			if math.Abs(lh[y][x]) > epsilon {
				t.Errorf("LH[%d][%d] = %v, want 0", y, x, lh[y][x])
			}
			if math.Abs(hl[y][x]) > epsilon {
				t.Errorf("HL[%d][%d] = %v, want 0", y, x, hl[y][x])
			}
			if math.Abs(hh[y][x]) > epsilon {
				t.Errorf("HH[%d][%d] = %v, want 0", y, x, hh[y][x])
			}
		}
	}
}
