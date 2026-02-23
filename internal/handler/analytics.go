package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"time"

	"github.com/ypk/downloadonce/internal/auth"
	"github.com/ypk/downloadonce/internal/db"
)

type analyticsData struct {
	Start             string
	End               string
	DailyCounts       []db.DailyDownloadCount
	CampaignAnalytics []db.CampaignAnalytics
	TotalDownloads    int
}

func (h *Handler) Analytics(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())

	end := r.URL.Query().Get("end")
	start := r.URL.Query().Get("start")
	if end == "" {
		end = time.Now().Format("2006-01-02")
	}
	if start == "" {
		start = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	}

	daily, _ := db.CountDownloadsByDateRange(h.DB, accountID, start, end)
	campaigns, _ := db.CampaignAnalyticsByDateRange(h.DB, accountID, start, end)

	total := 0
	for _, d := range daily {
		total += d.Count
	}

	h.renderAuth(w, r, "analytics.html", "Analytics", analyticsData{
		Start:             start,
		End:               end,
		DailyCounts:       daily,
		CampaignAnalytics: campaigns,
		TotalDownloads:    total,
	})
}

func (h *Handler) AnalyticsExport(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())

	end := r.URL.Query().Get("end")
	start := r.URL.Query().Get("start")
	if end == "" {
		end = time.Now().Format("2006-01-02")
	}
	if start == "" {
		start = time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	}

	events, err := db.ExportDownloadEvents(h.DB, accountID, start, end)
	if err != nil {
		http.Error(w, "Internal error", 500)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=downloads_%s_%s.csv", start, end))

	writer := csv.NewWriter(w)
	writer.Write([]string{"Campaign", "Recipient", "Email", "Downloaded At", "IP Address"})
	for _, e := range events {
		writer.Write([]string{e.CampaignName, e.RecipientName, e.RecipientEmail, e.DownloadedAt.Format("2006-01-02 15:04:05"), e.IPAddress})
	}
	writer.Flush()
}
