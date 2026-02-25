package db

import (
	"database/sql"
	"strings"

	"github.com/YannKr/downloadonce/internal/model"
)

func CreateWebhook(database *sql.DB, w *model.Webhook) error {
	_, err := database.Exec(
		`INSERT INTO webhooks (id, account_id, url, secret, events, enabled) VALUES (?, ?, ?, ?, ?, ?)`,
		w.ID, w.AccountID, w.URL, w.Secret, w.Events, boolToInt(w.Enabled),
	)
	return err
}

func ListWebhooks(database *sql.DB, accountID string) ([]model.Webhook, error) {
	rows, err := database.Query(
		`SELECT id, account_id, url, secret, events, enabled, created_at
		 FROM webhooks WHERE account_id = ? ORDER BY created_at DESC`, accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var webhooks []model.Webhook
	for rows.Next() {
		var w model.Webhook
		var enabled int
		var createdAt SQLiteTime
		if err := rows.Scan(&w.ID, &w.AccountID, &w.URL, &w.Secret, &w.Events, &enabled, &createdAt); err != nil {
			return nil, err
		}
		w.Enabled = enabled != 0
		w.CreatedAt = createdAt.Time
		webhooks = append(webhooks, w)
	}
	return webhooks, rows.Err()
}

func DeleteWebhook(database *sql.DB, id, accountID string) error {
	_, err := database.Exec(`DELETE FROM webhooks WHERE id = ? AND account_id = ?`, id, accountID)
	return err
}

func ListEnabledWebhooks(database *sql.DB, accountID, eventType string) ([]model.Webhook, error) {
	rows, err := database.Query(
		`SELECT id, account_id, url, secret, events, enabled, created_at
		 FROM webhooks WHERE account_id = ? AND enabled = 1 ORDER BY created_at ASC`, accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var webhooks []model.Webhook
	for rows.Next() {
		var w model.Webhook
		var enabled int
		var createdAt SQLiteTime
		if err := rows.Scan(&w.ID, &w.AccountID, &w.URL, &w.Secret, &w.Events, &enabled, &createdAt); err != nil {
			return nil, err
		}
		w.Enabled = enabled != 0
		w.CreatedAt = createdAt.Time

		// Filter by event type
		for _, e := range strings.Split(w.Events, ",") {
			if strings.TrimSpace(e) == eventType {
				webhooks = append(webhooks, w)
				break
			}
		}
	}
	return webhooks, rows.Err()
}
