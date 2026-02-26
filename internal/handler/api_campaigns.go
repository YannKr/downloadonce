package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YannKr/downloadonce/internal/auth"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
)

type apiCampaign struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	AssetID         string  `json:"asset_id"`
	State           string  `json:"state"`
	MaxDownloads    *int    `json:"max_downloads"`
	ExpiresAt       *string `json:"expires_at"`
	VisibleWM       bool    `json:"visible_wm"`
	InvisibleWM     bool    `json:"invisible_wm"`
	JobsTotal       int     `json:"jobs_total"`
	JobsCompleted   int     `json:"jobs_completed"`
	JobsFailed      int     `json:"jobs_failed"`
	RecipientCount  int     `json:"recipient_count"`
	DownloadedCount int     `json:"downloaded_count"`
	CreatedAt       string  `json:"created_at"`
	PublishedAt     *string `json:"published_at"`
}

type apiToken struct {
	ID             string  `json:"id"`
	CampaignID     string  `json:"campaign_id"`
	RecipientID    string  `json:"recipient_id"`
	RecipientName  string  `json:"recipient_name"`
	RecipientEmail string  `json:"recipient_email"`
	RecipientOrg   string  `json:"recipient_org"`
	State          string  `json:"state"`
	DownloadCount  int     `json:"download_count"`
	MaxDownloads   *int    `json:"max_downloads"`
	LastDownloadAt *string `json:"last_download_at"`
	ExpiresAt      *string `json:"expires_at"`
	DownloadURL    string  `json:"download_url"`
	CreatedAt      string  `json:"created_at"`
}

func campaignToAPI(c *model.Campaign, jobsTotal, jobsCompleted, jobsFailed, recipientCount, downloadedCount int) apiCampaign {
	ac := apiCampaign{
		ID:              c.ID,
		Name:            c.Name,
		AssetID:         c.AssetID,
		State:           c.State,
		MaxDownloads:    c.MaxDownloads,
		VisibleWM:       c.VisibleWM,
		InvisibleWM:     c.InvisibleWM,
		JobsTotal:       jobsTotal,
		JobsCompleted:   jobsCompleted,
		JobsFailed:      jobsFailed,
		RecipientCount:  recipientCount,
		DownloadedCount: downloadedCount,
		CreatedAt:       c.CreatedAt.UTC().Format(time.RFC3339),
	}
	if c.ExpiresAt != nil {
		s := c.ExpiresAt.UTC().Format(time.RFC3339)
		ac.ExpiresAt = &s
	}
	if c.PublishedAt != nil {
		s := c.PublishedAt.UTC().Format(time.RFC3339)
		ac.PublishedAt = &s
	}
	return ac
}

func tokenToAPI(t *model.TokenWithRecipient, downloadURL string) apiToken {
	at := apiToken{
		ID:             t.ID,
		CampaignID:     t.CampaignID,
		RecipientID:    t.RecipientID,
		RecipientName:  t.RecipientName,
		RecipientEmail: t.RecipientEmail,
		RecipientOrg:   t.RecipientOrg,
		State:          t.State,
		DownloadCount:  t.DownloadCount,
		MaxDownloads:   t.MaxDownloads,
		DownloadURL:    downloadURL,
		CreatedAt:      t.CreatedAt.UTC().Format(time.RFC3339),
	}
	if t.LastDownloadAt != nil {
		s := t.LastDownloadAt.UTC().Format(time.RFC3339)
		at.LastDownloadAt = &s
	}
	if t.ExpiresAt != nil {
		s := t.ExpiresAt.UTC().Format(time.RFC3339)
		at.ExpiresAt = &s
	}
	return at
}

// APICampaignCreate - POST /api/v1/campaigns
func (h *Handler) APICampaignCreate(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())

	var body struct {
		Name         string   `json:"name"`
		AssetID      string   `json:"asset_id"`
		RecipientIDs []string `json:"recipient_ids"`
		MaxDownloads *int     `json:"max_downloads"`
		ExpiresAt    string   `json:"expires_at"`
		VisibleWM    bool     `json:"visible_wm"`
		InvisibleWM  bool     `json:"invisible_wm"`
		AutoPublish  bool     `json:"auto_publish"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	if body.Name == "" {
		renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "name is required")
		return
	}
	if body.AssetID == "" {
		renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "asset_id is required")
		return
	}
	if len(body.RecipientIDs) == 0 {
		renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "recipient_ids must be a non-empty array")
		return
	}

	asset, err := db.GetAsset(h.DB, body.AssetID)
	if err != nil {
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get asset")
		return
	}
	if asset == nil {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "asset not found")
		return
	}

	campaign := &model.Campaign{
		ID:           uuid.New().String(),
		AccountID:    accountID,
		AssetID:      body.AssetID,
		Name:         body.Name,
		MaxDownloads: body.MaxDownloads,
		VisibleWM:    body.VisibleWM,
		InvisibleWM:  body.InvisibleWM,
		State:        "DRAFT",
	}

	if body.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, body.ExpiresAt)
		if err != nil {
			renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid expires_at format, use RFC3339")
			return
		}
		campaign.ExpiresAt = &t
	}

	if err := db.CreateCampaign(h.DB, campaign); err != nil {
		slog.Error("api create campaign", "error", err)
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create campaign")
		return
	}

	tokens := make([]*model.DownloadToken, 0, len(body.RecipientIDs))
	for _, rid := range body.RecipientIDs {
		token := &model.DownloadToken{
			ID:           uuid.New().String(),
			CampaignID:   campaign.ID,
			RecipientID:  rid,
			MaxDownloads: campaign.MaxDownloads,
			State:        "PENDING",
			ExpiresAt:    campaign.ExpiresAt,
		}
		if err := db.CreateToken(h.DB, token); err != nil {
			slog.Error("api create token", "error", err, "recipient_id", rid)
			continue
		}
		tokens = append(tokens, token)
	}

	if body.AutoPublish {
		db.SetCampaignPublishedReady(h.DB, campaign.ID)
		campaign.State = "READY"
		now := time.Now()
		campaign.PublishedAt = &now
	}

	db.InsertAuditLog(h.DB, accountID, "campaign_created", "campaign", campaign.ID, campaign.Name, r.RemoteAddr)

	jobsTotal, jobsCompleted, jobsFailed, _ := db.CountJobsByCampaign(h.DB, campaign.ID)
	ac := campaignToAPI(campaign, jobsTotal, jobsCompleted, jobsFailed, len(tokens), 0)
	renderJSON(w, http.StatusCreated, ac)
}

// APICampaignGet - GET /api/v1/campaigns/{id}
func (h *Handler) APICampaignGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	campaign, err := db.GetCampaign(h.DB, id)
	if err != nil {
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get campaign")
		return
	}
	if campaign == nil {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found")
		return
	}
	if campaign.AccountID != accountID && !auth.IsAdmin(r.Context()) {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found")
		return
	}

	jobsTotal, jobsCompleted, jobsFailed, _ := db.CountJobsByCampaign(h.DB, id)
	tokens, _ := db.ListTokensByCampaign(h.DB, id)

	downloadedCount := 0
	for _, t := range tokens {
		if t.DownloadCount > 0 {
			downloadedCount++
		}
	}

	ac := campaignToAPI(campaign, jobsTotal, jobsCompleted, jobsFailed, len(tokens), downloadedCount)
	renderJSON(w, http.StatusOK, ac)
}

// APICampaignPublish - POST /api/v1/campaigns/{id}/publish
func (h *Handler) APICampaignPublish(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	campaign, err := db.GetCampaign(h.DB, id)
	if err != nil {
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get campaign")
		return
	}
	if campaign == nil {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found")
		return
	}
	if campaign.AccountID != accountID && !auth.IsAdmin(r.Context()) {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found")
		return
	}

	if campaign.State != "DRAFT" {
		renderJSONError(w, http.StatusConflict, "CONFLICT", "campaign is not in DRAFT state")
		return
	}

	tokens, err := db.ListTokensByCampaign(h.DB, id)
	if err != nil {
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list tokens")
		return
	}
	if len(tokens) == 0 {
		renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "no recipients attached")
		return
	}

	asset, err := db.GetAsset(h.DB, campaign.AssetID)
	if err != nil || asset == nil {
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "asset not found")
		return
	}

	jobType := "watermark_video"
	if asset.AssetType == "image" {
		jobType = "watermark_image"
	}

	db.SetCampaignPublished(h.DB, id)
	for _, t := range tokens {
		job := &model.Job{
			ID:         uuid.New().String(),
			JobType:    jobType,
			CampaignID: id,
			TokenID:    t.ID,
		}
		if err := db.EnqueueJob(h.DB, job); err != nil {
			slog.Error("api enqueue watermark job", "error", err, "token", t.ID)
		}
	}
	db.InsertAuditLog(h.DB, accountID, "campaign_published", "campaign", id, campaign.Name, r.RemoteAddr)

	if h.Mailer != nil && h.Mailer.Enabled() {
		for _, t := range tokens {
			downloadURL := h.Cfg.BaseURL + "/d/" + t.ID
			go func(toEmail, name, url string) {
				if err := h.Mailer.SendDownloadLink(toEmail, name, campaign.Name, url); err != nil {
					slog.Error("send download email", "error", err, "to", toEmail)
				}
			}(t.RecipientEmail, t.RecipientName, downloadURL)
		}
	}

	campaign, _ = db.GetCampaign(h.DB, id)

	downloadedCount := 0
	for _, t := range tokens {
		if t.DownloadCount > 0 {
			downloadedCount++
		}
	}
	jobsTotal, jobsCompleted, jobsFailed, _ := db.CountJobsByCampaign(h.DB, id)
	ac := campaignToAPI(campaign, jobsTotal, jobsCompleted, jobsFailed, len(tokens), downloadedCount)
	renderJSON(w, http.StatusOK, ac)
}

// APICampaignTokenList - GET /api/v1/campaigns/{id}/tokens
func (h *Handler) APICampaignTokenList(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	campaign, err := db.GetCampaign(h.DB, id)
	if err != nil {
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get campaign")
		return
	}
	if campaign == nil {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found")
		return
	}
	if campaign.AccountID != accountID && !auth.IsAdmin(r.Context()) {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found")
		return
	}

	tokens, err := db.ListTokensByCampaign(h.DB, id)
	if err != nil {
		slog.Error("api list tokens", "error", err)
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list tokens")
		return
	}

	page, perPage := paginate(r)
	total := len(tokens)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	slice := tokens[start:end]

	result := make([]apiToken, len(slice))
	for i, t := range slice {
		downloadURL := h.Cfg.BaseURL + "/d/" + t.ID
		result[i] = tokenToAPI(&t, downloadURL)
	}

	renderJSON(w, http.StatusOK, paginatedResult{
		Data:    result,
		Total:   total,
		Page:    page,
		PerPage: perPage,
	})
}

// APICampaignAddRecipients - POST /api/v1/campaigns/{id}/recipients
func (h *Handler) APICampaignAddRecipients(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	campaign, err := db.GetCampaign(h.DB, id)
	if err != nil {
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get campaign")
		return
	}
	if campaign == nil {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found")
		return
	}
	if campaign.AccountID != accountID && !auth.IsAdmin(r.Context()) {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found")
		return
	}

	switch campaign.State {
	case "DRAFT", "PROCESSING", "READY":
		// allowed
	default:
		renderJSONError(w, http.StatusConflict, "CONFLICT", "cannot add recipients to a campaign in state "+campaign.State)
		return
	}

	var body struct {
		RecipientIDs []string `json:"recipient_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}

	asset, err := db.GetAsset(h.DB, campaign.AssetID)
	if err != nil || asset == nil {
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "asset not found")
		return
	}
	jobType := "watermark_video"
	if asset.AssetType == "image" {
		jobType = "watermark_image"
	}

	added := 0
	skipped := 0
	for _, rid := range body.RecipientIDs {
		rec, err := db.GetRecipient(h.DB, rid)
		if err != nil || rec == nil {
			skipped++
			continue
		}
		token := &model.DownloadToken{
			ID:           uuid.New().String(),
			CampaignID:   campaign.ID,
			RecipientID:  rid,
			MaxDownloads: campaign.MaxDownloads,
			State:        "PENDING",
			ExpiresAt:    campaign.ExpiresAt,
		}
		if err := db.CreateToken(h.DB, token); err != nil {
			slog.Error("api add recipient token", "error", err, "recipient_id", rid)
			skipped++
			continue
		}
		if campaign.State == "PROCESSING" || campaign.State == "READY" {
			job := &model.Job{
				ID:         uuid.New().String(),
				JobType:    jobType,
				CampaignID: campaign.ID,
				TokenID:    token.ID,
			}
			if err := db.EnqueueJob(h.DB, job); err != nil {
				slog.Error("api enqueue watermark job for new token", "error", err, "token", token.ID)
			}
		}
		added++
	}

	if added > 0 && campaign.State == "READY" {
		db.UpdateCampaignState(h.DB, campaign.ID, "PROCESSING")
	}

	renderJSON(w, http.StatusOK, map[string]int{"added": added, "skipped": skipped})
}

// APICampaignRevokeToken - DELETE /api/v1/campaigns/{id}/tokens/{tokenID}
func (h *Handler) APICampaignRevokeToken(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tokenID := chi.URLParam(r, "tokenID")
	accountID := auth.AccountFromContext(r.Context())

	campaign, err := db.GetCampaign(h.DB, id)
	if err != nil {
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get campaign")
		return
	}
	if campaign == nil {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found")
		return
	}
	if campaign.AccountID != accountID && !auth.IsAdmin(r.Context()) {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "campaign not found")
		return
	}

	db.ExpireToken(h.DB, tokenID)
	db.InsertAuditLog(h.DB, accountID, "token_revoked", "token", tokenID, "", r.RemoteAddr)

	w.WriteHeader(http.StatusNoContent)
}
