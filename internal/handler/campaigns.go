package handler

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ypk/downloadonce/internal/auth"
	"github.com/ypk/downloadonce/internal/db"
	"github.com/ypk/downloadonce/internal/model"
)

type campaignNewData struct {
	Assets       []model.Asset
	Recipients   []model.Recipient
	Name         string
	AssetID      string
	MaxDownloads string
	ExpiresAt    string
	SelectedIDs  map[string]bool
	VisibleWM    bool
	InvisibleWM  bool
}

type campaignDetailData struct {
	Campaign model.CampaignSummary
	Asset    model.Asset
	Tokens   []model.TokenWithRecipient
	BaseURL  string
}

func (h *Handler) CampaignList(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	campaigns, err := db.ListCampaigns(h.DB, accountID)
	if err != nil {
		slog.Error("list campaigns", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	h.render(w, "campaign_list.html", PageData{
		Title:         "Campaigns",
		Authenticated: true,
		Data:          campaigns,
	})
}

func (h *Handler) CampaignNewForm(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	assets, _ := db.ListAssets(h.DB, accountID)
	recipients, _ := db.ListRecipients(h.DB, accountID)
	h.render(w, "campaign_new.html", PageData{
		Title:         "New Campaign",
		Authenticated: true,
		Data: campaignNewData{
			Assets:      assets,
			Recipients:  recipients,
			SelectedIDs: make(map[string]bool),
			VisibleWM:   true,
			InvisibleWM: true,
		},
	})
}

func (h *Handler) CampaignCreate(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	r.ParseForm()

	assetID := r.FormValue("asset_id")
	name := strings.TrimSpace(r.FormValue("name"))
	recipientIDs := r.Form["recipient_ids"]

	if assetID == "" || name == "" || len(recipientIDs) == 0 {
		assets, _ := db.ListAssets(h.DB, accountID)
		recipients, _ := db.ListRecipients(h.DB, accountID)
		selected := make(map[string]bool)
		for _, rid := range recipientIDs {
			selected[rid] = true
		}
		h.render(w, "campaign_new.html", PageData{
			Title: "New Campaign", Authenticated: true,
			Error: "Asset, name, and at least one recipient are required.",
			Data: campaignNewData{
				Assets:       assets,
				Recipients:   recipients,
				Name:         name,
				AssetID:      assetID,
				MaxDownloads: r.FormValue("max_downloads"),
				ExpiresAt:    r.FormValue("expires_at"),
				SelectedIDs:  selected,
				VisibleWM:    r.FormValue("visible_wm") == "on",
				InvisibleWM:  r.FormValue("invisible_wm") == "on",
			},
		})
		return
	}

	asset, err := db.GetAsset(h.DB, assetID)
	if err != nil || asset == nil || asset.AccountID != accountID {
		http.Error(w, "Invalid asset", 400)
		return
	}

	campaign := &model.Campaign{
		ID:          uuid.New().String(),
		AccountID:   accountID,
		AssetID:     assetID,
		Name:        name,
		VisibleWM:   r.FormValue("visible_wm") == "on",
		InvisibleWM: r.FormValue("invisible_wm") == "on",
		State:       "DRAFT",
	}

	if maxDL := r.FormValue("max_downloads"); maxDL != "" {
		if n, err := strconv.Atoi(maxDL); err == nil && n > 0 {
			campaign.MaxDownloads = &n
		}
	}

	if expiry := r.FormValue("expires_at"); expiry != "" {
		if t, err := time.Parse("2006-01-02T15:04", expiry); err == nil {
			campaign.ExpiresAt = &t
		}
	}

	if err := db.CreateCampaign(h.DB, campaign); err != nil {
		slog.Error("create campaign", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}

	for _, rid := range recipientIDs {
		token := &model.DownloadToken{
			ID:           uuid.New().String(),
			CampaignID:   campaign.ID,
			RecipientID:  rid,
			MaxDownloads: campaign.MaxDownloads,
			State:        "PENDING",
			ExpiresAt:    campaign.ExpiresAt,
		}
		if err := db.CreateToken(h.DB, token); err != nil {
			slog.Error("create token", "error", err)
			continue
		}
	}

	http.Redirect(w, r, "/campaigns/"+campaign.ID, http.StatusSeeOther)
}

func (h *Handler) CampaignDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	campaigns, _ := db.ListCampaigns(h.DB, accountID)
	var cs *model.CampaignSummary
	for i := range campaigns {
		if campaigns[i].ID == id {
			cs = &campaigns[i]
			break
		}
	}
	if cs == nil {
		http.NotFound(w, r)
		return
	}

	asset, _ := db.GetAsset(h.DB, cs.AssetID)
	if asset == nil {
		http.NotFound(w, r)
		return
	}

	tokens, _ := db.ListTokensByCampaign(h.DB, id)

	// Load download events for each token
	for i := range tokens {
		events, _ := db.ListDownloadEventsByToken(h.DB, tokens[i].ID)
		tokens[i].DownloadEvents = events
	}

	h.render(w, "campaign_detail.html", PageData{
		Title:         cs.Name,
		Authenticated: true,
		Data: campaignDetailData{
			Campaign: *cs,
			Asset:    *asset,
			Tokens:   tokens,
			BaseURL:  h.Cfg.BaseURL,
		},
	})
}

func (h *Handler) CampaignPublish(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	campaign, err := db.GetCampaign(h.DB, id)
	if err != nil || campaign == nil || campaign.AccountID != accountID {
		http.NotFound(w, r)
		return
	}

	if campaign.State != "DRAFT" {
		http.Redirect(w, r, "/campaigns/"+id, http.StatusSeeOther)
		return
	}

	asset, _ := db.GetAsset(h.DB, campaign.AssetID)
	if asset == nil {
		http.Error(w, "Asset not found", 400)
		return
	}

	tokens, _ := db.ListTokensByCampaign(h.DB, id)
	if len(tokens) == 0 {
		http.Error(w, "No recipients", 400)
		return
	}

	// Mark campaign as processing
	db.SetCampaignPublished(h.DB, id)

	// Determine job type
	jobType := "watermark_video"
	if asset.AssetType == "image" {
		jobType = "watermark_image"
	}

	// Create a job for each token
	for _, t := range tokens {
		job := &model.Job{
			ID:         uuid.New().String(),
			JobType:    jobType,
			CampaignID: id,
			TokenID:    t.ID,
		}
		if err := db.EnqueueJob(h.DB, job); err != nil {
			slog.Error("enqueue job", "error", err, "token", t.ID)
		}
	}

	http.Redirect(w, r, "/campaigns/"+id, http.StatusSeeOther)
}

func (h *Handler) TokenRevoke(w http.ResponseWriter, r *http.Request) {
	campaignID := chi.URLParam(r, "id")
	tokenID := chi.URLParam(r, "tokenID")

	db.ExpireToken(h.DB, tokenID)

	http.Redirect(w, r, "/campaigns/"+campaignID, http.StatusSeeOther)
}
