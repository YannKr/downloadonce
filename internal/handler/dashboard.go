package handler

import (
	"net/http"

	"github.com/ypk/downloadonce/internal/auth"
	"github.com/ypk/downloadonce/internal/db"
)

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())

	assets, _ := db.ListAssets(h.DB)
	campaigns, _ := db.ListCampaigns(h.DB, accountID, false)
	events, _ := db.ListRecentDownloadEvents(h.DB, accountID, 20)

	totalDownloads := 0
	for _, c := range campaigns {
		totalDownloads += c.DownloadedCount
	}

	type dashData struct {
		TotalAssets    int
		TotalCampaigns int
		TotalDownloads int
		Campaigns      interface{}
		Events         interface{}
	}

	h.renderAuth(w, r, "dashboard.html", "Dashboard", dashData{
		TotalAssets:    len(assets),
		TotalCampaigns: len(campaigns),
		TotalDownloads: totalDownloads,
		Campaigns:      campaigns,
		Events:         events,
	})
}
