package webhook

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/YannKr/downloadonce/internal/db"
)

type Retrier struct {
	DB       *sql.DB
	Interval time.Duration
}

func (r *Retrier) Start(ctx context.Context) {
	if r.Interval == 0 {
		r.Interval = 30 * time.Second
	}
	go r.loop(ctx)
	slog.Info("webhook retrier started", "interval", r.Interval)
}

func (r *Retrier) loop(ctx context.Context) {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnce()
		}
	}
}

func (r *Retrier) runOnce() {
	deliveries, err := db.ListDueWebhookDeliveries(r.DB, time.Now())
	if err != nil {
		slog.Error("webhook retrier: list due deliveries", "error", err)
		return
	}
	for i := range deliveries {
		d := &deliveries[i]
		wh, err := db.GetWebhookByID(r.DB, d.WebhookID)
		if err != nil || wh == nil {
			continue
		}
		d.AttemptNumber++
		attemptAndRecord(r.DB, wh, d)
	}
}
