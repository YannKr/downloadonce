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

	downloadonce "github.com/YannKr/downloadonce"
	"github.com/YannKr/downloadonce/internal/cleanup"
	"github.com/YannKr/downloadonce/internal/config"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/diskstat"
	"github.com/YannKr/downloadonce/internal/email"
	"github.com/YannKr/downloadonce/internal/handler"
	"github.com/YannKr/downloadonce/internal/sse"
	"github.com/YannKr/downloadonce/internal/webhook"
	"github.com/YannKr/downloadonce/internal/worker"
)

func Run(ctx context.Context, cfg *config.Config) error {
	for _, dir := range []string{cfg.DataDir, cfg.DataDir + "/originals", cfg.DataDir + "/watermarked", cfg.DataDir + "/detect", cfg.DataDir + "/uploads"} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	scriptsDir, err := extractScripts()
	if err != nil {
		return err
	}
	defer os.RemoveAll(scriptsDir)
	cfg.ScriptsDir = scriptsDir
	slog.Info("scripts extracted", "dir", scriptsDir)

	database, err := db.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer database.Close()

	if err := db.Migrate(database, downloadonce.MigrationFS); err != nil {
		return err
	}
	slog.Info("database ready")

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

	webhookDispatcher := &webhook.Dispatcher{DB: database}

	cleaner := &cleanup.Cleaner{
		DB:       database,
		DataDir:  cfg.DataDir,
		Interval: time.Duration(cfg.CleanupIntervalMins) * time.Minute,
	}
	cleaner.Start(ctx)
	defer cleaner.Stop()

	sseHub := sse.New()

	pool := worker.NewPool(database, cfg, mailer, webhookDispatcher, sseHub)
	pool.Start(ctx)
	defer pool.Stop()

	retrier := &webhook.Retrier{DB: database, Interval: 30 * time.Second}
	retrier.Start(ctx)

	templateFS, err := fs.Sub(downloadonce.TemplateFS, "templates")
	if err != nil {
		return err
	}

	staticFS, err := fs.Sub(downloadonce.StaticFS, "static")
	if err != nil {
		return err
	}

	authRL := handler.NewRateLimiter(5.0/60.0, 5)
	defer authRL.Stop()

	diskCache := diskstat.New(cfg.DataDir, 60*time.Second)
	diskCache.Start()
	defer diskCache.Stop()

	h := handler.New(database, cfg, templateFS, mailer, webhookDispatcher, sseHub)
	h.DiskCache = diskCache
	router := h.Routes(staticFS, authRL)

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: router,
	}

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
