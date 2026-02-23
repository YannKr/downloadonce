package watermark

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

type ProbeResult struct {
	DurationSecs float64
	Width        int
	Height       int
	VideoCodec   string
	AudioCodec   string
}

type ffprobeOutput struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func Probe(filePath string) (*ProbeResult, error) {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		filePath,
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}

	var parsed ffprobeOutput
	if err := json.Unmarshal(output, &parsed); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}

	result := &ProbeResult{}
	if parsed.Format.Duration != "" {
		result.DurationSecs, _ = strconv.ParseFloat(parsed.Format.Duration, 64)
	}
	for _, s := range parsed.Streams {
		switch s.CodecType {
		case "video":
			result.VideoCodec = s.CodecName
			result.Width = s.Width
			result.Height = s.Height
		case "audio":
			result.AudioCodec = s.CodecName
		}
	}
	return result, nil
}

func ExtractVideoThumbnail(ctx context.Context, inputPath, outputPath string, seekSecs float64) error {
	if seekSecs < 0.1 {
		seekSecs = 1
	}
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-ss", fmt.Sprintf("%.2f", seekSecs),
		"-i", inputPath,
		"-vframes", "1",
		"-vf", "scale=400:-1",
		"-q:v", "3",
		"-y",
		outputPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg thumbnail: %w\n%s", err, string(out))
	}
	return nil
}

func ExtractImageThumbnail(ctx context.Context, inputPath, outputPath string) error {
	cmd := exec.CommandContext(ctx, "magick",
		inputPath,
		"-thumbnail", "400x",
		"-quality", "80",
		outputPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("imagemagick thumbnail: %w\n%s", err, string(out))
	}
	return nil
}
