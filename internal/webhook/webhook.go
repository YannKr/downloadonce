package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
)

var backoffSchedule = []time.Duration{
	30 * time.Second,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
}

func nextRetryAt(attemptNumber int) *time.Time {
	idx := attemptNumber - 1
	if idx >= len(backoffSchedule) {
		return nil
	}
	t := time.Now().Add(backoffSchedule[idx])
	return &t
}

type Dispatcher struct {
	DB *sql.DB
}

type Event struct {
	EventType string      `json:"event_type"`
	EventID   string      `json:"event_id"`
	Timestamp string      `json:"timestamp"`
	Data      interface{} `json:"data"`
}

func (d *Dispatcher) Dispatch(accountID, eventType string, data interface{}) {
	if d == nil || d.DB == nil {
		return
	}

	webhooks, err := db.ListEnabledWebhooks(d.DB, accountID, eventType)
	if err != nil {
		slog.Error("webhook lookup", "error", err)
		return
	}
	if len(webhooks) == 0 {
		return
	}

	eventID := uuid.New().String()
	event := Event{
		EventType: eventType,
		EventID:   eventID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data:      data,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("webhook marshal", "error", err)
		return
	}

	now := time.Now()
	for _, wh := range webhooks {
		delivery := &model.WebhookDelivery{
			ID:            uuid.New().String(),
			WebhookID:     wh.ID,
			EventType:     eventType,
			EventID:       eventID,
			PayloadJSON:   string(payload),
			AttemptNumber: 1,
			State:         "pending",
			NextRetryAt:   &now,
		}
		if err := db.CreateWebhookDelivery(d.DB, delivery); err != nil {
			slog.Error("webhook: create delivery record", "error", err)
			continue
		}
		go attemptAndRecord(d.DB, &wh, delivery)
	}
}

func attemptAndRecord(database *sql.DB, wh *model.Webhook, delivery *model.WebhookDelivery) {
	payload := []byte(delivery.PayloadJSON)
	status, preview, err := postWebhook(wh.URL, wh.Secret, payload)

	delivery.ResponseStatus = status
	delivery.ResponseBodyPreview = preview

	if err == nil {
		now := time.Now()
		delivery.State = "delivered"
		delivery.NextRetryAt = nil
		delivery.DeliveredAt = &now
		delivery.ErrorMessage = ""
		slog.Info("webhook delivered", "url", wh.URL, "event", delivery.EventType)
	} else {
		delivery.ErrorMessage = err.Error()
		nextAt := nextRetryAt(delivery.AttemptNumber)
		if nextAt == nil {
			delivery.State = "exhausted"
			delivery.NextRetryAt = nil
			slog.Warn("webhook exhausted", "url", wh.URL, "event", delivery.EventType, "attempts", delivery.AttemptNumber)
		} else {
			delivery.State = "failed"
			delivery.NextRetryAt = nextAt
			slog.Warn("webhook failed, will retry", "url", wh.URL, "event", delivery.EventType,
				"attempt", delivery.AttemptNumber, "next_retry", nextAt)
		}
	}

	if uerr := db.UpdateWebhookDelivery(database, delivery); uerr != nil {
		slog.Error("webhook: update delivery record", "error", uerr)
	}
}

func postWebhook(url, secret string, payload []byte) (statusCode *int, preview string, err error) {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	signature := hex.EncodeToString(mac.Sum(nil))

	req, reqErr := http.NewRequest("POST", url, bytes.NewReader(payload))
	if reqErr != nil {
		return nil, "", fmt.Errorf("create request: %w", reqErr)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DownloadOnce-Signature", "sha256="+signature)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, respErr := client.Do(req)
	if respErr != nil {
		return nil, "", fmt.Errorf("post: %w", respErr)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
	preview = string(body)
	code := resp.StatusCode
	statusCode = &code

	if resp.StatusCode >= 400 {
		return statusCode, preview, fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return statusCode, preview, nil
}
