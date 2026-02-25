package handler

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YannKr/downloadonce/internal/auth"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
)

type recipientPageData struct {
	Recipients []model.RecipientWithGroups
	FormName   string
	FormEmail  string
	FormOrg    string
}

func (h *Handler) RecipientList(w http.ResponseWriter, r *http.Request) {
	recipients, err := db.ListRecipientsWithGroups(h.DB)
	if err != nil {
		http.Error(w, "Internal error", 500)
		return
	}
	h.renderAuth(w, r, "recipients.html", "Recipients", recipientPageData{Recipients: recipients})
}

func (h *Handler) RecipientCreate(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())

	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	org := strings.TrimSpace(r.FormValue("org"))

	if name == "" || email == "" {
		recipients, _ := db.ListRecipientsWithGroups(h.DB)
		h.render(w, r, "recipients.html", PageData{
			Title: "Recipients", Authenticated: true,
			IsAdmin: auth.IsAdmin(r.Context()), UserName: auth.NameFromContext(r.Context()),
			Error: "Name and email are required.",
			Data:  recipientPageData{Recipients: recipients, FormName: name, FormEmail: email, FormOrg: org},
		})
		return
	}

	recipient := &model.Recipient{
		ID:        uuid.New().String(),
		AccountID: accountID,
		Name:      name,
		Email:     email,
		Org:       org,
	}
	if err := db.CreateRecipient(h.DB, recipient); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			recipients, _ := db.ListRecipientsWithGroups(h.DB)
			h.render(w, r, "recipients.html", PageData{
				Title: "Recipients", Authenticated: true,
				IsAdmin: auth.IsAdmin(r.Context()), UserName: auth.NameFromContext(r.Context()),
				Error: "A recipient with this email already exists.",
				Data:  recipientPageData{Recipients: recipients, FormName: name, FormEmail: email, FormOrg: org},
			})
			return
		}
		http.Error(w, "Internal error", 500)
		return
	}

	db.InsertAuditLog(h.DB, auth.AccountFromContext(r.Context()), "recipient_created", "recipient", recipient.ID, recipient.Email, r.RemoteAddr)

	setFlash(w, "Recipient created.")
	http.Redirect(w, r, "/recipients", http.StatusSeeOther)
}

func (h *Handler) RecipientImport(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	bulk := r.FormValue("bulk")

	var created, skipped int
	lines := strings.Split(bulk, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ",", 3)
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		email := strings.TrimSpace(parts[1])
		org := ""
		if len(parts) == 3 {
			org = strings.TrimSpace(parts[2])
		}
		if name == "" || email == "" {
			continue
		}

		existing, _ := db.GetOrCreateRecipientByEmail(h.DB, accountID, name, email, org)
		if existing.ID != "" {
			skipped++
			continue
		}

		existing.ID = uuid.New().String()
		if err := db.CreateRecipient(h.DB, existing); err != nil {
			skipped++
			continue
		}
		created++
	}

	recipients, _ := db.ListRecipientsWithGroups(h.DB)
	flash := ""
	if created > 0 {
		flash += strings.Replace("N created", "N", strings.TrimSpace(itoa(created)), 1)
	}
	if skipped > 0 {
		if flash != "" {
			flash += ", "
		}
		flash += strings.Replace("N skipped", "N", strings.TrimSpace(itoa(skipped)), 1)
	}

	h.render(w, r, "recipients.html", PageData{
		Title: "Recipients", Authenticated: true,
		IsAdmin: auth.IsAdmin(r.Context()), UserName: auth.NameFromContext(r.Context()),
		Flash: flash,
		Data:  recipientPageData{Recipients: recipients},
	})
}

func (h *Handler) RecipientDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	recipient, err := db.GetRecipient(h.DB, id)
	if err != nil || recipient == nil || (recipient.AccountID != accountID && !auth.IsAdmin(r.Context())) {
		http.NotFound(w, r)
		return
	}

	db.DeleteRecipient(h.DB, id)
	db.InsertAuditLog(h.DB, auth.AccountFromContext(r.Context()), "recipient_deleted", "recipient", id, "", r.RemoteAddr)
	setFlash(w, "Recipient deleted.")
	http.Redirect(w, r, "/recipients", http.StatusSeeOther)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
