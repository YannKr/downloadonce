package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YannKr/downloadonce/internal/auth"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
)

type apiRecipient struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Org       string `json:"org"`
	CreatedAt string `json:"created_at"`
}

func recipientToAPI(r *model.Recipient) apiRecipient {
	return apiRecipient{
		ID:        r.ID,
		Name:      r.Name,
		Email:     r.Email,
		Org:       r.Org,
		CreatedAt: r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// APIRecipientCreate — POST /api/v1/recipients
func (h *Handler) APIRecipientCreate(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())

	var body struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Org   string `json:"org"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
		return
	}
	if body.Name == "" || body.Email == "" {
		renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "name and email are required")
		return
	}

	rec, err := db.GetOrCreateRecipientByEmail(h.DB, accountID, body.Name, body.Email, body.Org)
	if err != nil {
		slog.Error("api get/create recipient", "error", err)
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create recipient")
		return
	}

	status := http.StatusCreated
	if rec.ID != "" {
		// Already existed
		status = http.StatusOK
	} else {
		// New recipient — assign ID and insert
		rec.ID = uuid.New().String()
		if err := db.CreateRecipient(h.DB, rec); err != nil {
			slog.Error("api create recipient", "error", err)
			renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create recipient")
			return
		}
		db.InsertAuditLog(h.DB, accountID, "recipient_created", "recipient", rec.ID, rec.Email, r.RemoteAddr)
	}

	renderJSON(w, status, recipientToAPI(rec))
}

// APIRecipientList — GET /api/v1/recipients
func (h *Handler) APIRecipientList(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	isAdmin := auth.IsAdmin(r.Context())

	recipients, err := db.ListRecipients(h.DB)
	if err != nil {
		slog.Error("api list recipients", "error", err)
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list recipients")
		return
	}

	if !isAdmin {
		filtered := recipients[:0]
		for _, rec := range recipients {
			if rec.AccountID == accountID {
				filtered = append(filtered, rec)
			}
		}
		recipients = filtered
	}

	page, perPage := paginate(r)
	total := len(recipients)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	slice := recipients[start:end]

	result := make([]apiRecipient, len(slice))
	for i, rec := range slice {
		result[i] = recipientToAPI(&rec)
	}

	renderJSON(w, http.StatusOK, paginatedResult{
		Data:    result,
		Total:   total,
		Page:    page,
		PerPage: perPage,
	})
}

// APIRecipientDelete — DELETE /api/v1/recipients/{id}
func (h *Handler) APIRecipientDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	rec, err := db.GetRecipient(h.DB, id)
	if err != nil {
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get recipient")
		return
	}
	if rec == nil || (rec.AccountID != accountID && !auth.IsAdmin(r.Context())) {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "recipient not found")
		return
	}

	if err := db.DeleteRecipient(h.DB, id); err != nil {
		slog.Error("api delete recipient", "error", err)
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete recipient")
		return
	}
	db.InsertAuditLog(h.DB, accountID, "recipient_deleted", "recipient", id, "", r.RemoteAddr)

	w.WriteHeader(http.StatusNoContent)
}
