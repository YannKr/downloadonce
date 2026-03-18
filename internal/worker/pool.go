package worker

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YannKr/downloadonce/internal/config"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/email"
	"github.com/YannKr/downloadonce/internal/model"
	"github.com/YannKr/downloadonce/internal/sse"
	"github.com/YannKr/downloadonce/internal/watermark"
	"github.com/YannKr/downloadonce/internal/webhook"
)

// backoffDelays defines the delay before each retry attempt.
var backoffDelays = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
}

func nextRetryDelay(retryCount int) time.Duration {
	if retryCount < len(backoffDelays) {
		return backoffDelays[retryCount]
	}
	return backoffDelays[len(backoffDelays)-1]
}

// isPermanentFailure returns true if the error indicates a condition that will
// never succeed on retry (e.g., corrupt input file, unknown format).
func isPermanentFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// FFmpeg permanent errors
	if strings.Contains(msg, "invalid data found when processing input") ||
		strings.Contains(msg, "moov atom not found") ||
		strings.Contains(msg, "unknown job type") {
		return true
	}
	// Input file missing
	if strings.Contains(msg, "no such file or directory") && strings.Contains(msg, "input") {
		return true
	}
	// Go-native image decode permanent errors
	if strings.Contains(msg, "image: unknown format") ||
		strings.Contains(msg, "invalid jpeg") ||
		strings.Contains(msg, "invalid png") ||
		strings.Contains(msg, "not a valid png") {
		return true
	}
	return false
}

type Pool struct {
	database *sql.DB
	cfg      *config.Config
	mailer   *email.Mailer
	webhook  *webhook.Dispatcher
	sseHub   *sse.Hub
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewPool(database *sql.DB, cfg *config.Config, mailer *email.Mailer, webhookDispatcher *webhook.Dispatcher, sseHub *sse.Hub) *Pool {
	return &Pool{database: database, cfg: cfg, mailer: mailer, webhook: webhookDispatcher, sseHub: sseHub}
}

func (p *Pool) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)
	for i := 0; i < p.cfg.WorkerCount; i++ {
		p.wg.Add(1)
		go p.run(ctx, i)
	}
	slog.Info("worker pool started", "workers", p.cfg.WorkerCount)
}

func (p *Pool) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	slog.Info("worker pool stopped")
}

func (p *Pool) run(ctx context.Context, id int) {
	defer p.wg.Done()

	jobTypes := []string{"watermark_video", "watermark_image", "detect"}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := db.ClaimNextJob(p.database, jobTypes)
		if err != nil {
			slog.Error("claim job", "worker", id, "error", err)
			sleep(ctx, 2*time.Second)
			continue
		}
		if job == nil {
			sleep(ctx, 2*time.Second)
			continue
		}

		slog.Info("processing job", "worker", id, "job", job.ID, "type", job.JobType)

		var processErr error
		switch job.JobType {
		case "detect":
			processErr = p.processDetectJob(ctx, job)
		default:
			processErr = p.processJob(ctx, job)
		}

		if processErr != nil {
			slog.Error("job failed", "job", job.ID, "type", job.JobType, "error", processErr)

			isPermanent := isPermanentFailure(processErr)
			var retried bool

			if isPermanent {
				db.FailJob(p.database, job.ID, processErr.Error())
			} else {
				delay := nextRetryDelay(job.RetryCount)
				retried, _ = db.RetryOrFailJob(p.database, job.ID, processErr.Error(), delay)
			}

			if !retried {
				p.publishJobFailed(job, processErr.Error())
				p.notifyJobFailed(job, processErr.Error())
			} else {
				slog.Info("job scheduled for retry", "job", job.ID, "retry", job.RetryCount+1, "delay", nextRetryDelay(job.RetryCount))
			}
		} else {
			db.CompleteJob(p.database, job.ID)
			slog.Info("job completed", "job", job.ID)
		}

		if job.JobType != "detect" {
			p.checkCampaignCompletion(job.CampaignID)
		}
	}
}

func (p *Pool) pythonPath() string {
	return filepath.Join(p.cfg.VenvPath, "bin", "python3")
}

func (p *Pool) embedScriptPath() string {
	return filepath.Join(p.cfg.ScriptsDir, "embed_watermark.py")
}

func (p *Pool) detectScriptPath() string {
	return filepath.Join(p.cfg.ScriptsDir, "detect_watermark.py")
}

func (p *Pool) processJob(ctx context.Context, job *model.Job) error {
	token, err := db.GetToken(p.database, job.TokenID)
	if err != nil || token == nil {
		return fmt.Errorf("load token %s: %w", job.TokenID, err)
	}

	campaign, err := db.GetCampaign(p.database, job.CampaignID)
	if err != nil || campaign == nil {
		return fmt.Errorf("load campaign %s: %w", job.CampaignID, err)
	}

	asset, err := db.GetAsset(p.database, campaign.AssetID)
	if err != nil || asset == nil {
		return fmt.Errorf("load asset %s: %w", campaign.AssetID, err)
	}

	recipient, err := db.GetRecipient(p.database, token.RecipientID)
	if err != nil || recipient == nil {
		return fmt.Errorf("load recipient %s: %w", token.RecipientID, err)
	}

	db.UpdateJobProgress(p.database, job.ID, 10) // started
	p.publishProgress(job, 10)

	inputPath := filepath.Join(p.cfg.DataDir, asset.OriginalPath)
	ext := filepath.Ext(asset.OriginalPath)
	if job.JobType == "watermark_video" {
		ext = ".mp4"
	}

	outDir := filepath.Join(p.cfg.DataDir, "watermarked", job.CampaignID)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	outputPath := filepath.Join(outDir, job.TokenID+ext)

	wmText := watermark.WatermarkText(job.TokenID, recipient.Name)

	// Build the proper 16-byte payload
	payloadHex := watermark.PayloadHex(job.TokenID, job.CampaignID)

	// needsInvisible is true if the campaign has invisible watermarking enabled.
	// The Go-native path is always available; Python is a fallback when configured.
	needsInvisible := campaign.InvisibleWM

	// For images with invisible watermark: visible -> temp PNG (lossless), then invisible -> final JPEG.
	// Using PNG for the intermediate avoids double JPEG compression which degrades the invisible watermark.
	// For images without invisible: visible -> final directly.
	visibleOutput := outputPath
	if needsInvisible && job.JobType == "watermark_image" {
		visibleOutput = outputPath + ".visible.png"
	}

	// wmAlgorithm records which algorithm was used for this token (written to watermark_index).
	wmAlgorithm := "dwtDctSvd-go"

	switch job.JobType {
	case "watermark_video":
		err = watermark.VideoWatermark(ctx, watermark.VideoParams{
			InputPath:  inputPath,
			OutputPath: outputPath,
			Text:       wmText,
			FontPath:   p.cfg.FontPath,
		})
		if err != nil {
			os.Remove(outputPath)
			return err
		}

		db.UpdateJobProgress(p.database, job.ID, 30) // visible done
		p.publishProgress(job, 30)

		// For video: embed invisible watermarks into extracted key frames using
		// Python (video frame embed is not yet ported to Go).
		if needsInvisible && p.cfg.ScriptsDir != "" {
			db.UpdateJobProgress(p.database, job.ID, 60) // invisible started
			p.publishProgress(job, 60)
			framesDir := filepath.Join(outDir, job.TokenID+"_frames")
			if embedErr := watermark.InvisibleVideoEmbed(ctx, outputPath, payloadHex, p.pythonPath(), p.embedScriptPath(), framesDir); embedErr != nil {
				slog.Warn("invisible video embed failed, continuing with visible only", "error", embedErr)
			}
			db.UpdateJobProgress(p.database, job.ID, 90) // invisible done
			p.publishProgress(job, 90)
		} else {
			db.UpdateJobProgress(p.database, job.ID, 90)
			p.publishProgress(job, 90)
		}

	case "watermark_image":
		err = watermark.ImageWatermark(ctx, watermark.ImageParams{
			InputPath:  inputPath,
			OutputPath: visibleOutput,
			Text:       wmText,
			FontPath:   p.cfg.FontPath,
		})
		if err != nil {
			os.Remove(visibleOutput)
			return err
		}

		db.UpdateJobProgress(p.database, job.ID, 30) // visible done
		p.publishProgress(job, 30)

		// Chain invisible watermark after visible.
		if needsInvisible {
			db.UpdateJobProgress(p.database, job.ID, 60) // invisible started
			p.publishProgress(job, 60)
			jpegQuality := 92

			// Try Go-native embed first.
			goErr := watermark.GoInvisibleImageEmbed(ctx, visibleOutput, outputPath, payloadHex, jpegQuality)
			if goErr != nil {
				slog.Warn("go invisible embed failed, falling back to python", "error", goErr)
				// Fall back to Python if configured.
				if p.cfg.ScriptsDir != "" {
					if pyErr := watermark.InvisibleImageEmbed(ctx, visibleOutput, outputPath, payloadHex, p.pythonPath(), p.embedScriptPath(), jpegQuality); pyErr != nil {
						slog.Warn("python invisible image embed also failed, using visible-only output", "error", pyErr)
						os.Rename(visibleOutput, outputPath)
						wmAlgorithm = "visible-only"
					} else {
						os.Remove(visibleOutput)
						wmAlgorithm = "dwtDctSvd-python"
					}
				} else {
					slog.Warn("go invisible embed failed and python not configured, using visible-only output", "error", goErr)
					os.Rename(visibleOutput, outputPath)
					wmAlgorithm = "visible-only"
				}
			} else {
				os.Remove(visibleOutput)
				wmAlgorithm = "dwtDctSvd-go"
			}

			db.UpdateJobProgress(p.database, job.ID, 90) // invisible done
			p.publishProgress(job, 90)
		} else {
			db.UpdateJobProgress(p.database, job.ID, 90)
			p.publishProgress(job, 90)
		}

	default:
		return fmt.Errorf("unknown job type: %s", job.JobType)
	}

	sha, err := watermark.SHA256File(outputPath)
	if err != nil {
		return fmt.Errorf("sha256: %w", err)
	}

	size, err := watermark.FileSize(outputPath)
	if err != nil {
		return fmt.Errorf("filesize: %w", err)
	}

	relPath := filepath.Join("watermarked", job.CampaignID, job.TokenID+ext)
	if err := db.ActivateToken(p.database, job.TokenID, relPath, sha, size); err != nil {
		return fmt.Errorf("activate token: %w", err)
	}

	db.InsertWatermarkIndex(p.database, payloadHex, job.TokenID, job.CampaignID, recipient.ID, wmAlgorithm)

	p.publishTokenReady(job)

	return nil
}

// detectResult is the JSON structure stored in result_data for detect jobs.
type detectResult struct {
	Found          bool   `json:"found"`
	PayloadHex     string `json:"payload_hex"`
	TokenID        string `json:"token_id,omitempty"`
	CampaignID     string `json:"campaign_id,omitempty"`
	CampaignName   string `json:"campaign_name,omitempty"`
	RecipientName  string `json:"recipient_name,omitempty"`
	RecipientEmail string `json:"recipient_email,omitempty"`
	RecipientOrg   string `json:"recipient_org,omitempty"`
	Message        string `json:"message,omitempty"`
}

func (p *Pool) processDetectJob(ctx context.Context, job *model.Job) error {
	inputPath := job.InputPath
	if inputPath == "" {
		return fmt.Errorf("detect job has no input_path")
	}

	// Determine file type
	ext := strings.ToLower(filepath.Ext(inputPath))
	isVideo := ext == ".mp4" || ext == ".mkv" || ext == ".avi" || ext == ".mov" || ext == ".webm"

	var payloadHex string
	var err error

	if isVideo {
		// Video detection still uses Python (video frame detect not yet ported to Go).
		var payloads []string
		payloads, err = watermark.InvisibleVideoDetect(ctx, inputPath, p.pythonPath(), p.detectScriptPath(), watermark.PayloadLength)
		if err == nil && len(payloads) > 0 {
			payloadHex = watermark.MajorityVote(payloads)
		}
	} else {
		// Try Go-native detection first (handles both Go-embedded and Python-embedded files
		// once cross-compatibility testing confirms parameter alignment).
		payloadHex, err = watermark.GoInvisibleImageDetect(ctx, inputPath, watermark.PayloadLength)
		if err != nil || payloadHex == "" {
			slog.Debug("go invisible detect failed or empty, falling back to python", "error", err)
			// Fall back to Python detection for legacy files while Python is available.
			if p.cfg.ScriptsDir != "" {
				payloadHex, err = watermark.InvisibleImageDetect(ctx, inputPath, p.pythonPath(), p.detectScriptPath(), watermark.PayloadLength)
			}
		}
	}

	if err != nil {
		result := detectResult{
			Found:   false,
			Message: "No watermark detected in file",
		}
		return p.saveDetectResult(job.ID, result)
	}

	// Parse the payload
	payloadBytes, decErr := hex.DecodeString(payloadHex)
	if decErr != nil || len(payloadBytes) == 0 {
		result := detectResult{
			Found:      false,
			PayloadHex: payloadHex,
			Message:    "No valid watermark detected in file",
		}
		return p.saveDetectResult(job.ID, result)
	}

	// Try exact payload match first (CRC validates)
	tokenIDHex, _, valid := watermark.ParsePayload(payloadBytes)
	var tokenID, campaignID, recipientID string

	if valid {
		// Exact CRC match -- look up by exact token_id_hex
		var lookupErr error
		tokenID, campaignID, recipientID, lookupErr = db.LookupWatermarkIndex(p.database, tokenIDHex)
		if lookupErr != nil {
			tokenID = ""
		}
	}

	// Fallback: fuzzy matching (CRC failed or exact lookup failed)
	if tokenID == "" {
		fuzzyTokenHex, _, plausible := watermark.ParsePayloadFuzzy(payloadBytes)
		if plausible {
			var diffCount int
			tokenID, campaignID, recipientID, diffCount, _ = db.LookupWatermarkIndexFuzzy(p.database, fuzzyTokenHex, 8)
			if tokenID != "" {
				slog.Info("fuzzy watermark match", "job", job.ID, "diff_chars", diffCount)
			}
		}
	}

	if tokenID == "" {
		msg := "Watermark payload detected but no matching recipient found in database"
		if !valid {
			msg = "Watermark found but payload CRC check failed; fuzzy match also failed"
		}
		result := detectResult{
			Found:      false,
			PayloadHex: payloadHex,
			Message:    msg,
		}
		return p.saveDetectResult(job.ID, result)
	}

	// Load details
	result := detectResult{
		Found:      true,
		PayloadHex: payloadHex,
		TokenID:    tokenID,
		CampaignID: campaignID,
	}

	if campaign, err := db.GetCampaign(p.database, campaignID); err == nil && campaign != nil {
		result.CampaignName = campaign.Name
	}
	if recipient, err := db.GetRecipient(p.database, recipientID); err == nil && recipient != nil {
		result.RecipientName = recipient.Name
		result.RecipientEmail = recipient.Email
		result.RecipientOrg = recipient.Org
	}

	return p.saveDetectResult(job.ID, result)
}

func (p *Pool) saveDetectResult(jobID string, result detectResult) error {
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal detect result: %w", err)
	}
	return db.SetJobResult(p.database, jobID, string(data))
}

func (p *Pool) checkCampaignCompletion(campaignID string) {
	total, completed, failed, pending, running, err := db.CountJobsByCampaignDetailed(p.database, campaignID)
	if err != nil {
		slog.Error("count jobs", "campaign", campaignID, "error", err)
		return
	}

	if pending > 0 || running > 0 {
		return // still in progress
	}

	if total == 0 {
		return
	}

	var newState string
	switch {
	case failed == 0:
		newState = "READY"
	case completed == 0:
		newState = "FAILED"
	default:
		newState = "PARTIAL"
	}

	slog.Info("campaign completion", "campaign", campaignID, "state", newState, "completed", completed, "failed", failed)

	if err := db.UpdateCampaignState(p.database, campaignID, newState); err != nil {
		slog.Error("update campaign state", "campaign", campaignID, "error", err)
	}

	campaign, err := db.GetCampaign(p.database, campaignID)
	if err != nil || campaign == nil {
		return
	}

	account, _ := db.GetAccountByID(p.database, campaign.AccountID)

	// Dispatch webhook with state info
	if p.webhook != nil {
		p.webhook.Dispatch(campaign.AccountID, "campaign_ready", map[string]interface{}{
			"campaign_id":      campaignID,
			"campaign_name":    campaign.Name,
			"state":            newState,
			"total_tokens":     total,
			"completed_tokens": completed,
			"failed_tokens":    failed,
		})
	}

	// Send appropriate email
	if p.mailer != nil && p.mailer.Enabled() && account != nil {
		go func() {
			var emailErr error
			switch newState {
			case "READY":
				emailErr = p.mailer.SendCampaignReady(account.Email, account.Email, campaign.Name, completed)
			case "PARTIAL":
				emailErr = p.mailer.SendCampaignPartial(account.Email, account.Email, campaign.Name, completed, failed)
			case "FAILED":
				emailErr = p.mailer.SendCampaignFailed(account.Email, account.Email, campaign.Name, failed)
			}
			if emailErr != nil {
				slog.Error("send campaign completion email", "error", emailErr, "state", newState)
			}
		}()
	}
}

func (p *Pool) publishProgress(job *model.Job, progress int) {
	if p.sseHub == nil {
		return
	}
	data := fmt.Sprintf(`{"token_id":"%s","progress":%d}`, job.TokenID, progress)
	evt := sse.Event{Type: "progress", Data: data}
	p.sseHub.Publish("token:"+job.TokenID, evt)
	p.sseHub.Publish("campaign:"+job.CampaignID, evt)
}

func (p *Pool) publishTokenReady(job *model.Job) {
	if p.sseHub == nil {
		return
	}
	data := fmt.Sprintf(`{"token_id":"%s"}`, job.TokenID)
	evt := sse.Event{Type: "token_ready", Data: data}
	p.sseHub.Publish("token:"+job.TokenID, evt)
	p.sseHub.Publish("campaign:"+job.CampaignID, evt)
}

func (p *Pool) publishJobFailed(job *model.Job, errorMsg string) {
	if p.sseHub == nil {
		return
	}
	safeMsg := strings.ReplaceAll(errorMsg, `"`, `\"`)
	data := fmt.Sprintf(`{"token_id":"%s","error":"%s"}`, job.TokenID, safeMsg)
	evt := sse.Event{Type: "token_failed", Data: data}
	p.sseHub.Publish("token:"+job.TokenID, evt)
	p.sseHub.Publish("campaign:"+job.CampaignID, evt)
}

// notifyJobFailed sends an email to the campaign owner when a job fails permanently.
func (p *Pool) notifyJobFailed(job *model.Job, errorMsg string) {
	if p.mailer == nil || !p.mailer.Enabled() {
		return
	}
	go func() {
		token, _ := db.GetToken(p.database, job.TokenID)
		if token == nil {
			return
		}
		recipient, _ := db.GetRecipient(p.database, token.RecipientID)
		campaign, _ := db.GetCampaign(p.database, job.CampaignID)
		if recipient == nil || campaign == nil {
			return
		}
		account, _ := db.GetAccountByID(p.database, campaign.AccountID)
		if account == nil {
			return
		}
		if err := p.mailer.SendJobFailed(account.Email, account.Email, campaign.Name, recipient.Name, errorMsg); err != nil {
			slog.Error("send job failed email", "error", err)
		}
	}()
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
