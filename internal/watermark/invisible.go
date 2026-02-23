package watermark

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// InvisibleImageEmbed applies an invisible DWT-DCT watermark to an image
// using the Python invisible-watermark library.
func InvisibleImageEmbed(ctx context.Context, inputPath, outputPath, payloadHex, pythonPath, scriptPath string, jpegQuality int) error {
	args, _ := json.Marshal(map[string]interface{}{
		"input_path":   inputPath,
		"output_path":  outputPath,
		"payload_hex":  payloadHex,
		"jpeg_quality": jpegQuality,
	})

	cmd := exec.CommandContext(ctx, pythonPath, scriptPath, string(args))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("invisible embed: %w\noutput: %s", err, string(output))
	}

	var result struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return fmt.Errorf("invisible embed: failed to parse output: %s", string(output))
	}
	if result.Status != "ok" {
		return fmt.Errorf("invisible embed: %s", result.Message)
	}
	return nil
}

// InvisibleImageDetect extracts an invisible DWT-DCT watermark from an image.
// Returns the hex-encoded payload.
func InvisibleImageDetect(ctx context.Context, inputPath, pythonPath, scriptPath string, payloadLength int) (string, error) {
	args, _ := json.Marshal(map[string]interface{}{
		"input_path":     inputPath,
		"payload_length": payloadLength,
	})

	cmd := exec.CommandContext(ctx, pythonPath, scriptPath, string(args))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("invisible detect: %w\noutput: %s", err, string(output))
	}

	var result struct {
		Status     string `json:"status"`
		PayloadHex string `json:"payload_hex"`
		Message    string `json:"message"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", fmt.Errorf("invisible detect: failed to parse output: %s", string(output))
	}
	if result.Status != "ok" {
		return "", fmt.Errorf("invisible detect: %s", result.Message)
	}
	return result.PayloadHex, nil
}

// InvisibleVideoEmbed embeds invisible watermarks into evenly-spaced key frames
// of a video file. Steps:
//  1. Extract N evenly-spaced I-frames from the video
//  2. Embed invisible watermark into each frame
//  3. The watermarked frames are stored alongside the video for detection reference
//
// Note: Full frame re-injection into the video stream is not performed in this version.
// The visible overlay from FFmpeg is the primary protection for video. Invisible watermarks
// on extracted frames provide a detection mechanism for clean digital copies.
func InvisibleVideoEmbed(ctx context.Context, videoPath, payloadHex, pythonPath, embedScript string, framesDir string) error {
	if err := os.MkdirAll(framesDir, 0755); err != nil {
		return fmt.Errorf("create frames dir: %w", err)
	}

	// Extract I-frames (1 per minute, max 10)
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", videoPath,
		"-vf", "select=eq(pict_type\\,I),showinfo",
		"-vsync", "vfr",
		"-frames:v", "10",
		"-q:v", "2",
		"-y",
		filepath.Join(framesDir, "frame_%03d.png"),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extract keyframes: %w\n%s", err, string(out))
	}

	// Watermark each extracted frame
	entries, err := os.ReadDir(framesDir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".png") {
			continue
		}
		framePath := filepath.Join(framesDir, e.Name())
		wmPath := filepath.Join(framesDir, "wm_"+e.Name())

		if err := InvisibleImageEmbed(ctx, framePath, wmPath, payloadHex, pythonPath, embedScript, 92); err != nil {
			return fmt.Errorf("watermark frame %s: %w", e.Name(), err)
		}
	}

	return nil
}

// InvisibleVideoDetect extracts key frames from a video and attempts to decode
// the invisible watermark from each. Returns all detected payload hex strings.
// The caller should perform majority voting to determine the most likely payload.
func InvisibleVideoDetect(ctx context.Context, videoPath, pythonPath, detectScript string, payloadLength int) ([]string, error) {
	tmpDir, err := os.MkdirTemp("", "detect-frames-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	// Extract key frames
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", videoPath,
		"-vf", "select=eq(pict_type\\,I)",
		"-vsync", "vfr",
		"-frames:v", "10",
		"-q:v", "2",
		"-y",
		filepath.Join(tmpDir, "frame_%03d.png"),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("extract keyframes: %w\n%s", err, string(out))
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return nil, err
	}

	var payloads []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".png") {
			continue
		}
		framePath := filepath.Join(tmpDir, e.Name())
		payload, err := InvisibleImageDetect(ctx, framePath, pythonPath, detectScript, payloadLength)
		if err != nil {
			continue // skip frames that fail to decode
		}
		if payload != "" {
			payloads = append(payloads, payload)
		}
	}

	return payloads, nil
}

// MajorityVote returns the most frequently occurring string from a list.
func MajorityVote(payloads []string) string {
	if len(payloads) == 0 {
		return ""
	}
	counts := make(map[string]int)
	for _, p := range payloads {
		counts[p]++
	}
	var best string
	var bestCount int
	for p, c := range counts {
		if c > bestCount {
			best = p
			bestCount = c
		}
	}
	return best
}
