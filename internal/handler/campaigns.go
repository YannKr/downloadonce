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
	Jobs     map[string]model.Job // keyed by token_id
	BaseURL  string
}

func (h *Handler) CampaignList(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	campaigns, err := db.ListCampaigns(h.DB, accountID, false)
	if err != nil {
		slog.Error("list campaigns", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	h.renderAuth(w, r, "campaign_list.html", "Campaigns", campaigns)
}

func (h *Handler) CampaignNewForm(w http.ResponseWriter, r *http.Request) {
	assets, _ := db.ListAssets(h.DB)
	recipients, _ := db.ListRecipients(h.DB)
	h.renderAuth(w, r, "campaign_new.html", "New Campaign", campaignNewData{
		Assets:      assets,
		Recipients:  recipients,
		SelectedIDs: make(map[string]bool),
		VisibleWM:   true,
		InvisibleWM: true,
	})
}

func (h *Handler) CampaignCreate(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	r.ParseForm()

	assetID := r.FormValue("asset_id")
	name := strings.TrimSpace(r.FormValue("name"))
	recipientIDs := r.Form["recipient_ids"]

	if assetID == "" || name == "" || len(recipientIDs) == 0 {
		assets, _ := db.ListAssets(h.DB)
		recipients, _ := db.ListRecipients(h.DB)
		selected := make(map[string]bool)
		for _, rid := range recipientIDs {
			selected[rid] = true
		}
		h.render(w, r, "campaign_new.html", PageData{
			Title: "New Campaign", Authenticated: true,
			IsAdmin: auth.IsAdmin(r.Context()), UserName: auth.NameFromContext(r.Context()),
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
	if err != nil || asset == nil {
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

	db.InsertAuditLog(h.DB, accountID, "campaign_created", "campaign", campaign.ID, campaign.Name, r.RemoteAddr)
	http.Redirect(w, r, "/campaigns/"+campaign.ID, http.StatusSeeOther)
}

func (h *Handler) CampaignDetail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())
	isAdmin := auth.IsAdmin(r.Context())

	campaign, err := db.GetCampaign(h.DB, id)
	if err != nil || campaign == nil {
		http.NotFound(w, r)
		return
	}
	if campaign.AccountID != accountID && !isAdmin {
		http.NotFound(w, r)
		return
	}

	// Get campaign summary for display (use showAll for admin, filtered for member)
	campaigns, _ := db.ListCampaigns(h.DB, accountID, isAdmin)
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

	// Load jobs for progress display for tokens being watermarked on-demand
	jobMap := make(map[string]model.Job)
	{
		jobs, _ := db.ListJobsByCampaign(h.DB, id)
		for _, j := range jobs {
			if j.State == "PENDING" || j.State == "RUNNING" {
				jobMap[j.TokenID] = j
			}
		}
	}

	h.renderAuth(w, r, "campaign_detail.html", cs.Name, campaignDetailData{
		Campaign: *cs,
		Asset:    *asset,
		Tokens:   tokens,
		Jobs:     jobMap,
		BaseURL:  h.Cfg.BaseURL,
	})
}

func (h *Handler) CampaignPublish(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	campaign, err := db.GetCampaign(h.DB, id)
	if err != nil || campaign == nil || (campaign.AccountID != accountID && !auth.IsAdmin(r.Context())) {
		http.NotFound(w, r)
		return
	}

	if campaign.State != "DRAFT" {
		http.Redirect(w, r, "/campaigns/"+id, http.StatusSeeOther)
		return
	}

	tokens, _ := db.ListTokensByCampaign(h.DB, id)
	if len(tokens) == 0 {
		http.Error(w, "No recipients", 400)
		return
	}

	// Set campaign directly to READY — watermarking happens on-demand
	db.SetCampaignPublishedReady(h.DB, id)
	db.InsertAuditLog(h.DB, accountID, "campaign_published", "campaign", id, campaign.Name, r.RemoteAddr)

	// Send download link emails if SMTP is configured
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

	setFlash(w, "Campaign published.")
	http.Redirect(w, r, "/campaigns/"+id, http.StatusSeeOther)
}

func (h *Handler) TokenRevoke(w http.ResponseWriter, r *http.Request) {
	campaignID := chi.URLParam(r, "id")
	tokenID := chi.URLParam(r, "tokenID")
	accountID := auth.AccountFromContext(r.Context())

	campaign, err := db.GetCampaign(h.DB, campaignID)
	if err != nil || campaign == nil || (campaign.AccountID != accountID && !auth.IsAdmin(r.Context())) {
		http.NotFound(w, r)
		return
	}

	db.ExpireToken(h.DB, tokenID)
	db.InsertAuditLog(h.DB, accountID, "token_revoked", "token", tokenID, "", r.RemoteAddr)
	setFlash(w, "Token revoked.")
	http.Redirect(w, r, "/campaigns/"+campaignID, http.StatusSeeOther)
}
