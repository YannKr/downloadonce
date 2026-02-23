package handler

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ypk/downloadonce/internal/db"
	"github.com/ypk/downloadonce/internal/model"
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
		h.render(w, "download_expired.html", PageData{Title: "Not Found"})
		return
	}

	token, err := db.GetToken(h.DB, tokenStr)
	if err != nil || token == nil {
		h.render(w, "download_expired.html", PageData{Title: "Not Found"})
		return
	}

	switch token.State {
	case "PENDING":
		// Check campaign progress
		total, completed, _, _ := db.CountJobsByCampaign(h.DB, token.CampaignID)
		h.render(w, "download_preparing.html", PageData{
			Title: "Preparing",
			Data: map[string]interface{}{
				"Total":     total,
				"Completed": completed,
			},
		})
		return
	case "CONSUMED":
		h.render(w, "download_expired.html", PageData{Title: "Link Used"})
		return
	case "EXPIRED":
		h.render(w, "download_expired.html", PageData{Title: "Link Expired"})
		return
	}

	// Check expiry
	if token.ExpiresAt != nil && token.ExpiresAt.Before(time.Now()) {
		db.ExpireToken(h.DB, token.ID)
		h.render(w, "download_expired.html", PageData{Title: "Link Expired"})
		return
	}

	campaign, _ := db.GetCampaign(h.DB, token.CampaignID)
	asset, _ := db.GetAsset(h.DB, campaign.AssetID)
	recipient, _ := db.GetRecipient(h.DB, token.RecipientID)

	h.render(w, "download.html", PageData{
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
	if _, err := os.Stat("/dev/null"); err == nil {
		// additional trimming if needed
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
