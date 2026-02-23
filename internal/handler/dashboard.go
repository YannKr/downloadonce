package handler

import (
	"net/http"

	"github.com/ypk/downloadonce/internal/auth"
	"github.com/ypk/downloadonce/internal/db"
)

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())

	assets, _ := db.ListAssets(h.DB, accountID)
	campaigns, _ := db.ListCampaigns(h.DB, accountID)
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

	h.render(w, "dashboard.html", PageData{
		Title:         "Dashboard",
		Authenticated: true,
		Data: dashData{
			TotalAssets:    len(assets),
			TotalCampaigns: len(campaigns),
			TotalDownloads: totalDownloads,
			Campaigns:      campaigns,
			Events:         events,
		},
	})
}
