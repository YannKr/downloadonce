package app

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	downloadonce "github.com/ypk/downloadonce"
	"github.com/ypk/downloadonce/internal/cleanup"
	"github.com/ypk/downloadonce/internal/config"
	"github.com/ypk/downloadonce/internal/db"
	"github.com/ypk/downloadonce/internal/diskstat"
	"github.com/ypk/downloadonce/internal/email"
	"github.com/ypk/downloadonce/internal/handler"
	"github.com/ypk/downloadonce/internal/sse"
	"github.com/ypk/downloadonce/internal/webhook"
	"github.com/ypk/downloadonce/internal/worker"
)

func Run(ctx context.Context, cfg *config.Config) error {
	// Ensure data directories exist
	for _, dir := range []string{cfg.DataDir, cfg.DataDir + "/originals", cfg.DataDir + "/watermarked", cfg.DataDir + "/detect", cfg.DataDir + "/uploads"} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	// Extract embedded Python scripts to a temp directory
	scriptsDir, err := extractScripts()
	if err != nil {
		return err
	}
	defer os.RemoveAll(scriptsDir)
	cfg.ScriptsDir = scriptsDir
	slog.Info("scripts extracted", "dir", scriptsDir)

	// Open database
	database, err := db.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer database.Close()

	// Run migrations
	if err := db.Migrate(database, downloadonce.MigrationFS); err != nil {
		return err
	}
	slog.Info("database ready")

	// Init email mailer
	mailer := &email.Mailer{
		Host: cfg.SMTPHost,
		Port: cfg.SMTPPort,
		User: cfg.SMTPUser,
		Pass: cfg.SMTPPass,
		From: cfg.SMTPFrom,
	}
	if mailer.Enabled() {
		slog.Info("email enabled", "host", cfg.SMTPHost, "from", cfg.SMTPFrom)
	}

	// Init webhook dispatcher
	webhookDispatcher := &webhook.Dispatcher{DB: database}

	// Start cleanup scheduler
	cleaner := &cleanup.Cleaner{
		DB:       database,
		DataDir:  cfg.DataDir,
		Interval: time.Duration(cfg.CleanupIntervalMins) * time.Minute,
	}
	cleaner.Start(ctx)
	defer cleaner.Stop()

	// Create SSE hub for real-time updates
	sseHub := sse.New()

	// Start worker pool
	pool := worker.NewPool(database, cfg, mailer, webhookDispatcher, sseHub)
	pool.Start(ctx)
	defer pool.Stop()

	// Get template FS (sub-directory)
	templateFS, err := fs.Sub(downloadonce.TemplateFS, "templates")
	if err != nil {
		return err
	}

	// Get static FS (sub-directory)
	staticFS, err := fs.Sub(downloadonce.StaticFS, "static")
	if err != nil {
		return err
	}

	// Rate limiter for auth endpoints: 5 requests/minute, burst of 5
	authRL := handler.NewRateLimiter(5.0/60.0, 5)
	defer authRL.Stop()

	// Start disk stats cache
	diskCache := diskstat.New(cfg.DataDir, 60*time.Second)
	diskCache.Start()
	defer diskCache.Stop()

	// Build handler and routes
	h := handler.New(database, cfg, templateFS, mailer, webhookDispatcher, sseHub)
	h.DiskCache = diskCache
	router := h.Routes(staticFS, authRL)

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: router,
	}

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		slog.Info("shutting down server")
		srv.Shutdown(context.Background())
	}()

	slog.Info("server starting", "addr", cfg.ListenAddr, "base_url", cfg.BaseURL)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

// extractScripts writes the embedded Python scripts to a temporary directory
// so they can be invoked as subprocesses.
func extractScripts() (string, error) {
	dir, err := os.MkdirTemp("", "downloadonce-scripts-*")
	if err != nil {
		return "", err
	}

	entries, err := fs.ReadDir(downloadonce.ScriptFS, "scripts")
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(downloadonce.ScriptFS, "scripts/"+e.Name())
		if err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, 0644); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}

	return dir, nil
}
