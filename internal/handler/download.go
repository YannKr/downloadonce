package handler

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
)

type downloadPageData struct {
	Campaign  *model.Campaign
	Asset     *model.Asset
	Recipient *model.Recipient
	Token     *model.DownloadToken
	BaseURL   string
}

func (h *Handler) DownloadPage(w http.ResponseWriter, r *http.Request) {
	tokenStr := chi.URLParam(r, "token")
	if _, err := uuid.Parse(tokenStr); err != nil {
		h.render(w, r, "download_expired.html", PageData{Title: "Not Found"})
		return
	}

	token, err := db.GetToken(h.DB, tokenStr)
	if err != nil || token == nil {
		h.render(w, r, "download_expired.html", PageData{Title: "Not Found"})
		return
	}

	switch token.State {
	case "PENDING":
		// On-demand watermarking: enqueue job if not already running
		campaign, _ := db.GetCampaign(h.DB, token.CampaignID)
		if campaign == nil || campaign.State == "DRAFT" {
			h.render(w, r, "download_preparing.html", PageData{
				Title: "Not Ready",
				Data:  map[string]interface{}{"TokenID": token.ID, "Progress": 0},
			})
			return
		}

		asset, _ := db.GetAsset(h.DB, campaign.AssetID)
		if asset == nil {
			h.render(w, r, "download_expired.html", PageData{Title: "Error"})
			return
		}

		jobType := "watermark_video"
		if asset.AssetType == "image" {
			jobType = "watermark_image"
		}

		job := &model.Job{
			ID:         uuid.New().String(),
			JobType:    jobType,
			CampaignID: token.CampaignID,
			TokenID:    token.ID,
		}
		_, err := db.EnqueueJobIfNotExists(h.DB, job)
		if err != nil {
			slog.Error("enqueue on-demand job", "error", err, "token", token.ID)
		}

		// Get current job progress
		progress := 0
		existingJob, _ := db.GetJobByToken(h.DB, token.ID)
		if existingJob != nil {
			progress = existingJob.Progress
		}

		h.render(w, r, "download_preparing.html", PageData{
			Title: "Preparing",
			Data:  map[string]interface{}{"TokenID": token.ID, "Progress": progress},
		})
		return
	case "CONSUMED":
		h.render(w, r, "download_expired.html", PageData{Title: "Link Used"})
		return
	case "EXPIRED":
		h.render(w, r, "download_expired.html", PageData{Title: "Link Expired"})
		return
	}

	// Check expiry
	if token.ExpiresAt != nil && token.ExpiresAt.Before(time.Now()) {
		db.ExpireToken(h.DB, token.ID)
		h.render(w, r, "download_expired.html", PageData{Title: "Link Expired"})
		return
	}

	campaign, _ := db.GetCampaign(h.DB, token.CampaignID)
	asset, _ := db.GetAsset(h.DB, campaign.AssetID)
	recipient, _ := db.GetRecipient(h.DB, token.RecipientID)

	h.render(w, r, "download.html", PageData{
		Title: campaign.Name,
		Data: downloadPageData{
			Campaign:  campaign,
			Asset:     asset,
			Recipient: recipient,
			Token:     token,
			BaseURL:   h.Cfg.BaseURL,
		},
	})
}

func (h *Handler) DownloadFile(w http.ResponseWriter, r *http.Request) {
	tokenStr := chi.URLParam(r, "token")
	if _, err := uuid.Parse(tokenStr); err != nil {
		http.NotFound(w, r)
		return
	}

	token, err := db.GetToken(h.DB, tokenStr)
	if err != nil || token == nil || token.State != "ACTIVE" {
		http.NotFound(w, r)
		return
	}

	if token.ExpiresAt != nil && token.ExpiresAt.Before(time.Now()) {
		db.ExpireToken(h.DB, token.ID)
		http.Error(w, "Link expired", http.StatusGone)
		return
	}

	if token.WatermarkedPath == nil {
		http.Error(w, "File not ready", http.StatusServiceUnavailable)
		return
	}

	_, consumed, err := db.IncrementDownloadCount(h.DB, token.ID)
	if err != nil {
		http.Error(w, "Internal error", 500)
		return
	}
	_ = consumed

	campaign, _ := db.GetCampaign(h.DB, token.CampaignID)

	event := &model.DownloadEvent{
		ID:          uuid.New().String(),
		TokenID:     token.ID,
		CampaignID:  token.CampaignID,
		RecipientID: token.RecipientID,
		AssetID:     campaign.AssetID,
		IPAddress:   realIP(r),
		UserAgent:   r.UserAgent(),
	}
	db.InsertDownloadEvent(h.DB, event)

	// Dispatch download webhook
	recipient, _ := db.GetRecipient(h.DB, token.RecipientID)
	if h.Webhook != nil {
		webhookData := map[string]interface{}{
			"token_id":      token.ID,
			"campaign_id":   token.CampaignID,
			"campaign_name": campaign.Name,
			"recipient_id":  token.RecipientID,
			"ip_address":    event.IPAddress,
		}
		if recipient != nil {
			webhookData["recipient_name"] = recipient.Name
			webhookData["recipient_email"] = recipient.Email
		}
		h.Webhook.Dispatch(campaign.AccountID, "download", webhookData)
	}

	// Send download notification email to campaign owner if enabled
	if h.Mailer != nil && h.Mailer.Enabled() {
		owner, _ := db.GetAccountByID(h.DB, campaign.AccountID)
		if owner != nil && owner.NotifyOnDownload {
			recipientName := ""
			recipientEmail := ""
			if recipient != nil {
				recipientName = recipient.Name
				recipientEmail = recipient.Email
			}
			downloadTime := time.Now().UTC().Format("2006-01-02 15:04 UTC")
			ipAddress := event.IPAddress
			go func() {
				if err := h.Mailer.SendDownloadNotification(owner.Email, owner.Name, campaign.Name, recipientName, recipientEmail, downloadTime, ipAddress); err != nil {
					slog.Error("send download notification", "error", err)
				}
			}()
		}
	}

	filePath := filepath.Join(h.Cfg.DataDir, *token.WatermarkedPath)
	ext := filepath.Ext(filePath)
	filename := sanitizeFilename(campaign.Name) + ext

	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, filename))
	http.ServeFile(w, r, filePath)
}

func realIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	s := replacer.Replace(name)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
