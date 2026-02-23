package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ypk/downloadonce/internal/auth"
	"github.com/ypk/downloadonce/internal/db"
	"github.com/ypk/downloadonce/internal/model"
)

type settingsData struct {
	APIKeys     []model.APIKey
	Webhooks    []model.Webhook
	NewAPIKey   string // shown once after creation
	SMTPEnabled bool
}

func (h *Handler) SettingsPage(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	keys, _ := db.ListAPIKeys(h.DB, accountID)
	webhooks, _ := db.ListWebhooks(h.DB, accountID)

	h.renderAuth(w, r, "settings.html", "Settings", settingsData{
		APIKeys:     keys,
		Webhooks:    webhooks,
		SMTPEnabled: h.Cfg.SMTPHost != "",
	})
}

func (h *Handler) APIKeyCreate(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	name := r.FormValue("name")
	if name == "" {
		name = "Unnamed key"
	}

	// Generate key: do_ + 32 hex bytes (64 chars)
	rawKey, err := auth.GenerateToken(32)
	if err != nil {
		http.Error(w, "Internal error", 500)
		return
	}
	fullKey := "do_" + rawKey
	prefix := rawKey[:8]

	hash, err := auth.HashPassword(fullKey)
	if err != nil {
		http.Error(w, "Internal error", 500)
		return
	}

	apiKey := &model.APIKey{
		ID:        uuid.New().String(),
		AccountID: accountID,
		Name:      name,
		KeyPrefix: prefix,
		KeyHash:   hash,
	}
	if err := db.CreateAPIKey(h.DB, apiKey); err != nil {
		http.Error(w, "Internal error", 500)
		return
	}

	// Show the key once
	keys, _ := db.ListAPIKeys(h.DB, accountID)
	webhooks, _ := db.ListWebhooks(h.DB, accountID)

	h.render(w, "settings.html", PageData{
		Title:         "Settings",
		Authenticated: true,
		IsAdmin:       auth.IsAdmin(r.Context()),
		UserName:      auth.NameFromContext(r.Context()),
		Flash:         "API key created. Copy it now — it won't be shown again.",
		Data: settingsData{
			APIKeys:     keys,
			Webhooks:    webhooks,
			NewAPIKey:   fullKey,
			SMTPEnabled: h.Cfg.SMTPHost != "",
		},
	})
}

func (h *Handler) APIKeyDelete(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	id := chi.URLParam(r, "id")
	db.DeleteAPIKey(h.DB, id, accountID)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *Handler) WebhookCreate(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	url := r.FormValue("url")
	if url == "" {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}

	events := r.Form["events"]
	if len(events) == 0 {
		events = []string{"download"}
	}

	secret, err := auth.GenerateToken(16)
	if err != nil {
		http.Error(w, "Internal error", 500)
		return
	}

	eventsStr := ""
	for i, e := range events {
		if i > 0 {
			eventsStr += ","
		}
		eventsStr += e
	}

	wh := &model.Webhook{
		ID:        uuid.New().String(),
		AccountID: accountID,
		URL:       url,
		Secret:    secret,
		Events:    eventsStr,
		Enabled:   true,
	}
	if err := db.CreateWebhook(h.DB, wh); err != nil {
		http.Error(w, "Internal error", 500)
		return
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *Handler) WebhookDelete(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	id := chi.URLParam(r, "id")
	db.DeleteWebhook(h.DB, id, accountID)
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
