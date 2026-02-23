package db

import (
	"database/sql"
	"time"
)

// DailyDownloadCount holds the download count for a single date.
type DailyDownloadCount struct {
	Date  string
	Count int
}

// CampaignAnalytics holds per-campaign download statistics.
type CampaignAnalytics struct {
	CampaignID       string
	CampaignName     string
	TotalDownloads   int
	UniqueRecipients int
	LastDownload     *time.Time
}

// DownloadEvent holds a single download event for CSV export.
type DownloadEvent struct {
	CampaignName   string
	RecipientName  string
	RecipientEmail string
	DownloadedAt   time.Time
	IPAddress      string
}

// DashboardStats holds aggregate download counts for the dashboard.
type DashboardStats struct {
	DownloadsThisWeek  int
	DownloadsThisMonth int
	DownloadsAllTime   int
}

// CountDownloadsByDateRange returns daily download counts for the given date range,
// filtered by account_id through the campaigns table.
func CountDownloadsByDateRange(database *sql.DB, accountID, start, end string) ([]DailyDownloadCount, error) {
	rows, err := database.Query(`
		SELECT date(de.downloaded_at) AS d, COUNT(*)
		FROM download_events de
		JOIN campaigns c ON de.campaign_id = c.id
		WHERE c.account_id = ?
		  AND date(de.downloaded_at) BETWEEN ? AND ?
		GROUP BY d
		ORDER BY d`, accountID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var counts []DailyDownloadCount
	for rows.Next() {
		var dc DailyDownloadCount
		if err := rows.Scan(&dc.Date, &dc.Count); err != nil {
			return nil, err
		}
		counts = append(counts, dc)
	}
	return counts, rows.Err()
}

// CampaignAnalyticsByDateRange returns per-campaign download stats for the given
// date range, filtered by account_id.
func CampaignAnalyticsByDateRange(database *sql.DB, accountID, start, end string) ([]CampaignAnalytics, error) {
	rows, err := database.Query(`
		SELECT c.id, c.name, COUNT(de.id), COUNT(DISTINCT de.recipient_id), MAX(de.downloaded_at)
		FROM campaigns c
		JOIN download_events de ON de.campaign_id = c.id
		WHERE c.account_id = ?
		  AND date(de.downloaded_at) BETWEEN ? AND ?
		GROUP BY c.id
		ORDER BY COUNT(de.id) DESC`, accountID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var analytics []CampaignAnalytics
	for rows.Next() {
		var ca CampaignAnalytics
		var lastDL SQLiteTime
		if err := rows.Scan(&ca.CampaignID, &ca.CampaignName, &ca.TotalDownloads, &ca.UniqueRecipients, &lastDL); err != nil {
			return nil, err
		}
		if !lastDL.Time.IsZero() {
			t := lastDL.Time
			ca.LastDownload = &t
		}
		analytics = append(analytics, ca)
	}
	return analytics, rows.Err()
}

// ExportDownloadEvents returns all download events for the given date range,
// suitable for CSV export.
func ExportDownloadEvents(database *sql.DB, accountID, start, end string) ([]DownloadEvent, error) {
	rows, err := database.Query(`
		SELECT c.name, r.name, r.email, de.downloaded_at, de.ip_address
		FROM download_events de
		JOIN campaigns c ON de.campaign_id = c.id
		JOIN recipients r ON de.recipient_id = r.id
		WHERE c.account_id = ?
		  AND date(de.downloaded_at) BETWEEN ? AND ?
		ORDER BY de.downloaded_at DESC`, accountID, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []DownloadEvent
	for rows.Next() {
		var ev DownloadEvent
		var downloadedAt SQLiteTime
		if err := rows.Scan(&ev.CampaignName, &ev.RecipientName, &ev.RecipientEmail, &downloadedAt, &ev.IPAddress); err != nil {
			return nil, err
		}
		ev.DownloadedAt = downloadedAt.Time
		events = append(events, ev)
	}
	return events, rows.Err()
}

// GetDashboardStats returns aggregate download counts for the past week,
// past month, and all time.
func GetDashboardStats(database *sql.DB, accountID string) (DashboardStats, error) {
	var stats DashboardStats
	err := database.QueryRow(`
		SELECT
		  (SELECT COUNT(*) FROM download_events de JOIN campaigns c ON de.campaign_id = c.id
		   WHERE c.account_id = ? AND date(de.downloaded_at) >= date('now', '-7 days')),
		  (SELECT COUNT(*) FROM download_events de JOIN campaigns c ON de.campaign_id = c.id
		   WHERE c.account_id = ? AND date(de.downloaded_at) >= date('now', '-30 days')),
		  (SELECT COUNT(*) FROM download_events de JOIN campaigns c ON de.campaign_id = c.id
		   WHERE c.account_id = ?)`,
		accountID, accountID, accountID,
	).Scan(&stats.DownloadsThisWeek, &stats.DownloadsThisMonth, &stats.DownloadsAllTime)
	if err != nil {
		return DashboardStats{}, err
	}
	return stats, nil
}
