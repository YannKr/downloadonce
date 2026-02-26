package watermark

// GoInvisibleImageEmbed and GoInvisibleImageDetect implement the DWT-DCT-SVD
// invisible watermarking algorithm in pure Go, matching the Python imwatermark
// library's dwtDctSvd mode.
//
// Algorithm summary (matching imwatermark EmbedDwtDctSvd):
//  1. Convert BGR image to YUV (OpenCV convention, uint8 range).
//  2. Process channel 1 (U) with scale=36 (channels 0 and 2 are skipped).
//  3. Trim image to dimensions divisible by 4 (row//4*4, col//4*4).
//  4. Apply single-level 2D Haar DWT to the (trimmed) channel → LL subband.
//  5. For each 4x4 block in the LL subband (row-major):
//     a. Apply 2D DCT (cv2.dct equivalent — OpenCV uses Type-II unnormalized DCT).
//     b. Apply SVD to the DCT block.
//     c. Embed: s[0] = (s[0]//scale + 0.25 + 0.5*wmBit) * scale
//        wmBit = watermarks[num % wmLen]  (bits cycle across all blocks)
//     d. Reconstruct block from modified SVD.
//     e. Apply inverse DCT.
//  6. Apply inverse DWT with modified LL and original LH/HL/HH.
//  7. Convert YUV back to BGR, clamp to [0,255], write output.
//
// Detection reverses steps 1–5:
//   - For each block, read score = int((s[0] % scale) > scale*0.5)
//   - Accumulate scores per bit position, average, threshold at 0.5.
//
// Note on OpenCV DCT normalization: cv2.dct uses the orthonormal Type-II DCT
// which matches scipy.fft.dctn with norm='ortho'. Our dct.Forward2D/Inverse2D
// implement this same convention.

import (
	"context"
	"encoding/hex"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"

	"gonum.org/v1/gonum/mat"

	"github.com/YannKr/downloadonce/internal/watermark/dct"
	"github.com/YannKr/downloadonce/internal/watermark/dwt"
)

const (
	// wmScale is the embedding strength (alpha) matching imwatermark's default
	// scales=[0,36,0] where channel 1 (U in YUV) uses scale 36.
	wmScale = 36.0
	// wmBlockSize is the 4x4 SVD block size used in the dwtDctSvd algorithm.
	wmBlockSize = 4
)

// GoInvisibleImageEmbed embeds a DWT-DCT-SVD invisible watermark into an image
// file, matching the Python imwatermark library's dwtDctSvd encoding.
//
// inputPath must be JPEG, PNG, or WebP.
// outputPath extension determines the output format (JPEG recommended).
// payloadHex is the 32-character hex string (16 bytes = 128 bits).
// jpegQuality is the JPEG quality for the output file (e.g., 92).
func GoInvisibleImageEmbed(ctx context.Context, inputPath, outputPath, payloadHex string, jpegQuality int) error {
	// Convert payloadHex to bit array (MSB first within each byte).
	bits, err := hexToBits(payloadHex)
	if err != nil {
		return fmt.Errorf("go invisible embed: invalid payload hex: %w", err)
	}
	wmLen := len(bits)

	// Load image to NRGBA.
	img, err := loadImageNRGBA(inputPath)
	if err != nil {
		return fmt.Errorf("go invisible embed: load image: %w", err)
	}

	bounds := img.Bounds()
	fullH := bounds.Dy()
	fullW := bounds.Dx()

	// Trim to divisible by 4 (matching Python: row//4*4, col//4*4).
	h := (fullH / 4) * 4
	w := (fullW / 4) * 4
	if h < 8 || w < 8 {
		return fmt.Errorf("go invisible embed: image too small (%dx%d), need at least 8x8", fullH, fullW)
	}

	// Minimum size: need at least wmLen blocks of 4x4 in the LL subband.
	// LL is [h/2][w/2]. Number of 4x4 blocks = (h/2/4)*(w/2/4) = (h*w/64).
	// We need >= wmLen blocks.
	numBlocks := (h / 2 / wmBlockSize) * (w / 2 / wmBlockSize)
	if numBlocks < wmLen {
		return fmt.Errorf("go invisible embed: image too small (%dx%d trimmed to %dx%d), only %d blocks available for %d bits",
			fullH, fullW, h, w, numBlocks, wmLen)
	}

	// Extract pixels as YUV float64 planes for the trimmed region.
	yPlane, uPlane, vPlane := extractYUVPlanes(img, h, w)

	// Process U channel (channel index 1 in YUV) with scale 36.
	modifiedU, err := embedChannelDwtDctSvd(uPlane, bits, wmLen, wmScale)
	if err != nil {
		return fmt.Errorf("go invisible embed: %w", err)
	}

	// Reconstruct image with modified U channel.
	out := image.NewNRGBA(bounds)
	// Copy original pixels first.
	draw.Draw(out, bounds, img, bounds.Min, draw.Src)
	// Overwrite the trimmed region with modified YUV.
	putYUVPlanes(out, yPlane, modifiedU, vPlane, h, w)

	return saveImage(out, outputPath, jpegQuality)
}

// GoInvisibleImageDetect extracts the DWT-DCT-SVD watermark from an image file.
// payloadLengthBytes is the number of payload bytes to extract (e.g., PayloadLength = 16).
// Returns the hex-encoded payload.
func GoInvisibleImageDetect(ctx context.Context, inputPath string, payloadLengthBytes int) (string, error) {
	wmLen := payloadLengthBytes * 8

	img, err := loadImageNRGBA(inputPath)
	if err != nil {
		return "", fmt.Errorf("go invisible detect: load image: %w", err)
	}

	bounds := img.Bounds()
	fullH := bounds.Dy()
	fullW := bounds.Dx()
	h := (fullH / 4) * 4
	w := (fullW / 4) * 4
	if h < 8 || w < 8 {
		return "", fmt.Errorf("go invisible detect: image too small")
	}

	_, uPlane, _ := extractYUVPlanes(img, h, w)

	bits, err := detectChannelDwtDctSvd(uPlane, wmLen, wmScale)
	if err != nil {
		return "", fmt.Errorf("go invisible detect: %w", err)
	}

	payload := bitsToBytes(bits)
	return hex.EncodeToString(payload), nil
}

// embedChannelDwtDctSvd applies the full DWT-DCT-SVD embed pipeline to a single
// float64 channel plane (h x w).
func embedChannelDwtDctSvd(plane [][]float64, bits []int, wmLen int, scale float64) ([][]float64, error) {
	// Apply 2D Haar DWT.
	ll, lh, hl, hh := dwt.Forward2D(plane)

	// Embed bits into 4x4 blocks of LL via per-block DCT + SVD.
	llH := len(ll)
	llW := len(ll[0])
	num := 0
	for i := 0; i < llH/wmBlockSize; i++ {
		for j := 0; j < llW/wmBlockSize; j++ {
			block := extractBlock(ll, i*wmBlockSize, j*wmBlockSize, wmBlockSize)
			wmBit := bits[num%wmLen]

			embedded := embedBlockDctSvd(block, wmBit, scale)
			putBlock(ll, embedded, i*wmBlockSize, j*wmBlockSize, wmBlockSize)
			num++
		}
	}

	// Apply inverse DWT.
	return dwt.Inverse2D(ll, lh, hl, hh), nil
}

// detectChannelDwtDctSvd applies the full DWT-DCT-SVD detect pipeline to a single
// float64 channel plane. Returns a bit slice of length wmLen.
func detectChannelDwtDctSvd(plane [][]float64, wmLen int, scale float64) ([]int, error) {
	ll, _, _, _ := dwt.Forward2D(plane)

	llH := len(ll)
	llW := len(ll[0])

	// Accumulate scores for each bit position.
	scores := make([][]float64, wmLen)
	for i := range scores {
		scores[i] = make([]float64, 0)
	}

	num := 0
	for i := 0; i < llH/wmBlockSize; i++ {
		for j := 0; j < llW/wmBlockSize; j++ {
			block := extractBlock(ll, i*wmBlockSize, j*wmBlockSize, wmBlockSize)
			score := inferBlockDctSvd(block, scale)
			wmBit := num % wmLen
			scores[wmBit] = append(scores[wmBit], score)
			num++
		}
	}

	// Average scores and threshold at 0.5.
	bits := make([]int, wmLen)
	for k := 0; k < wmLen; k++ {
		if len(scores[k]) == 0 {
			bits[k] = 0
			continue
		}
		avg := 0.0
		for _, s := range scores[k] {
			avg += s
		}
		avg /= float64(len(scores[k]))
		// Python: bits = (np.array(avgScores) * 255 > 127)
		if avg*255 > 127 {
			bits[k] = 1
		} else {
			bits[k] = 0
		}
	}
	return bits, nil
}

// embedBlockDctSvd applies DCT, embeds one bit via SVD modification, then
// applies inverse DCT. Matches Python's diffuse_dct_svd method.
//
// Python: u,s,v = np.linalg.svd(cv2.dct(block))
//
//	s[0] = (s[0] // scale + 0.25 + 0.5 * wmBit) * scale
//	return cv2.idct(np.dot(u, np.dot(np.diag(s), v)))
func embedBlockDctSvd(block [][]float64, wmBit int, scale float64) [][]float64 {
	n := wmBlockSize
	dctBlock := dct.Forward2D(block)

	// Flatten for gonum mat.
	data := make([]float64, n*n)
	for i, row := range dctBlock {
		copy(data[i*n:], row)
	}
	m := mat.NewDense(n, n, data)

	var svdDecomp mat.SVD
	svdDecomp.Factorize(m, mat.SVDThin)
	s := svdDecomp.Values(nil)

	// Quantization embedding matching Python:
	// s[0] = (s[0] // scale + 0.25 + 0.5 * wmBit) * scale
	s[0] = (math.Floor(s[0]/scale) + 0.25 + 0.5*float64(wmBit)) * scale

	// Reconstruct: U * diag(s) * V^T.
	var u, v mat.Dense
	svdDecomp.UTo(&u)
	svdDecomp.VTo(&v) // gonum returns V; we need V^T, so use v.T()

	diagS := mat.NewDiagDense(n, s)
	var tmp, result mat.Dense
	tmp.Mul(&u, diagS)
	result.Mul(&tmp, v.T())

	// Apply inverse DCT.
	modified := make([][]float64, n)
	for i := range modified {
		modified[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			modified[i][j] = result.At(i, j)
		}
	}
	return dct.Inverse2D(modified)
}

// inferBlockDctSvd reads the watermark score from a block. Matches Python's
// infer_dct_svd method.
//
// Python: u,s,v = np.linalg.svd(cv2.dct(block))
//
//	score = int((s[0] % scale) > scale * 0.5)
func inferBlockDctSvd(block [][]float64, scale float64) float64 {
	n := wmBlockSize
	dctBlock := dct.Forward2D(block)

	data := make([]float64, n*n)
	for i, row := range dctBlock {
		copy(data[i*n:], row)
	}
	m := mat.NewDense(n, n, data)

	var svdDecomp mat.SVD
	svdDecomp.Factorize(m, mat.SVDThin)
	s := svdDecomp.Values(nil)

	mod := math.Mod(s[0], scale)
	// Handle negative modulo (Go's math.Mod can return negative for negative s[0]).
	if mod < 0 {
		mod += scale
	}
	if mod > scale*0.5 {
		return 1.0
	}
	return 0.0
}

// extractBlock extracts a wmBlockSize x wmBlockSize block from a 2D slice.
func extractBlock(plane [][]float64, row, col, size int) [][]float64 {
	block := make([][]float64, size)
	for i := 0; i < size; i++ {
		block[i] = make([]float64, size)
		copy(block[i], plane[row+i][col:col+size])
	}
	return block
}

// putBlock writes a wmBlockSize x wmBlockSize block back into a 2D slice.
func putBlock(plane [][]float64, block [][]float64, row, col, size int) {
	for i := 0; i < size; i++ {
		copy(plane[row+i][col:col+size], block[i])
	}
}

// extractYUVPlanes extracts Y, U, V float64 planes from an NRGBA image.
// Conversion matches OpenCV's COLOR_BGR2YUV formula (applied to RGB):
//
//	Y =  0.299*R + 0.587*G + 0.114*B
//	U = -0.14713*R - 0.28886*G + 0.436*B + 128
//	V =  0.615*R - 0.51499*G - 0.10001*B + 128
//
// Only the first h rows and w columns are extracted (measured from the image
// origin, i.e., bounds.Min).
func extractYUVPlanes(img *image.NRGBA, h, w int) (yPlane, uPlane, vPlane [][]float64) {
	minX := img.Rect.Min.X
	minY := img.Rect.Min.Y
	yPlane = make([][]float64, h)
	uPlane = make([][]float64, h)
	vPlane = make([][]float64, h)
	for y := 0; y < h; y++ {
		yPlane[y] = make([]float64, w)
		uPlane[y] = make([]float64, w)
		vPlane[y] = make([]float64, w)
		for x := 0; x < w; x++ {
			off := img.PixOffset(minX+x, minY+y)
			r := float64(img.Pix[off])
			g := float64(img.Pix[off+1])
			b := float64(img.Pix[off+2])

			yPlane[y][x] = 0.299*r + 0.587*g + 0.114*b
			uPlane[y][x] = -0.14713*r - 0.28886*g + 0.436*b + 128.0
			vPlane[y][x] = 0.615*r - 0.51499*g - 0.10001*b + 128.0
		}
	}
	return
}

// putYUVPlanes writes modified YUV planes back to an NRGBA image.
// Only writes the first h rows and w columns (measured from bounds.Min);
// the rest of the image is untouched (already copied from source).
func putYUVPlanes(img *image.NRGBA, yPlane, uPlane, vPlane [][]float64, h, w int) {
	minX := img.Rect.Min.X
	minY := img.Rect.Min.Y
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			yv := yPlane[y][x]
			uv := uPlane[y][x]
			vv := vPlane[y][x]

			// Inverse YUV to RGB (OpenCV COLOR_YUV2BGR inverse).
			r := yv + 1.13983*(vv-128.0)
			g := yv - 0.39465*(uv-128.0) - 0.58060*(vv-128.0)
			b := yv + 2.03211*(uv-128.0)

			off := img.PixOffset(minX+x, minY+y)
			img.Pix[off] = clampU8(r)
			img.Pix[off+1] = clampU8(g)
			img.Pix[off+2] = clampU8(b)
			// Alpha unchanged.
		}
	}
}

// clampU8 clamps a float64 to [0, 255] and converts to uint8.
func clampU8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(math.Round(v))
}

// loadImageNRGBA opens an image file (JPEG or PNG) and returns it as
// *image.NRGBA with all color models normalized to RGBA.
// WebP images must first be converted to JPEG or PNG by the caller
// (the existing ImageMagick visible-watermark step handles this).
func loadImageNRGBA(path string) (*image.NRGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(path))
	var decoded image.Image
	switch ext {
	case ".jpg", ".jpeg":
		decoded, err = jpeg.Decode(f)
	case ".png":
		decoded, err = png.Decode(f)
	default:
		// Try auto-detect for any other format registered in image package.
		decoded, _, err = image.Decode(f)
	}
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	bounds := decoded.Bounds()
	nrgba := image.NewNRGBA(bounds)
	draw.Draw(nrgba, bounds, decoded, bounds.Min, draw.Src)
	return nrgba, nil
}

// saveImage saves an NRGBA image to disk. Format is determined by outputPath extension.
func saveImage(img *image.NRGBA, outputPath string, jpegQuality int) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(outputPath))
	switch ext {
	case ".jpg", ".jpeg":
		return jpeg.Encode(f, img, &jpeg.Options{Quality: jpegQuality})
	case ".png":
		return png.Encode(f, img)
	default:
		return jpeg.Encode(f, img, &jpeg.Options{Quality: jpegQuality})
	}
}

// hexToBits converts a hex string to a bit slice, MSB first within each byte.
func hexToBits(hexStr string) ([]int, error) {
	b, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, err
	}
	bits := make([]int, len(b)*8)
	for i, byt := range b {
		for bit := 0; bit < 8; bit++ {
			// MSB first: bit 7 is first.
			if byt&(1<<uint(7-bit)) != 0 {
				bits[i*8+bit] = 1
			} else {
				bits[i*8+bit] = 0
			}
		}
	}
	return bits, nil
}

// bitsToBytes packs a bit slice (MSB first) into bytes.
func bitsToBytes(bits []int) []byte {
	nBytes := (len(bits) + 7) / 8
	out := make([]byte, nBytes)
	for i, b := range bits {
		if b != 0 {
			out[i/8] |= 1 << uint(7-(i%8))
		}
	}
	return out
}
