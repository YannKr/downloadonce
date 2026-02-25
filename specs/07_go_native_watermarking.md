# Spec 07: Go-Native Invisible Image Watermarking

**Status:** Draft
**Date:** 2026-02-23
**Author:** Engineering
**Type:** R&D / Technical Debt Reduction

---

## 1. Problem Statement

The current invisible watermarking pipeline for images relies on a Python subprocess chain that is the single largest source of operational complexity and Docker image bloat in the project.

### 1.1 Concrete Costs

**Docker image size.** The runtime stage of `Dockerfile` currently installs:

```
python3, python3-pip, python3-venv          ~80 MB
opencv-python-headless                      ~200 MB
invisible-watermark (+ numpy, pywavelets)   ~80 MB
venv overhead                               ~50 MB
```

Total Python layer: approximately **410 MB**. The full runtime image lands around **1.2 GB**. Removing Python would bring the image to roughly **200 MB** (debian:trixie-slim + ffmpeg + imagemagick + fonts + the Go binary).

**Subprocess startup latency.** Each call to `InvisibleImageEmbed` or `InvisibleImageDetect` in `internal/watermark/invisible.go` forks a Python interpreter via `exec.CommandContext`. Python 3 interpreter cold-start is 200–600 ms depending on the host, before any image I/O begins. For a campaign with 50 recipients, that is 10–30 seconds of pure interpreter overhead added to the watermarking queue, in addition to the actual DWT-DCT computation time. This latency is dead time from the worker's perspective; the Go process simply waits for the subprocess to exit.

**Operational fragility.** The venv is provisioned at Docker build time and pinned to exact pip-resolved versions. Any change to the base image's system Python, an ABI mismatch between `opencv-python-headless` and the glibc version, or a corrupt venv volume results in a silent failure that falls back to visible-only watermarking (see `pool.go` line 219: `slog.Warn("invisible image embed failed, using visible-only output", ...)`). The fallback is correct behavior but the failure mode is opaque in production.

**Dockerfile complexity.** Two extra `RUN` layers are required purely for the Python stack. The `embed.go` root file embeds a `scripts/` FS that is extracted at startup to a temp directory (via `ScriptsDir` in `config.Config`). The extracted paths are threaded through the worker as `p.pythonPath()` and `p.embedScriptPath()`/`p.detectScriptPath()` — plumbing that would be entirely eliminated.

**CGO-free binary compromise.** The Go binary is built with `CGO_ENABLED=0` for a fully static binary. The Python dependency re-introduces a runtime native-library dependency chain through the back door (OpenCV links against system libstdc++, libgomp, etc.), which the static Go build was meant to avoid.

### 1.2 What the Python Scripts Actually Do

`scripts/embed_watermark.py` calls `imwatermark.WatermarkEncoder.encode(img, 'dwtDctSvd')`, and `scripts/detect_watermark.py` calls `imwatermark.WatermarkDecoder.decode(img, 'dwtDctSvd')`. The `invisible-watermark` Python library (`imwatermark`) implements the **DWT-DCT-SVD** algorithm — a three-stage frequency-domain embedding:

1. Apply a 2D Haar Discrete Wavelet Transform to the blue channel of the image.
2. Apply a 2D Discrete Cosine Transform to the LL (approximation) subband.
3. Apply Singular Value Decomposition (SVD) to blocks of the DCT coefficients and embed watermark bits by modifying the singular values.

The 16-byte payload from `internal/watermark/payload.go` (128 bits) is what gets embedded. Detection reverses the process: DWT → DCT → SVD → read back bit sequence.

Note: the Python scripts currently use `'dwtDctSvd'` (the SVD variant), not the plain `'dwtDct'` mode. This is important for the compatibility analysis in section 6.

---

## 2. Goals and Non-Goals

### Goals

- Implement the full DWT-DCT-SVD invisible watermark embed and detect pipeline in pure Go, eliminating all Python subprocess calls for image watermarking.
- Produce watermarked images that are at least as robust as the current Python output (survivable through JPEG re-compression at ≥75% quality, 50% downscale, and standard brightness/contrast edits).
- Maintain the existing 16-byte payload format (`payload.go`) with no changes to `BuildPayload`, `ParsePayload`, or `ParsePayloadFuzzy`.
- Reduce Docker image size from ~1.2 GB to ~200 MB.
- Eliminate subprocess startup latency from the watermarking worker.
- Implement a Go-native detection path so the system remains fully self-contained for new files.
- Provide a migration strategy for files watermarked by the old Python implementation.

### Non-Goals

- Replacing FFmpeg for video watermarking. FFmpeg is the correct tool for video re-encoding with `libx265` and the `drawtext` filter. It stays.
- Achieving cross-algorithm compatibility with other watermarking systems or standards beyond the existing `imwatermark` dwtDctSvd output.
- Watermarking formats other than JPEG, PNG, and WebP (already the only formats handled in `imagemagick.go`).
- Removing ImageMagick in this milestone (see section 10 for feasibility discussion — this is deferred but recommended as a follow-on).
- GPU acceleration.

---

## 3. Algorithm Options

Three approaches are evaluated. Each is assessed against the requirements: robustness to JPEG re-compression and geometric transforms, implementation complexity in pure Go, and compatibility with existing detection.

### Option A: Go DWT-DCT-SVD Implementation (Port of imwatermark dwtDctSvd)

Port the exact algorithm used by the Python library to Go. The algorithm operates on the blue channel (BGR index 0 in OpenCV, which is the B channel) of the image:

1. Convert image to a float64 plane for the blue channel.
2. Apply 2D single-level Haar DWT → four subbands: LL, LH, HL, HH.
3. Apply 2D DCT to the LL subband using 4×4 blocks.
4. Partition the DCT-LL matrix into non-overlapping 4×4 blocks; apply SVD to each block.
5. Embed one bit per block by modifying the largest singular value (`S[0]`) relative to a scaling factor.
6. Reverse: inverse SVD reconstruction → inverse DCT → inverse DWT → reconstruct the image with the modified blue channel.

**Detection** simply reverses to step 5 and reads back the bits by comparing `S[0]` against the threshold.

**Go library status:** No off-the-shelf Go library implements DWT-DCT-SVD watermarking. The required primitives are:
- 2D Haar DWT: must be implemented (simple, ~50 lines). `gonum.org/v1/gonum/dsp/fourier` covers FFT but not wavelet transforms. The Haar DWT is a simple filter-bank operation and does not require any external library.
- 2D DCT: `gonum.org/v1/gonum/dsp/fourier` provides 1D DCT; 2D DCT is separable (apply 1D DCT to each row, then each column). Alternatively, implement the Type-II DCT directly (~40 lines).
- SVD: `gonum.org/v1/gonum/mat` provides `SVD` via the `mat.SVD` struct, which wraps LAPACK-equivalent operations. This is available without CGO in the pure Go gonum implementation.
- Image I/O: Go stdlib `image`, `image/jpeg`, `image/png`; `golang.org/x/image/webp` for WebP decode (WebP encode requires a third-party library or conversion via ImageMagick — see note below).

**Pros:** Highest robustness. The SVD step provides algebraic stability — the embedded information survives moderate coefficient perturbations from re-compression. If implemented correctly, byte-level compatibility with the Python implementation is achievable (see section 6). Payload format is unchanged.

**Cons:** Most implementation work. SVD per block is computationally heavier than DCT-only. Requires `gonum/mat` as a new dependency (pure Go, no CGO). Careful attention to the exact block ordering and bit encoding used by `imwatermark` is required to achieve cross-compatibility — the source of that library must be studied carefully.

**Recommendation: Choose Option A.**

### Option B: DCT-Only JPEG Coefficient Manipulation

Manipulate JPEG DCT coefficients at the JPEG container level, bypassing the DWT and SVD steps.

**Go library status:** Go's stdlib `image/jpeg` decodes to `image.Image` — it does not expose the underlying DCT coefficient blocks. Direct coefficient manipulation would require either:
- A CGO wrapper around libjpeg-turbo, which reintroduces a native dependency.
- A pure-Go JPEG library that exposes coefficients: none exists in the Go ecosystem as of early 2026 that is production-ready and exposes write access to DCT blocks.

**Pros:** No DWT/SVD complexity. Modifications survive JPEG round-trips perfectly (since the coefficients are not decoded to pixels and back).

**Cons:** JPEG-only; PNG and WebP files would require a different code path. The CGO route reintroduces native library dependency, which contradicts the goal. The pure-Go route has no viable library. This approach is not recommended.

### Option C: LSB Steganography

Embed watermark bits in the least-significant bits of pixel values using Go's stdlib `image` package.

**Pros:** Trivially simple. Pure stdlib. ~20 lines of code.

**Cons:** Completely destroyed by any lossy JPEG compression. Since the DownloadOnce workflow re-encodes images through ImageMagick at quality 92, LSB payloads would be erased before the file is ever stored. This approach is categorically unsuitable for this use case.

### Recommendation Summary

| Criterion               | A (DWT-DCT-SVD) | B (DCT-coeff) | C (LSB) |
|-------------------------|-----------------|---------------|---------|
| JPEG robustness         | High            | High          | None    |
| Downscale robustness    | Medium-High     | Medium        | None    |
| Implementation effort   | High            | High          | Trivial |
| Pure Go (no CGO)        | Yes             | No            | Yes     |
| PNG/WebP support        | Yes             | No            | Yes     |
| Cross-compat w/ Python  | Achievable      | N/A           | N/A     |

**Option A is the correct choice.** The implementation effort is bounded; the core DWT, DCT, and SVD primitives are straightforward. The `gonum` package for SVD is a single well-maintained Go module with no CGO requirement.

---

## 4. Go Library Landscape

### Existing Go Watermarking Libraries

No production-ready Go library for invisible frequency-domain watermarking exists in the ecosystem as of February 2026. A survey of GitHub and pkg.go.dev finds only:
- `github.com/auyer/steganography` — LSB steganography only, no frequency-domain support.
- `github.com/shomali11/watermark` — visible text/image overlay only.
- Several unmaintained forks of LSB approaches.

This confirms the implementation must be built from first principles.

### gonum (gonum.org/v1/gonum)

The primary mathematical support library.
- `gonum.org/v1/gonum/mat` — dense matrix operations including SVD (`mat.SVD`), matrix multiplication, slicing. Pure Go. This is the critical dependency for the SVD step.
- `gonum.org/v1/gonum/dsp/fourier` — provides 1D FFT and 1D DCT (Type-II). The 2D DCT is constructed from two passes of the 1D DCT (separable transform). Can be used directly or replaced by a hand-rolled DCT for performance.

`gonum` is a large module; care should be taken to import only the required sub-packages to minimize binary size growth.

### Image Processing

- `image`, `image/jpeg`, `image/png`, `image/color` — Go stdlib; handles decode/encode for JPEG and PNG without any external dependency.
- `golang.org/x/image/webp` — decodes WebP. WebP encoding in pure Go is not available; the current `imagemagick.go` path already writes WebP via ImageMagick, so this is not a regression.
- `github.com/disintegration/imaging` — convenient image resizing and channel extraction; pure Go. Useful for extracting float channel planes but not strictly required.

### JPEG Quality Considerations

Go's `image/jpeg` encoder accepts a `jpeg.Options{Quality: N}` parameter matching libjpeg's quality scale. The current Python pipeline writes JPEG at quality 92 via `cv2.imwrite`. Matching this exactly in Go is straightforward.

---

## 5. Implementation Plan

### 5.1 Package Structure

```
internal/watermark/
├── payload.go          (unchanged)
├── ffmpeg.go           (unchanged)
├── imagemagick.go      (unchanged — visible overlay; migration in section 10)
├── invisible.go        (kept for legacy detect path; embed functions deprecated)
├── image_go.go         (NEW — Go-native embed + detect entry points)
└── dwt/
    ├── haar.go         (NEW — 2D Haar DWT forward and inverse)
    └── haar_test.go    (NEW — unit tests for round-trip identity)
    dct/
    ├── dct2d.go        (NEW — 2D Type-II DCT and inverse using separable 1D DCT)
    └── dct2d_test.go   (NEW)
```

The `dwt` and `dct` sub-packages contain pure mathematical primitives with no image dependencies, making them independently testable.

### 5.2 `internal/watermark/dwt/haar.go`

The 2D Haar DWT decomposes a 2D float64 slice into four subbands. The forward transform:

```
// Forward2D applies a single-level 2D Haar DWT to src (h×w), modifying dst in place.
// Returns (LL, LH, HL, HH) as views into dst.
// h and w must be even.
func Forward2D(src [][]float64) (ll, lh, hl, hh [][]float64)
```

Pseudocode for the forward 1D Haar transform on a row of length N (N must be even):

```
for i := 0; i < N/2; i++ {
    avg[i]  = (src[2*i] + src[2*i+1]) / 2.0
    diff[i] = (src[2*i] - src[2*i+1]) / 2.0
}
// Output: first N/2 elements = averages (low), second N/2 = differences (high)
```

The 2D forward transform applies the 1D transform along rows first, then along columns of the result. This gives a standard subband layout in a single output buffer of the same size as the input:

```
[  LL  |  LH  ]
[  HL  |  HH  ]
```

The inverse transform mirrors the forward pass: reconstruct rows from (avg, diff), then reconstruct columns.

Unit tests must verify:
- `Inverse2D(Forward2D(x)) == x` for random float64 inputs (round-trip identity to within floating-point epsilon).
- Subband energy preservation: sum of subband variances equals variance of input.

### 5.3 `internal/watermark/dct/dct2d.go`

The 2D DCT-II is separable: apply 1D DCT-II to each row, then to each column of the result. The naive O(N²) per-block DCT is sufficient given the block sizes involved (4×4 or 8×8).

```
// Forward2D applies the 2D Type-II DCT to a square block.
func Forward2D(block [][]float64) [][]float64

// Inverse2D applies the 2D Type-III DCT (inverse of Type-II).
func Inverse2D(block [][]float64) [][]float64
```

The 1D DCT-II formula for N points:

```
X[k] = scale(k) * sum_{n=0}^{N-1} x[n] * cos(pi * k * (2n+1) / (2N))

where scale(0) = sqrt(1/N), scale(k>0) = sqrt(2/N)
```

This matches the orthonormal DCT-II definition used by scipy and numpy, which is what `imwatermark` relies on (via `cv2.dct` which uses the same normalization convention).

Unit tests must verify round-trip identity and cross-check a known test vector against a reference implementation.

### 5.4 `internal/watermark/image_go.go`

This file is the replacement for the Python subprocess calls. It exposes two functions matching the signatures of the functions in `invisible.go` that are called from `internal/worker/pool.go`:

```go
// GoInvisibleImageEmbed embeds a DWT-DCT-SVD invisible watermark into an image file.
// inputPath must be JPEG, PNG, or WebP.
// outputPath extension determines the output format; JPEG is recommended for final delivery.
// payloadHex is the 32-character hex string from watermark.PayloadHex().
func GoInvisibleImageEmbed(
    ctx context.Context,
    inputPath, outputPath, payloadHex string,
    jpegQuality int,
) error

// GoInvisibleImageDetect extracts the DWT-DCT-SVD watermark from an image file.
// Returns the hex-encoded payload bytes.
func GoInvisibleImageDetect(
    ctx context.Context,
    inputPath string,
    payloadLength int,
) (string, error)
```

**Embed algorithm (detailed pseudocode):**

```
1. Decode inputPath → image.Image (JPEG/PNG auto-detected)
2. Convert to [H][W][3]float64 in BGR channel order to match OpenCV convention
   (Go's image.NRGBA has R at index 0; swap to B=0, G=1, R=2)
3. Extract blue channel plane B[H][W]float64
4. Apply Forward2D Haar DWT on B → subbands (LL, LH, HL, HH)
   LL is [H/2][W/2]
5. Apply Forward2D DCT to the entire LL subband (treat as one block for the
   imwatermark reference implementation; some variants apply per-block —
   study imwatermark source to confirm the exact tiling)
6. Partition the DCT result into non-overlapping 4×4 blocks, left-to-right,
   top-to-bottom
7. Convert payloadHex to []byte (16 bytes = 128 bits)
   Expand to []int{0,1,...} bit sequence (MSB first)
8. For each bit b at block index i:
       Apply SVD to block[i] → U, S, Vt
       Modify S[0]: if b == 1 then S[0] += alpha else S[0] -= alpha
       (alpha is the embedding strength; imwatermark uses alpha ≈ 10.0)
       Reconstruct block[i] = U * diag(S) * Vt
9. Rebuild DCT result from modified blocks
10. Apply Inverse2D DCT to get modified LL
11. Apply Inverse2D DWT using modified LL and original LH, HL, HH
12. Reconstruct image with modified blue channel; clamp to [0, 255]
13. Encode to outputPath at specified JPEG quality
```

**Detect algorithm:**

```
1–5. Same as embed steps 1–5 (decode → BGR float → blue channel → DWT → DCT)
6. Partition DCT result into 4×4 blocks, same tiling
7. For each block i (up to payloadLength * 8 blocks):
       Apply SVD → U, S, Vt
       bit[i] = 1 if S[0] > threshold else 0
       (threshold is the same alpha used in embedding; or use sign of deviation)
8. Pack bit sequence into bytes (MSB first), return hex string
```

**SVD dependency:** Use `gonum.org/v1/gonum/mat`:

```go
import "gonum.org/v1/gonum/mat"

var svd mat.SVD
svd.Factorize(blockMatrix, mat.SVDThin)
S := svd.Values(nil)
var U, Vt mat.Dense
svd.UTo(&U)
svd.VTo(&Vt)  // gonum returns V, not Vt; transpose before multiply
```

**Image channel handling note:** OpenCV reads images as BGR; Go's `image` package returns RGBA (or NRGBA, Gray, YCbCr, etc. depending on JPEG sub-type). The channel extraction must account for this. A JPEG decoded by Go's `image/jpeg` often returns `*image.YCbCr`. The implementation must handle color model conversion explicitly:

```go
func toRGBA(img image.Image) *image.NRGBA {
    bounds := img.Bounds()
    dst := image.NewNRGBA(bounds)
    draw.Draw(dst, bounds, img, bounds.Min, draw.Src)
    return dst
}
// Blue channel in Go's RGBA is pixel[3*i+2] in flat layout (R=0,G=1,B=2)
// imwatermark uses B channel (OpenCV index 0) = Go's B component
```

### 5.5 Worker Integration

In `internal/worker/pool.go`, the existing calls to `watermark.InvisibleImageEmbed` and `watermark.InvisibleImageDetect` are replaced with calls to `watermark.GoInvisibleImageEmbed` and `watermark.GoInvisibleImageDetect`. The function signatures are identical except for removing the `pythonPath` and `scriptPath` parameters:

Current (pool.go line 218):
```go
watermark.InvisibleImageEmbed(ctx, visibleOutput, outputPath, payloadHex,
    p.pythonPath(), p.embedScriptPath(), jpegQuality)
```

Replacement:
```go
watermark.GoInvisibleImageEmbed(ctx, visibleOutput, outputPath, payloadHex, jpegQuality)
```

The `p.pythonPath()`, `p.embedScriptPath()`, `p.detectScriptPath()` helper methods on `Pool` are removed. The `VenvPath` and `ScriptsDir` fields on `config.Config` are retained for now to support the legacy detection path (see section 6) but can be removed after the migration window closes.

---

## 6. Compatibility Concern and Detection Strategy

### 6.1 The Compatibility Problem

Files watermarked before this change were embedded using the Python `dwtDctSvd` implementation. Files watermarked after will use the Go implementation. These are two separate code paths that must produce interoperable outputs — or detection must route to the correct algorithm based on metadata.

Cross-compatibility at the byte level between the Python and Go implementations is theoretically achievable but requires careful alignment of:
- The exact block size used for DCT partitioning.
- The embedding strength (alpha parameter; must be read from `imwatermark` source).
- The SVD normalization convention.
- The bit packing order (MSB vs LSB first).
- The channel ordering (B in OpenCV = B in Go RGBA model, but the array layout differs).

If any of these differ, Go-embedded watermarks will not be detected by the Python script, and vice versa. This is an acceptable temporary state during migration if detection is also ported to Go (which is part of this spec).

### 6.2 Algorithm Tag in Database

Add a `wm_algorithm` column to `watermark_index` to record which implementation embedded a given file. This allows the detection path to choose the correct decoder.

**New migration `006_wm_algorithm.sql`:**

```sql
ALTER TABLE watermark_index ADD COLUMN wm_algorithm TEXT NOT NULL DEFAULT 'dwtDctSvd-python';
```

When `GoInvisibleImageEmbed` is used for a new file, the worker records `wm_algorithm = 'dwtDctSvd-go'` in `InsertWatermarkIndex`. The existing rows default to `'dwtDctSvd-python'`.

Similarly, add `wm_algorithm` to `download_tokens` as a record of what was used at embed time:

```sql
ALTER TABLE download_tokens ADD COLUMN wm_algorithm TEXT;
```

### 6.3 Detection Routing

In `processDetectJob` in `pool.go`, after extracting the payload hex from the uploaded file, the detection path becomes:

```go
// Try Go native detection first (for new files)
payloadHex, err = watermark.GoInvisibleImageDetect(ctx, inputPath, watermark.PayloadLength)
if err != nil || payloadHex == "" {
    // Fall back to Python detection for legacy files (while Python is still available)
    payloadHex, err = watermark.InvisibleImageDetect(ctx, inputPath,
        p.pythonPath(), p.detectScriptPath(), watermark.PayloadLength)
}
```

During the migration window (while both implementations are available), both are tried. After Docker removes Python, only the Go path runs. Legacy Python-embedded files detected during this period will require the Go implementation to be cross-compatible with the Python output — which is the hard requirement that drives the algorithm alignment work in section 7.

### 6.4 Recommendation

Implement Go embed and Go detect together. Do not ship Go embed without Go detect. The migration window where Python is still present in Docker should be used to run the cross-compatibility test suite (section 7). Once cross-compatibility is confirmed, Python is removed from the Docker image.

---

## 7. Robustness Testing Plan

All tests are run against the same 10-image test corpus (mix of JPEG and PNG, varying resolutions from 800×600 to 4096×3072, varying content types: photos, screenshots, diagrams).

### 7.1 Test Suite Structure

Tests live in `internal/watermark/testdata/` (images) and `internal/watermark/image_go_test.go`.

### 7.2 Test Cases

**T1: Round-trip identity (lossless)**
- Embed with Go at PNG output → detect with Go.
- Expected: 100% of 16 payload bytes recovered correctly for all 10 images.
- Pass criterion: 10/10 images, 0 bit errors.

**T2: JPEG round-trip at quality 92 (current production quality)**
- Embed with Go → save as JPEG Q92 → detect with Go.
- Pass criterion: ≥9/10 images, ≤2 bit errors per image.

**T3: JPEG re-compression at Q85**
- Embed with Go → save JPEG Q92 → re-compress to Q85 → detect with Go.
- Pass criterion: ≥8/10 images, CRC valid or fuzzy match succeeds.

**T4: JPEG re-compression at Q75**
- Embed → save JPEG Q92 → re-compress to Q75 → detect with Go.
- Pass criterion: ≥7/10 images recover payload (CRC or fuzzy).

**T5: JPEG re-compression at Q60**
- Pass criterion: ≥5/10 images recover payload (fuzzy match acceptable).
- Informational only; Q60 is below the expected attack surface.

**T6: 50% downscale (bilinear resample)**
- Embed → save JPEG Q92 → downscale to 50% → detect with Go.
- Pass criterion: ≥6/10 images recover payload.

**T7: Brightness +20% adjustment**
- Embed → save JPEG Q92 → apply brightness ×1.2 (clamp to 255) → detect.
- Pass criterion: ≥8/10 images recover payload.

**T8: Cross-compatibility — Go embed, Python detect**
- Embed with Go → save JPEG Q92 → run Python `detect_watermark.py`.
- Pass criterion: ≥8/10 images produce identical payload hex.
- This test is run only when Python is available (pre-migration Docker build).

**T9: Cross-compatibility — Python embed, Go detect**
- Embed with Python → save JPEG Q92 → detect with Go.
- Pass criterion: ≥8/10 images produce identical payload hex.

T8 and T9 together confirm bidirectional cross-compatibility. Failure on these tests means the Go implementation has a parameter mismatch with the Python library and must be corrected before Python is removed from Docker.

### 7.3 Performance Benchmark

Run `go test -bench=BenchmarkGoEmbed -benchtime=10s` on the test corpus.

Target: embedding a 2000×1500 JPEG in ≤500 ms on a single core (matching or beating the Python subprocess path which incurs ~300 ms interpreter startup alone before any computation).

### 7.4 Pass/Fail Criteria for Production Release

- T1: must be 10/10 (no round-trip errors).
- T2 and T3: must both pass.
- T8 and T9: must both pass (cross-compatibility required before Python is removed).
- T4, T5, T6, T7: advisory; failure reduces confidence but does not block release.

---

## 8. Migration Strategy

### 8.1 State of Existing Files

All watermarked files currently on disk in `data/watermarked/<campaign_id>/<token_id>.<ext>` were produced by the Python pipeline. The `watermark_index` table has their `payload_hex` entries but no algorithm tag (pre-migration 006 SQL).

After `006_wm_algorithm.sql` runs, existing rows get `wm_algorithm = 'dwtDctSvd-python'` via the `DEFAULT` clause.

### 8.2 New File Production

Starting from the release that includes the Go watermarking implementation:
- All new campaigns use `GoInvisibleImageEmbed`.
- Worker records `wm_algorithm = 'dwtDctSvd-go'` in the `watermark_index` row.
- The `download_tokens.wm_algorithm` column is set to `'dwtDctSvd-go'` at `ActivateToken` time.

Old campaigns (already published) are not re-processed. Their files remain on disk and are served as-is.

### 8.3 Detection During Migration Window

The migration window is defined as the period between shipping the Go implementation and removing Python from Docker. During this window:

1. The Docker image still contains Python and the venv.
2. Detection jobs try the Go detector first.
3. If Go detection fails (returns empty or error), fall back to Python.
4. The `wm_algorithm` column in `watermark_index` is used to provide a hint; if `wm_algorithm = 'dwtDctSvd-python'`, Python detection is attempted first.

### 8.4 Post-Migration (Python Removed)

After the cross-compatibility tests (T8, T9) pass in staging:

1. Remove `python3`, `python3-pip`, `python3-venv`, `invisible-watermark`, `opencv-python-headless` from the Dockerfile.
2. Remove the `RUN python3 -m venv /opt/venv && ...` layer.
3. Remove `ENV VENV_PATH` from the Dockerfile.
4. Keep `VENV_PATH` in `config.Config` but make it ignored (or remove after a deprecation cycle).
5. The `ScriptFS` embed in `embed.go` and the `scripts/` directory can be removed, eliminating `embed_watermark.py` and `detect_watermark.py` from the binary.
6. Remove `p.pythonPath()`, `p.embedScriptPath()`, `p.detectScriptPath()` from `pool.go`.
7. The `InvisibleImageEmbed`, `InvisibleImageDetect`, `InvisibleVideoEmbed`, `InvisibleVideoDetect` functions in `invisible.go` can be deleted or left as dead code until a cleanup pass.

### 8.5 Database Queries Impact

`db.InsertWatermarkIndex` in `queries_campaigns.go` (or wherever it currently resides) gains a `wmAlgorithm string` parameter:

```go
func InsertWatermarkIndex(database *sql.DB, payloadHex, tokenID, campaignID, recipientID, wmAlgorithm string) error {
    _, err := database.Exec(
        `INSERT OR IGNORE INTO watermark_index (payload_hex, token_id, campaign_id, recipient_id, wm_algorithm)
         VALUES (?, ?, ?, ?, ?)`,
        payloadHex, tokenID, campaignID, recipientID, wmAlgorithm,
    )
    return err
}
```

The call site in `pool.go` line 250 is updated accordingly.

---

## 9. Docker Image Impact

### Before (Current)

```
debian:trixie-slim base             ~80 MB
ffmpeg                              ~60 MB
imagemagick                         ~30 MB
fonts-dejavu-core                    ~2 MB
ca-certificates                      ~1 MB
python3 + pip + venv               ~80 MB
opencv-python-headless             ~200 MB
invisible-watermark + numpy + etc   ~80 MB
downloadonce binary                  ~20 MB
                                  --------
Total (approx):                   ~553 MB uncompressed
                                  ~1.2 GB with Docker layer overhead and OS libs
```

### After (Post-Migration)

```
debian:trixie-slim base             ~80 MB
ffmpeg                              ~60 MB
imagemagick                         ~30 MB
fonts-dejavu-core                    ~2 MB
ca-certificates                      ~1 MB
downloadonce binary (larger due to gonum) ~30 MB
                                  --------
Total (approx):                   ~203 MB uncompressed
                                  ~200–250 MB compressed image
```

**Net reduction: approximately 1 GB compressed Docker image size.**

The Go binary grows slightly (gonum/mat adds ~5–10 MB to the binary), but this is negligible compared to the savings. `CGO_ENABLED=0` is maintained.

### What Changes in the Dockerfile

Lines removed:
```dockerfile
python3 \
python3-pip \
python3-venv \
```

Block removed:
```dockerfile
RUN python3 -m venv /opt/venv \
    && /opt/venv/bin/pip install --no-cache-dir \
       invisible-watermark opencv-python-headless
```

Line removed:
```dockerfile
ENV VENV_PATH=/opt/venv
```

The `tesseract-ocr` package (currently in the Dockerfile) is unrelated to watermarking; its presence should be reviewed separately but is out of scope for this spec.

---

## 10. Visible Overlay Migration (ImageMagick)

The visible watermark overlay in `internal/watermark/imagemagick.go` calls `magick` to annotate three text strings at SouthEast, NorthWest, and Center gravity with configurable opacity and font size. This is the remaining subprocess dependency for image processing after Python is removed.

### Feasibility of Pure Go Replacement

A Go-native visible overlay implementation is feasible using:
- `golang.org/x/image/font` + `github.com/golang/freetype` for TrueType font rendering.
- `image/draw` for compositing text onto the image with alpha blending.
- `image/jpeg` and `image/png` for output.

The `freetype` package (`github.com/golang/freetype`) or the newer `golang.org/x/image/font/opentype` can render TrueType fonts (the DejaVu font already required by the Dockerfile) to an `image.RGBA` mask, which is then composited using `draw.DrawMask`.

**Effort estimate:** Medium. Text rendering at arbitrary angles is not needed (ImageMagick's `-annotate` with `+0+0` is axis-aligned). The font loading, glyph measurement, and blending are ~200–300 lines of Go. The result would remove `imagemagick` from the Dockerfile, saving another ~30 MB and eliminating the second subprocess dependency.

**Recommendation:** Treat visible overlay migration as a follow-on to this spec, scheduled as Milestone M5 after the invisible watermark work is complete and stable. The operational risk of changing the visible overlay appearance mid-cycle is low but unnecessary to take on simultaneously with the DWT-DCT-SVD port. If ImageMagick is removed, `ENV FONT_PATH` remains meaningful (used directly by Go's freetype renderer) and no config changes are needed.

---

## 11. Implementation Milestones

### M1: Core Mathematical Primitives (1–2 weeks)

Deliverables:
- `internal/watermark/dwt/haar.go` with `Forward2D` and `Inverse2D`.
- `internal/watermark/dct/dct2d.go` with `Forward2D` and `Inverse2D`.
- Unit tests for both: round-trip identity, known-vector cross-check.
- `go test ./internal/watermark/dwt/... ./internal/watermark/dct/...` passes.
- `gonum.org/v1/gonum` added to `go.mod`.

Risk: Low. Both transforms are well-understood; unit tests will catch any implementation bugs immediately.

### M2: Embed and Detect Implementation (2–3 weeks)

Deliverables:
- `internal/watermark/image_go.go` with `GoInvisibleImageEmbed` and `GoInvisibleImageDetect`.
- Test suite T1 and T2 pass (round-trip and JPEG Q92).
- The `imwatermark` Python source is reviewed to confirm exact parameter alignment (block size, alpha, bit ordering).
- Migration `006_wm_algorithm.sql` written and added.
- `db.InsertWatermarkIndex` updated to accept `wmAlgorithm`.

Risk: Medium. The SVD block tiling and bit encoding convention must exactly match `imwatermark`'s internal implementation. This requires careful reading of the Python library source (`invisible-watermark` on GitHub). A mismatch here means T8/T9 fail, which blocks production deployment.

### M3: Robustness and Cross-Compatibility Testing (1 week)

Deliverables:
- Full T1–T9 test suite executed.
- T1, T2, T3, T8, T9 pass (hard gates).
- Performance benchmark meets ≤500 ms per image target.
- Any failures in T8/T9 trigger a parameter correction loop back into M2.

Risk: High if cross-compatibility is not achieved. Mitigation: start with T1 (pure Go round-trip) and T8/T9 (cross-compat) before investing in robustness testing. If T8/T9 cannot be made to pass within a reasonable iteration budget, reconsider: the fallback position is to keep Python in Docker for detection of legacy files only and never ship the Go detection path — but this is undesirable and should be avoided.

### M4: Worker Integration and Docker Update (1 week)

Deliverables:
- `pool.go` updated to call `GoInvisibleImageEmbed` and `GoInvisibleImageDetect`.
- Python subprocess calls removed from the hot path (kept as fallback during migration window only).
- `Dockerfile` updated: Python dependencies removed.
- `embed.go` `ScriptFS` and `scripts/` directory removed.
- `config.Config.VenvPath` and `config.Config.ScriptsDir` deprecated (removed or no-op).
- Docker image built and size verified at ~200–250 MB.
- Integration test: publish a campaign end-to-end in Docker, verify watermark detected.

Risk: Low once M3 is complete. This is plumbing and cleanup work.

### M5 (Future): ImageMagick Visible Overlay Replacement

Deliverables:
- `internal/watermark/imagemagick.go` replaced with pure Go text compositing.
- `imagemagick` removed from `Dockerfile`.
- Visual output verified to be perceptually equivalent to current ImageMagick output.

---

## Appendix A: imwatermark dwtDctSvd Reference Parameters

The following parameters from the `invisible-watermark` Python library source must be replicated exactly in the Go implementation. These values should be verified against the library's current source before M2 is completed.

| Parameter         | Value (from imwatermark source) | Notes |
|-------------------|---------------------------------|-------|
| DWT level         | 1 (single decomposition level)  | |
| DWT channel       | Blue (OpenCV index 0)           | B in Go RGBA |
| DCT block size    | 4×4                             | Applied to full LL subband? Verify. |
| SVD alpha         | ~10.0 (embedding strength)      | Verify exact value |
| Bit order         | MSB first, row-major            | Verify |
| Payload expansion | None (bits packed from bytes)   | 128 bits for 16-byte payload |

**Action item for M2:** Clone `https://github.com/ShieldMnt/invisible-watermark` and audit `wmEncoder.py` and `wmDecoder.py` for exact parameter values before writing `image_go.go`.

---

## Appendix B: Files Changed Summary

| File | Change |
|------|--------|
| `internal/watermark/dwt/haar.go` | New |
| `internal/watermark/dwt/haar_test.go` | New |
| `internal/watermark/dct/dct2d.go` | New |
| `internal/watermark/dct/dct2d_test.go` | New |
| `internal/watermark/image_go.go` | New |
| `internal/watermark/invisible.go` | Deprecated (kept for legacy detect fallback) |
| `internal/worker/pool.go` | Update embed/detect calls; remove python path helpers |
| `internal/db/queries_campaigns.go` | Update `InsertWatermarkIndex` signature |
| `internal/config/config.go` | Mark `VenvPath` deprecated |
| `migrations/006_wm_algorithm.sql` | New migration |
| `embed.go` | Remove `ScriptFS` (M4) |
| `scripts/embed_watermark.py` | Delete (M4) |
| `scripts/detect_watermark.py` | Delete (M4) |
| `Dockerfile` | Remove Python layers (M4) |
| `go.mod` | Add `gonum.org/v1/gonum` |
