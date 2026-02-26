// Package dct implements a 2D Type-II DCT and its inverse (Type-III).
//
// The 1D DCT-II formula (orthonormal, matching scipy/numpy/cv2 convention):
//
//	X[k] = scale(k) * sum_{n=0}^{N-1} x[n] * cos(pi * k * (2n+1) / (2N))
//	scale(0) = sqrt(1/N), scale(k>0) = sqrt(2/N)
//
// The 2D DCT-II is separable: apply 1D DCT-II to each row, then to each
// column of the result. The inverse is the 2D DCT-III (apply 1D DCT-III to
// each column, then to each row).
package dct

import (
	"math"
)

// forward1D applies the 1D Type-II DCT (orthonormal) to x.
func forward1D(x []float64) []float64 {
	n := len(x)
	out := make([]float64, n)
	scale0 := math.Sqrt(1.0 / float64(n))
	scaleK := math.Sqrt(2.0 / float64(n))
	for k := 0; k < n; k++ {
		scale := scaleK
		if k == 0 {
			scale = scale0
		}
		sum := 0.0
		for i := 0; i < n; i++ {
			sum += x[i] * math.Cos(math.Pi*float64(k)*float64(2*i+1)/(2.0*float64(n)))
		}
		out[k] = scale * sum
	}
	return out
}

// inverse1D applies the 1D Type-III DCT (inverse of Type-II, orthonormal) to X.
func inverse1D(X []float64) []float64 {
	n := len(X)
	out := make([]float64, n)
	scale0 := math.Sqrt(1.0 / float64(n))
	scaleK := math.Sqrt(2.0 / float64(n))
	for i := 0; i < n; i++ {
		sum := scale0 * X[0]
		for k := 1; k < n; k++ {
			sum += scaleK * X[k] * math.Cos(math.Pi*float64(k)*float64(2*i+1)/(2.0*float64(n)))
		}
		out[i] = sum
	}
	return out
}

// Forward2D applies the 2D Type-II DCT to a rectangular block.
// The input need not be square; the block dimensions determine the transform size.
// Returns a new block of the same dimensions.
func Forward2D(block [][]float64) [][]float64 {
	rows := len(block)
	cols := len(block[0])

	// Apply 1D DCT-II to each row.
	rowOut := make([][]float64, rows)
	for y := 0; y < rows; y++ {
		rowOut[y] = forward1D(block[y])
	}

	// Apply 1D DCT-II to each column of the row-transformed result.
	out := make([][]float64, rows)
	for y := 0; y < rows; y++ {
		out[y] = make([]float64, cols)
	}
	for x := 0; x < cols; x++ {
		col := make([]float64, rows)
		for y := 0; y < rows; y++ {
			col[y] = rowOut[y][x]
		}
		transCol := forward1D(col)
		for y := 0; y < rows; y++ {
			out[y][x] = transCol[y]
		}
	}
	return out
}

// Inverse2D applies the 2D Type-III DCT (inverse of Forward2D) to a rectangular block.
// Returns a new block of the same dimensions.
func Inverse2D(block [][]float64) [][]float64 {
	rows := len(block)
	cols := len(block[0])

	// Apply 1D DCT-III to each column first.
	colOut := make([][]float64, rows)
	for y := 0; y < rows; y++ {
		colOut[y] = make([]float64, cols)
	}
	for x := 0; x < cols; x++ {
		col := make([]float64, rows)
		for y := 0; y < rows; y++ {
			col[y] = block[y][x]
		}
		invCol := inverse1D(col)
		for y := 0; y < rows; y++ {
			colOut[y][x] = invCol[y]
		}
	}

	// Apply 1D DCT-III to each row.
	out := make([][]float64, rows)
	for y := 0; y < rows; y++ {
		out[y] = inverse1D(colOut[y])
	}
	return out
}
