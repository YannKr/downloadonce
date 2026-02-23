package cleanup

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/ypk/downloadonce/internal/db"
)

type Cleaner struct {
	DB       *sql.DB
	DataDir  string
	Interval time.Duration
	cancel   context.CancelFunc
	done     chan struct{}
}

func (c *Cleaner) Start(ctx context.Context) {
	ctx, c.cancel = context.WithCancel(ctx)
	c.done = make(chan struct{})
	go c.loop(ctx)
	slog.Info("cleanup scheduler started", "interval", c.Interval)
}

func (c *Cleaner) Stop() {
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
	slog.Info("cleanup scheduler stopped")
}

func (c *Cleaner) loop(ctx context.Context) {
	defer close(c.done)

	// Run once at startup
	c.runOnce()

	ticker := time.NewTicker(c.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runOnce()
		}
	}
}

func (c *Cleaner) runOnce() {
	campaigns, err := db.ListExpiredCampaigns(c.DB)
	if err != nil {
		slog.Error("cleanup: list expired campaigns", "error", err)
		return
	}

	for _, campaign := range campaigns {
		slog.Info("expiring campaign", "id", campaign.ID, "name", campaign.Name)

		if err := db.ExpireCampaignAndTokens(c.DB, campaign.ID); err != nil {
			slog.Error("cleanup: expire campaign", "id", campaign.ID, "error", err)
			continue
		}

		// Remove watermarked files
		wmDir := filepath.Join(c.DataDir, "watermarked", campaign.ID)
		if err := os.RemoveAll(wmDir); err != nil {
			slog.Warn("cleanup: remove watermarked dir", "dir", wmDir, "error", err)
		} else {
			slog.Info("cleanup: removed watermarked files", "campaign", campaign.ID)
		}
	}
}
