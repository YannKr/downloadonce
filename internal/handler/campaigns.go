package handler

import (
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YannKr/downloadonce/internal/auth"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
)

type campaignNewData struct {
	Assets         []model.Asset
	Recipients     []model.Recipient
	Groups         []model.RecipientGroupSummary
	Name           string
	AssetID        string
	MaxDownloads   string
	ExpiresAt      string
	SelectedIDs    map[string]bool
	SelectedGroups map[string]bool
	VisibleWM      bool
	InvisibleWM    bool
}

type campaignDetailData struct {
	Campaign            model.CampaignSummary
	Asset               model.Asset
	Tokens              []model.TokenWithRecipient
	Jobs                map[string]model.Job // keyed by token_id
	BaseURL             string
	AvailableRecipients []model.Recipient
}

func (h *Handler) CampaignList(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	showArchived := r.URL.Query().Get("archived") == "1"
	campaigns, err := db.ListCampaigns(h.DB, accountID, false, showArchived)
	if err != nil {
		slog.Error("list campaigns", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	h.renderAuth(w, r, "campaign_list.html", "Campaigns", map[string]interface{}{
		"Campaigns":    campaigns,
		"ShowArchived": showArchived,
	})
}

func (h *Handler) CampaignNewForm(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	assets, _ := db.ListAssets(h.DB)
	recipients, _ := db.ListRecipients(h.DB)
	groups, _ := db.ListRecipientGroups(h.DB, accountID)
	h.renderAuth(w, r, "campaign_new.html", "New Campaign", campaignNewData{
		Assets:         assets,
		Recipients:     recipients,
		Groups:         groups,
		SelectedIDs:    make(map[string]bool),
		SelectedGroups: make(map[string]bool),
		VisibleWM:      true,
		InvisibleWM:    true,
	})
}

func (h *Handler) CampaignCreate(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	r.ParseForm()

	assetID := r.FormValue("asset_id")
	name := strings.TrimSpace(r.FormValue("name"))
	recipientIDs := r.Form["recipient_ids"]
	groupIDs := r.Form["group_ids"]

	// Expand groups and deduplicate with directly selected recipients
	seen := make(map[string]struct{})
	finalIDs := make([]string, 0)
	for _, rid := range recipientIDs {
		if _, ok := seen[rid]; !ok {
			seen[rid] = struct{}{}
			finalIDs = append(finalIDs, rid)
		}
	}
	for _, gid := range groupIDs {
		members, _ := db.ListGroupMemberIDs(h.DB, gid, accountID)
		for _, rid := range members {
			if _, ok := seen[rid]; !ok {
				seen[rid] = struct{}{}
				finalIDs = append(finalIDs, rid)
			}
		}
	}

	if assetID == "" || name == "" || len(finalIDs) == 0 {
		assets, _ := db.ListAssets(h.DB)
		recipients, _ := db.ListRecipients(h.DB)
		groups, _ := db.ListRecipientGroups(h.DB, accountID)
		selected := make(map[string]bool)
		for _, rid := range recipientIDs {
			selected[rid] = true
		}
		selectedGroups := make(map[string]bool)
		for _, gid := range groupIDs {
			selectedGroups[gid] = true
		}
		h.render(w, r, "campaign_new.html", PageData{
			Title: "New Campaign", Authenticated: true,
			IsAdmin: auth.IsAdmin(r.Context()), UserName: auth.NameFromContext(r.Context()),
			Error: "Asset, name, and at least one recipient or group are required.",
			Data: campaignNewData{
				Assets:         assets,
				Recipients:     recipients,
				Groups:         groups,
				Name:           name,
				AssetID:        assetID,
				MaxDownloads:   r.FormValue("max_downloads"),
				ExpiresAt:      r.FormValue("expires_at"),
				SelectedIDs:    selected,
				SelectedGroups: selectedGroups,
				VisibleWM:      r.FormValue("visible_wm") == "on",
				InvisibleWM:    r.FormValue("invisible_wm") == "on",
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

	for _, rid := range finalIDs {
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
	campaigns, _ := db.ListCampaigns(h.DB, accountID, isAdmin, false)
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

	// Build set of already-added recipient IDs for filtering
	added := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		added[t.RecipientID] = struct{}{}
	}
	allRecipients, _ := db.ListRecipients(h.DB)
	var available []model.Recipient
	for _, rec := range allRecipients {
		if _, ok := added[rec.ID]; !ok {
			available = append(available, rec)
		}
	}

	h.renderAuth(w, r, "campaign_detail.html", cs.Name, campaignDetailData{
		Campaign:            *cs,
		Asset:               *asset,
		Tokens:              tokens,
		Jobs:                jobMap,
		BaseURL:             h.Cfg.BaseURL,
		AvailableRecipients: available,
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

	asset, err := db.GetAsset(h.DB, campaign.AssetID)
	if err != nil || asset == nil {
		http.Error(w, "Asset not found", 500)
		return
	}

	jobType := "watermark_video"
	if asset.AssetType == "image" {
		jobType = "watermark_image"
	}

	// Set campaign to PROCESSING and enqueue one watermark job per token
	db.SetCampaignPublished(h.DB, id)
	for _, t := range tokens {
		job := &model.Job{
			ID:         uuid.New().String(),
			JobType:    jobType,
			CampaignID: id,
			TokenID:    t.ID,
		}
		if err := db.EnqueueJob(h.DB, job); err != nil {
			slog.Error("enqueue watermark job", "error", err, "token", t.ID)
		}
	}
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

	setFlash(w, "Campaign published. Watermarking in progress.")
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

func (h *Handler) CampaignClone(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	src, err := db.GetCampaign(h.DB, id)
	if err != nil || src == nil || (src.AccountID != accountID && !auth.IsAdmin(r.Context())) {
		http.NotFound(w, r)
		return
	}

	srcTokens, err := db.ListTokensByCampaign(h.DB, id)
	if err != nil {
		slog.Error("clone: list tokens", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	recipientIDs := make([]string, 0, len(srcTokens))
	for _, t := range srcTokens {
		recipientIDs = append(recipientIDs, t.RecipientID)
	}

	r.ParseForm()

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = src.Name + " (copy)"
	}

	assetID := r.FormValue("asset_id")
	if assetID == "" {
		assetID = src.AssetID
	}

	assetMissing := false
	if a, _ := db.GetAsset(h.DB, assetID); a == nil {
		assetMissing = true
		assetID = ""
	}

	newExpiry := src.ExpiresAt
	if raw := r.FormValue("expires_at"); raw != "" {
		if t, terr := time.Parse("2006-01-02T15:04", raw); terr == nil {
			newExpiry = &t
		}
	}

	newCampaign := &model.Campaign{
		ID:          uuid.New().String(),
		AccountID:   accountID,
		AssetID:     assetID,
		Name:        name,
		MaxDownloads: src.MaxDownloads,
		ExpiresAt:   newExpiry,
		VisibleWM:   src.VisibleWM,
		InvisibleWM: src.InvisibleWM,
		State:       "DRAFT",
	}

	skipped, err := db.CloneCampaign(h.DB, newCampaign, recipientIDs)
	if err != nil {
		slog.Error("clone campaign", "src", id, "error", err)
		http.Error(w, "Internal error", 500)
		return
	}

	db.InsertAuditLog(h.DB, accountID, "campaign_cloned", "campaign", newCampaign.ID, newCampaign.Name, r.RemoteAddr)

	flashMsg := "Campaign cloned successfully."
	if assetMissing {
		flashMsg = "Campaign cloned, but the original asset no longer exists — please select a new asset before publishing."
	} else if skipped > 0 {
		flashMsg = fmt.Sprintf("Campaign cloned. %d recipient(s) were skipped because they no longer exist.", skipped)
	}
	setFlash(w, flashMsg)
	http.Redirect(w, r, "/campaigns/"+newCampaign.ID, http.StatusSeeOther)
}

func (h *Handler) CampaignExportLinks(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	campaign, err := db.GetCampaign(h.DB, id)
	if err != nil || campaign == nil || (campaign.AccountID != accountID && !auth.IsAdmin(r.Context())) {
		http.NotFound(w, r)
		return
	}

	switch campaign.State {
	case "PROCESSING", "READY", "EXPIRED":
		// allowed
	default:
		http.Error(w, "Export is only available after a campaign has been published.", http.StatusBadRequest)
		return
	}

	tokens, err := db.ListTokensByCampaign(h.DB, id)
	if err != nil {
		slog.Error("export-links: list tokens", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}

	format := r.URL.Query().Get("format")
	safeName := sanitizeFilename(campaign.Name)

	switch format {
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="%s-links.csv"`, safeName))
		wr := csv.NewWriter(w)
		wr.Write([]string{"name", "email", "org", "download_url", "token_state", "download_count"})
		for _, t := range tokens {
			wr.Write([]string{
				t.RecipientName, t.RecipientEmail, t.RecipientOrg,
				h.Cfg.BaseURL + "/d/" + t.ID,
				t.State, strconv.Itoa(t.DownloadCount),
			})
		}
		wr.Flush()
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="%s-links.txt"`, safeName))
		for _, t := range tokens {
			fmt.Fprintf(w, "%s <%s> → %s\n",
				t.RecipientName, t.RecipientEmail, h.Cfg.BaseURL+"/d/"+t.ID)
		}
	}
}

func (h *Handler) CampaignAddRecipients(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	campaign, err := db.GetCampaign(h.DB, id)
	if err != nil || campaign == nil || (campaign.AccountID != accountID && !auth.IsAdmin(r.Context())) {
		http.NotFound(w, r)
		return
	}

	switch campaign.State {
	case "DRAFT", "PROCESSING", "READY":
		// allowed
	default:
		http.Error(w, "Cannot add recipients to a campaign in state "+campaign.State, http.StatusBadRequest)
		return
	}

	r.ParseForm()
	recipientIDs := r.Form["recipient_ids"]
	if len(recipientIDs) == 0 {
		setFlash(w, "No recipients selected.")
		http.Redirect(w, r, "/campaigns/"+id, http.StatusSeeOther)
		return
	}

	asset, err := db.GetAsset(h.DB, campaign.AssetID)
	if err != nil || asset == nil {
		http.Error(w, "Asset not found", http.StatusInternalServerError)
		return
	}

	jobType := "watermark_video"
	if asset.AssetType == "image" {
		jobType = "watermark_image"
	}

	added := 0
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
			slog.Error("add recipient token", "error", err, "recipient_id", rid)
			continue
		}
		// For published campaigns, immediately enqueue a watermark job
		if campaign.State == "PROCESSING" || campaign.State == "READY" {
			job := &model.Job{
				ID:         uuid.New().String(),
				JobType:    jobType,
				CampaignID: campaign.ID,
				TokenID:    token.ID,
			}
			if err := db.EnqueueJob(h.DB, job); err != nil {
				slog.Error("enqueue watermark job for new token", "error", err, "token", token.ID)
			}
		}
		added++
	}

	// Put campaign back to PROCESSING so the worker picks up the new jobs
	if added > 0 && campaign.State == "READY" {
		db.UpdateCampaignState(h.DB, id, "PROCESSING")
	}

	db.InsertAuditLog(h.DB, accountID, "recipients_added", "campaign", id, campaign.Name, r.RemoteAddr)
	setFlash(w, fmt.Sprintf("%d recipient(s) added.", added))
	http.Redirect(w, r, "/campaigns/"+id, http.StatusSeeOther)
}

func (h *Handler) CampaignArchive(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	campaign, err := db.GetCampaign(h.DB, id)
	if err != nil || campaign == nil || (campaign.AccountID != accountID && !auth.IsAdmin(r.Context())) {
		http.NotFound(w, r)
		return
	}
	if campaign.State == "ARCHIVED" {
		http.Redirect(w, r, "/campaigns", http.StatusSeeOther)
		return
	}

	if err := db.ArchiveCampaign(h.DB, id); err != nil {
		slog.Error("archive campaign", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	db.InsertAuditLog(h.DB, accountID, "campaign_archived", "campaign", id, campaign.Name, r.RemoteAddr)
	setFlash(w, "Campaign archived.")
	http.Redirect(w, r, "/campaigns", http.StatusSeeOther)
}
