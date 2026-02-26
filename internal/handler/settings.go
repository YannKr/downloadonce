package handler

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YannKr/downloadonce/internal/auth"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
)

type settingsData struct {
	APIKeys             []model.APIKey
	Webhooks            []model.Webhook
	NewAPIKey           string
	SMTPEnabled         bool
	NotifyOnDownload    bool
	WebhookLastDelivery map[string]*model.WebhookDelivery
	ExhaustedDeliveries int
}

func (h *Handler) SettingsPage(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	keys, _ := db.ListAPIKeys(h.DB, accountID)
	webhooks, _ := db.ListWebhooks(h.DB, accountID)
	account, _ := db.GetAccountByID(h.DB, accountID)

	notifyOn := false
	if account != nil {
		notifyOn = account.NotifyOnDownload
	}

	lastDelivery, _ := db.GetLastDeliveryPerWebhook(h.DB, accountID)
	exhausted, _ := db.CountExhaustedDeliveriesLast24h(h.DB, accountID)

	h.renderAuth(w, r, "settings.html", "Settings", settingsData{
		APIKeys:             keys,
		Webhooks:            webhooks,
		SMTPEnabled:         h.Cfg.SMTPHost != "",
		NotifyOnDownload:    notifyOn,
		WebhookLastDelivery: lastDelivery,
		ExhaustedDeliveries: exhausted,
	})
}

func (h *Handler) APIKeyCreate(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	name := r.FormValue("name")
	if name == "" {
		name = "Unnamed key"
	}

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

	db.InsertAuditLog(h.DB, accountID, "api_key_created", "api_key", apiKey.ID, name, r.RemoteAddr)

	keys, _ := db.ListAPIKeys(h.DB, accountID)
	webhooks, _ := db.ListWebhooks(h.DB, accountID)

	h.render(w, r, "settings.html", PageData{
		Title:         "Settings",
		Authenticated: true,
		IsAdmin:       auth.IsAdmin(r.Context()),
		UserName:      auth.NameFromContext(r.Context()),
		Flash:         "API key created. Copy it now it won't be shown again.",
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
	db.InsertAuditLog(h.DB, accountID, "api_key_deleted", "api_key", id, "", r.RemoteAddr)
	setFlash(w, "API key deleted.")
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

	db.InsertAuditLog(h.DB, accountID, "webhook_created", "webhook", wh.ID, url, r.RemoteAddr)

	setFlash(w, "Webhook created.")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *Handler) WebhookDelete(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	id := chi.URLParam(r, "id")
	db.DeleteWebhook(h.DB, id, accountID)
	db.InsertAuditLog(h.DB, accountID, "webhook_deleted", "webhook", id, "", r.RemoteAddr)
	setFlash(w, "Webhook deleted.")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (h *Handler) NotifyOnDownloadUpdate(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	notify := r.FormValue("notify_on_download") == "1"
	db.UpdateAccountNotifyOnDownload(h.DB, accountID, notify)
	setFlash(w, "Notification preference saved.")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

type deliveriesData struct {
	Webhook    model.Webhook
	Deliveries []model.WebhookDelivery
	Total      int
	Page       int
	PerPage    int
	TotalPages int
	PrevPage   int
	NextPage   int
}

func (h *Handler) WebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	whID := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	wh, err := db.GetWebhookByID(h.DB, whID)
	if err != nil || wh == nil || (wh.AccountID != accountID && !auth.IsAdmin(r.Context())) {
		http.NotFound(w, r)
		return
	}

	page := 1
	if p, _ := strconv.Atoi(r.URL.Query().Get("page")); p > 0 {
		page = p
	}
	perPage := 50
	offset := (page - 1) * perPage

	total, _ := db.CountWebhookDeliveries(h.DB, whID)
	deliveries, err := db.ListWebhookDeliveries(h.DB, whID, perPage, offset)
	if err != nil {
		slog.Error("list webhook deliveries", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}

	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}

	prevPage := 0
	if page > 1 {
		prevPage = page - 1
	}
	nextPage := 0
	if page < totalPages {
		nextPage = page + 1
	}

	h.renderAuth(w, r, "webhook_deliveries.html", "Delivery History", deliveriesData{
		Webhook:    *wh,
		Deliveries: deliveries,
		Total:      total,
		Page:       page,
		PerPage:    perPage,
		TotalPages: totalPages,
		PrevPage:   prevPage,
		NextPage:   nextPage,
	})
}

func (h *Handler) WebhookDeliveryReplay(w http.ResponseWriter, r *http.Request) {
	whID := chi.URLParam(r, "id")
	deliveryID := chi.URLParam(r, "deliveryID")
	accountID := auth.AccountFromContext(r.Context())

	wh, err := db.GetWebhookByID(h.DB, whID)
	if err != nil || wh == nil || (wh.AccountID != accountID && !auth.IsAdmin(r.Context())) {
		http.NotFound(w, r)
		return
	}

	delivery, err := db.GetWebhookDelivery(h.DB, deliveryID)
	if err != nil || delivery == nil || delivery.WebhookID != whID {
		http.NotFound(w, r)
		return
	}

	if delivery.State != "exhausted" && delivery.State != "delivered" {
		http.Error(w, "Only exhausted or delivered deliveries can be replayed", http.StatusBadRequest)
		return
	}

	if err := db.ReplayWebhookDelivery(h.DB, deliveryID); err != nil {
		slog.Error("replay webhook delivery", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}

	db.InsertAuditLog(h.DB, accountID, "webhook_delivery_replayed", "webhook_delivery", deliveryID, wh.URL, r.RemoteAddr)
	setFlash(w, "Delivery re-queued.")
	http.Redirect(w, r, "/settings/webhooks/"+whID+"/deliveries", http.StatusSeeOther)
}
