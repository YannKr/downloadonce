package db

import (
	"database/sql"

	"github.com/YannKr/downloadonce/internal/model"
)

func InsertDownloadEvent(database *sql.DB, e *model.DownloadEvent) error {
	_, err := database.Exec(
		`INSERT INTO download_events (id, token_id, campaign_id, recipient_id, asset_id, ip_address, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.TokenID, e.CampaignID, e.RecipientID, e.AssetID, e.IPAddress, e.UserAgent,
	)
	return err
}

func ListDownloadEventsByToken(database *sql.DB, tokenID string) ([]model.DownloadEvent, error) {
	rows, err := database.Query(
		`SELECT id, token_id, campaign_id, recipient_id, asset_id, ip_address, user_agent, downloaded_at
		 FROM download_events WHERE token_id = ? ORDER BY downloaded_at DESC`, tokenID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []model.DownloadEvent
	for rows.Next() {
		var e model.DownloadEvent
		var createdAt SQLiteTime
		if err := rows.Scan(&e.ID, &e.TokenID, &e.CampaignID, &e.RecipientID,
			&e.AssetID, &e.IPAddress, &e.UserAgent, &createdAt); err != nil {
			return nil, err
		}
		e.CreatedAt = createdAt.Time
		events = append(events, e)
	}
	return events, rows.Err()
}

func ListRecentDownloadEvents(database *sql.DB, accountID string, limit int) ([]model.DownloadEvent, error) {
	rows, err := database.Query(`
		SELECT de.id, de.token_id, de.campaign_id, de.recipient_id, de.asset_id,
		  de.ip_address, de.user_agent, de.downloaded_at
		FROM download_events de
		JOIN campaigns c ON c.id = de.campaign_id
		WHERE c.account_id = ?
		ORDER BY de.downloaded_at DESC
		LIMIT ?`, accountID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []model.DownloadEvent
	for rows.Next() {
		var e model.DownloadEvent
		var createdAt SQLiteTime
		if err := rows.Scan(&e.ID, &e.TokenID, &e.CampaignID, &e.RecipientID,
			&e.AssetID, &e.IPAddress, &e.UserAgent, &createdAt); err != nil {
			return nil, err
		}
		e.CreatedAt = createdAt.Time
		events = append(events, e)
	}
	return events, rows.Err()
}
