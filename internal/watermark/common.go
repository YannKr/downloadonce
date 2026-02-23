package watermark

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
)

func WatermarkText(tokenID, recipientName string) string {
	h := sha256.Sum256([]byte(tokenID))
	shortHash := hex.EncodeToString(h[:4])
	return fmt.Sprintf("[%s | %s]", shortHash, recipientName)
}

func EscapeFFmpegText(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`'`, `'\\''`,
		`:`, `\:`,
		`[`, `\[`,
		`]`, `\]`,
		`;`, `\;`,
		`%`, `%%`,
	)
	return r.Replace(s)
}

func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var h hash.Hash = sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func FileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

var MimeToExt = map[string]string{
	"video/mp4":        ".mp4",
	"video/quicktime":  ".mov",
	"video/x-matroska": ".mkv",
	"image/jpeg":       ".jpg",
	"image/png":        ".png",
	"image/tiff":       ".tiff",
	"image/webp":       ".webp",
}

var MimeToAssetType = map[string]string{
	"video/mp4":        "video",
	"video/quicktime":  "video",
	"video/x-matroska": "video",
	"image/jpeg":       "image",
	"image/png":        "image",
	"image/tiff":       "image",
	"image/webp":       "image",
}
