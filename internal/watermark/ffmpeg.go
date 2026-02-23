package watermark

import (
	"context"
	"fmt"
	"os/exec"
)

type VideoParams struct {
	InputPath  string
	OutputPath string
	Text       string
	FontPath   string
}

func VideoWatermark(ctx context.Context, p VideoParams) error {
	escaped := EscapeFFmpegText(p.Text)

	cornerFilter := fmt.Sprintf(
		"drawtext=text='%s':fontcolor=white@0.15:fontsize=11:"+
			"x='if(lt(mod(t\\,60)\\,30)\\,w-text_w-20\\,20)':"+
			"y='if(lt(mod(t\\,60)\\,30)\\,h-text_h-20\\,20)':"+
			"fontfile='%s'",
		escaped, p.FontPath,
	)

	centerFilter := fmt.Sprintf(
		"drawtext=text='%s':fontcolor=white@0.08:fontsize=14:"+
			"x=(w-text_w)/2:y=(h-text_h)/2:"+
			"fontfile='%s'",
		escaped, p.FontPath,
	)

	vf := cornerFilter + "," + centerFilter

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-i", p.InputPath,
		"-vf", vf,
		"-c:v", "libx265",
		"-crf", "22",
		"-preset", "medium",
		"-tag:v", "hvc1",
		"-c:a", "copy",
		"-y",
		p.OutputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg watermark: %w\noutput: %s", err, string(output))
	}
	return nil
}
