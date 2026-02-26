package db

import (
	"database/sql"
	"strings"
	"time"

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

		for _, e := range strings.Split(w.Events, ",") {
			if strings.TrimSpace(e) == eventType {
				webhooks = append(webhooks, w)
				break
			}
		}
	}
	return webhooks, rows.Err()
}

func GetWebhookByID(database *sql.DB, id string) (*model.Webhook, error) {
	w := &model.Webhook{}
	var enabled int
	var createdAt SQLiteTime
	err := database.QueryRow(
		`SELECT id, account_id, url, secret, events, enabled, created_at FROM webhooks WHERE id = ?`, id,
	).Scan(&w.ID, &w.AccountID, &w.URL, &w.Secret, &w.Events, &enabled, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	w.Enabled = enabled != 0
	w.CreatedAt = createdAt.Time
	return w, nil
}

func CreateWebhookDelivery(database *sql.DB, d *model.WebhookDelivery) error {
	var nextRetryAt *string
	if d.NextRetryAt != nil {
		s := d.NextRetryAt.UTC().Format(time.RFC3339)
		nextRetryAt = &s
	}
	_, err := database.Exec(
		`INSERT INTO webhook_deliveries
		 (id, webhook_id, event_type, event_id, payload_json, attempt_number, state, next_retry_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.WebhookID, d.EventType, d.EventID, d.PayloadJSON,
		d.AttemptNumber, d.State, nextRetryAt,
	)
	return err
}

func UpdateWebhookDelivery(database *sql.DB, d *model.WebhookDelivery) error {
	var nextRetryAt, deliveredAt *string
	if d.NextRetryAt != nil {
		s := d.NextRetryAt.UTC().Format(time.RFC3339)
		nextRetryAt = &s
	}
	if d.DeliveredAt != nil {
		s := d.DeliveredAt.UTC().Format(time.RFC3339)
		deliveredAt = &s
	}
	_, err := database.Exec(
		`UPDATE webhook_deliveries
		 SET state = ?, attempt_number = ?, response_status = ?,
		     response_body_preview = ?, error_message = ?,
		     next_retry_at = ?, delivered_at = ?
		 WHERE id = ?`,
		d.State, d.AttemptNumber, d.ResponseStatus,
		d.ResponseBodyPreview, d.ErrorMessage,
		nextRetryAt, deliveredAt, d.ID,
	)
	return err
}

func ListDueWebhookDeliveries(database *sql.DB, now time.Time) ([]model.WebhookDelivery, error) {
	nowStr := now.UTC().Format(time.RFC3339)
	rows, err := database.Query(
		`SELECT id, webhook_id, event_type, event_id, payload_json, attempt_number, state, next_retry_at
		 FROM webhook_deliveries
		 WHERE state IN ('pending', 'failed') AND next_retry_at <= ?
		 ORDER BY next_retry_at ASC LIMIT 100`, nowStr)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deliveries []model.WebhookDelivery
	for rows.Next() {
		var d model.WebhookDelivery
		var nextRetryAt *string
		if err := rows.Scan(&d.ID, &d.WebhookID, &d.EventType, &d.EventID,
			&d.PayloadJSON, &d.AttemptNumber, &d.State, &nextRetryAt); err != nil {
			return nil, err
		}
		if nextRetryAt != nil {
			t, _ := time.Parse(time.RFC3339, *nextRetryAt)
			d.NextRetryAt = &t
		}
		deliveries = append(deliveries, d)
	}
	return deliveries, rows.Err()
}

func ListWebhookDeliveries(database *sql.DB, webhookID string, limit, offset int) ([]model.WebhookDelivery, error) {
	rows, err := database.Query(
		`SELECT id, webhook_id, event_type, event_id, attempt_number,
		        response_status, response_body_preview, error_message, state,
		        next_retry_at, delivered_at, created_at
		 FROM webhook_deliveries WHERE webhook_id = ?
		 ORDER BY created_at DESC LIMIT ? OFFSET ?`, webhookID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deliveries []model.WebhookDelivery
	for rows.Next() {
		var d model.WebhookDelivery
		var nextRetryAt, deliveredAt *string
		var createdAt SQLiteTime
		var respStatus *int
		if err := rows.Scan(&d.ID, &d.WebhookID, &d.EventType, &d.EventID,
			&d.AttemptNumber, &respStatus, &d.ResponseBodyPreview, &d.ErrorMessage,
			&d.State, &nextRetryAt, &deliveredAt, &createdAt); err != nil {
			return nil, err
		}
		d.ResponseStatus = respStatus
		d.CreatedAt = createdAt.Time
		if nextRetryAt != nil {
			t, _ := time.Parse(time.RFC3339, *nextRetryAt)
			d.NextRetryAt = &t
		}
		if deliveredAt != nil {
			t, _ := time.Parse(time.RFC3339, *deliveredAt)
			d.DeliveredAt = &t
		}
		deliveries = append(deliveries, d)
	}
	return deliveries, rows.Err()
}

func GetWebhookDelivery(database *sql.DB, id string) (*model.WebhookDelivery, error) {
	d := &model.WebhookDelivery{}
	var nextRetryAt, deliveredAt *string
	var createdAt SQLiteTime
	var respStatus *int
	err := database.QueryRow(
		`SELECT id, webhook_id, event_type, event_id, payload_json, attempt_number,
		        response_status, response_body_preview, error_message, state,
		        next_retry_at, delivered_at, created_at
		 FROM webhook_deliveries WHERE id = ?`, id,
	).Scan(&d.ID, &d.WebhookID, &d.EventType, &d.EventID, &d.PayloadJSON,
		&d.AttemptNumber, &respStatus, &d.ResponseBodyPreview, &d.ErrorMessage,
		&d.State, &nextRetryAt, &deliveredAt, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.ResponseStatus = respStatus
	d.CreatedAt = createdAt.Time
	if nextRetryAt != nil {
		t, _ := time.Parse(time.RFC3339, *nextRetryAt)
		d.NextRetryAt = &t
	}
	if deliveredAt != nil {
		t, _ := time.Parse(time.RFC3339, *deliveredAt)
		d.DeliveredAt = &t
	}
	return d, nil
}

func ReplayWebhookDelivery(database *sql.DB, id string) error {
	nowStr := time.Now().UTC().Format(time.RFC3339)
	_, err := database.Exec(
		`UPDATE webhook_deliveries
		 SET state = 'pending', attempt_number = 0, next_retry_at = ?,
		     error_message = '', response_status = NULL, response_body_preview = ''
		 WHERE id = ? AND state IN ('exhausted', 'delivered')`, nowStr, id)
	return err
}

func GetLastDeliveryPerWebhook(database *sql.DB, accountID string) (map[string]*model.WebhookDelivery, error) {
	rows, err := database.Query(
		`SELECT wd.webhook_id, wd.state, wd.created_at, wd.response_status, wd.error_message
		 FROM webhook_deliveries wd
		 JOIN webhooks w ON w.id = wd.webhook_id
		 WHERE w.account_id = ?
		   AND wd.created_at = (
		       SELECT MAX(wd2.created_at) FROM webhook_deliveries wd2
		       WHERE wd2.webhook_id = wd.webhook_id
		   )`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]*model.WebhookDelivery)
	for rows.Next() {
		var d model.WebhookDelivery
		var createdAt SQLiteTime
		var respStatus *int
		if err := rows.Scan(&d.WebhookID, &d.State, &createdAt, &respStatus, &d.ErrorMessage); err != nil {
			return nil, err
		}
		d.ResponseStatus = respStatus
		d.CreatedAt = createdAt.Time
		result[d.WebhookID] = &d
	}
	return result, rows.Err()
}

func CountExhaustedDeliveriesLast24h(database *sql.DB, accountID string) (int, error) {
	cutoff := time.Now().Add(time.Hour * -24).UTC().Format(time.RFC3339)
	var count int
	err := database.QueryRow(
		`SELECT COUNT(*) FROM webhook_deliveries wd
		 JOIN webhooks w ON w.id = wd.webhook_id
		 WHERE w.account_id = ? AND wd.state = 'exhausted'
		   AND wd.created_at >= ?`,
		accountID, cutoff,
	).Scan(&count)
	return count, err
}

func CountWebhookDeliveries(database *sql.DB, webhookID string) (int, error) {
	var count int
	err := database.QueryRow(
		`SELECT COUNT(*) FROM webhook_deliveries WHERE webhook_id = ?`, webhookID,
	).Scan(&count)
	return count, err
}

func PruneOldWebhookDeliveries(database *sql.DB, cutoff time.Time) (int64, error) {
	res, err := database.Exec(
		`DELETE FROM webhook_deliveries
		 WHERE created_at < ? AND state IN ('delivered', 'exhausted')`,
		cutoff.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
