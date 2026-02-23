package handler

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ypk/downloadonce/internal/auth"
	"github.com/ypk/downloadonce/internal/db"
	"github.com/ypk/downloadonce/internal/model"
)

type recipientPageData struct {
	Recipients []model.Recipient
	FormName   string
	FormEmail  string
	FormOrg    string
}

func (h *Handler) RecipientList(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	recipients, err := db.ListRecipients(h.DB, accountID)
	if err != nil {
		http.Error(w, "Internal error", 500)
		return
	}
	h.render(w, "recipients.html", PageData{
		Title:         "Recipients",
		Authenticated: true,
		Data:          recipientPageData{Recipients: recipients},
	})
}

func (h *Handler) RecipientCreate(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())

	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	org := strings.TrimSpace(r.FormValue("org"))

	if name == "" || email == "" {
		recipients, _ := db.ListRecipients(h.DB, accountID)
		h.render(w, "recipients.html", PageData{
			Title: "Recipients", Authenticated: true,
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
			recipients, _ := db.ListRecipients(h.DB, accountID)
			h.render(w, "recipients.html", PageData{
				Title: "Recipients", Authenticated: true,
				Error: "A recipient with this email already exists.",
				Data:  recipientPageData{Recipients: recipients, FormName: name, FormEmail: email, FormOrg: org},
			})
			return
		}
		http.Error(w, "Internal error", 500)
		return
	}

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

	recipients, _ := db.ListRecipients(h.DB, accountID)
	flash := ""
	if created > 0 || skipped > 0 {
		flash = strings.TrimSpace(strings.Join([]string{
			func() string {
				if created > 0 {
					return strings.Repeat("", 0) + string(rune(created+'0')) + " created"
				}
				return ""
			}(),
		}, ", "))
		// Simpler approach
		flash = ""
		if created > 0 {
			flash += strings.Replace("N created", "N", strings.TrimSpace(itoa(created)), 1)
		}
		if skipped > 0 {
			if flash != "" {
				flash += ", "
			}
			flash += strings.Replace("N skipped", "N", strings.TrimSpace(itoa(skipped)), 1)
		}
	}

	h.render(w, "recipients.html", PageData{
		Title: "Recipients", Authenticated: true,
		Flash: flash,
		Data:  recipientPageData{Recipients: recipients},
	})
}

func (h *Handler) RecipientDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	db.DeleteRecipient(h.DB, id)
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
