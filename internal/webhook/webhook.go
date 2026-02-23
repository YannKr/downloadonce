package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ypk/downloadonce/internal/db"
)

type Dispatcher struct {
	DB *sql.DB
}

type Event struct {
	Type      string      `json:"type"`
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

	event := Event{
		Type:      eventType,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data:      data,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		slog.Error("webhook marshal", "error", err)
		return
	}

	for _, wh := range webhooks {
		go func(url, secret string) {
			if err := postWebhook(url, secret, payload); err != nil {
				slog.Warn("webhook delivery failed", "url", url, "error", err)
			} else {
				slog.Info("webhook delivered", "url", url, "event", eventType)
			}
		}(wh.URL, wh.Secret)
	}
}

func postWebhook(url, secret string, payload []byte) error {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	signature := hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature-256", "sha256="+signature)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}
