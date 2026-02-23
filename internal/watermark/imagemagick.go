package watermark

import (
	"context"
	"fmt"
	"os/exec"
)

type ImageParams struct {
	InputPath  string
	OutputPath string
	Text       string
	FontPath   string
}

func ImageWatermark(ctx context.Context, p ImageParams) error {
	cmd := exec.CommandContext(ctx, "magick",
		p.InputPath,
		"-font", p.FontPath,
		"-pointsize", "24",
		"-fill", "rgba(255,255,255,0.15)",
		"-gravity", "SouthEast",
		"-annotate", "+20+20", p.Text,
		"-gravity", "NorthWest",
		"-annotate", "+20+20", p.Text,
		"-fill", "rgba(255,255,255,0.08)",
		"-gravity", "Center",
		"-pointsize", "32",
		"-annotate", "+0+0", p.Text,
		"-quality", "92",
		p.OutputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("imagemagick watermark: %w\noutput: %s", err, string(output))
	}
	return nil
}
