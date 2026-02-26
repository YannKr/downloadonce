// Package dwt implements a single-level 2D Haar Discrete Wavelet Transform.
package dwt

// forward1D applies the Haar forward transform to a row of length N (must be even).
// avg[i] = (src[2i] + src[2i+1]) / 2, diff[i] = (src[2i] - src[2i+1]) / 2.
// Returns a new slice: first N/2 elements are averages, next N/2 are differences.
func forward1D(src []float64) []float64 {
	n := len(src)
	out := make([]float64, n)
	half := n / 2
	for i := 0; i < half; i++ {
		out[i] = (src[2*i] + src[2*i+1]) / 2.0
		out[half+i] = (src[2*i] - src[2*i+1]) / 2.0
	}
	return out
}

// inverse1D reconstructs a row from Haar coefficients.
// src is [avg0..avgN/2-1, diff0..diffN/2-1].
func inverse1D(src []float64) []float64 {
	n := len(src)
	half := n / 2
	out := make([]float64, n)
	for i := 0; i < half; i++ {
		avg := src[i]
		diff := src[half+i]
		out[2*i] = avg + diff
		out[2*i+1] = avg - diff
	}
	return out
}

// Forward2D applies a single-level 2D Haar DWT to src.
// src must be a rectangular [][]float64 with even dimensions (h rows, w cols).
// Returns four subbands LL, LH, HL, HH each of size [h/2][w/2].
//
// Subband layout in the transform domain:
//
//	[ LL | LH ]
//	[ HL | HH ]
//
// The transform applies forward1D to each row, then to each column of the
// intermediate result.
func Forward2D(src [][]float64) (ll, lh, hl, hh [][]float64) {
	h := len(src)
	w := len(src[0])
	halfH := h / 2
	halfW := w / 2

	// Step 1: apply 1D forward transform to each row.
	rowTrans := make([][]float64, h)
	for y := 0; y < h; y++ {
		rowTrans[y] = forward1D(src[y])
	}

	// Step 2: apply 1D forward transform to each column of rowTrans.
	// Build column-by-column.
	full := make([][]float64, h)
	for y := 0; y < h; y++ {
		full[y] = make([]float64, w)
	}
	for x := 0; x < w; x++ {
		col := make([]float64, h)
		for y := 0; y < h; y++ {
			col[y] = rowTrans[y][x]
		}
		transCol := forward1D(col)
		for y := 0; y < h; y++ {
			full[y][x] = transCol[y]
		}
	}

	// Extract four subbands.
	ll = makeGrid(halfH, halfW)
	lh = makeGrid(halfH, halfW)
	hl = makeGrid(halfH, halfW)
	hh = makeGrid(halfH, halfW)

	for y := 0; y < halfH; y++ {
		for x := 0; x < halfW; x++ {
			ll[y][x] = full[y][x]
			lh[y][x] = full[y][halfW+x]
			hl[y][x] = full[halfH+y][x]
			hh[y][x] = full[halfH+y][halfW+x]
		}
	}
	return ll, lh, hl, hh
}

// Inverse2D reconstructs a 2D array from the four subbands produced by Forward2D.
// All subbands must be [h/2][w/2]; the result is [h][w].
func Inverse2D(ll, lh, hl, hh [][]float64) [][]float64 {
	halfH := len(ll)
	halfW := len(ll[0])
	h := halfH * 2
	w := halfW * 2

	// Assemble the full coefficient matrix.
	full := make([][]float64, h)
	for y := 0; y < h; y++ {
		full[y] = make([]float64, w)
	}
	for y := 0; y < halfH; y++ {
		for x := 0; x < halfW; x++ {
			full[y][x] = ll[y][x]
			full[y][halfW+x] = lh[y][x]
			full[halfH+y][x] = hl[y][x]
			full[halfH+y][halfW+x] = hh[y][x]
		}
	}

	// Step 1: inverse 1D on each column.
	colInv := make([][]float64, h)
	for y := 0; y < h; y++ {
		colInv[y] = make([]float64, w)
	}
	for x := 0; x < w; x++ {
		col := make([]float64, h)
		for y := 0; y < h; y++ {
			col[y] = full[y][x]
		}
		inv := inverse1D(col)
		for y := 0; y < h; y++ {
			colInv[y][x] = inv[y]
		}
	}

	// Step 2: inverse 1D on each row.
	out := make([][]float64, h)
	for y := 0; y < h; y++ {
		out[y] = inverse1D(colInv[y])
	}
	return out
}

// makeGrid allocates a 2D slice of float64 with the given dimensions.
func makeGrid(rows, cols int) [][]float64 {
	g := make([][]float64, rows)
	for i := range g {
		g[i] = make([]float64, cols)
	}
	return g
}
