package handler

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ypk/downloadonce/internal/auth"
	"github.com/ypk/downloadonce/internal/db"
	"github.com/ypk/downloadonce/internal/model"
)

type adminUsersData struct {
	Users             []model.Account
	AllowRegistration bool
}

func (h *Handler) AdminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := db.ListAccounts(h.DB)
	if err != nil {
		slog.Error("list accounts", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	h.renderAuth(w, r, "admin_users.html", "Users", adminUsersData{
		Users:             users,
		AllowRegistration: h.Cfg.AllowRegistration,
	})
}

func (h *Handler) AdminCreateUser(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	role := r.FormValue("role")

	if name == "" || email == "" || password == "" {
		users, _ := db.ListAccounts(h.DB)
		h.renderAuth(w, r, "admin_users.html", "Users", adminUsersData{Users: users})
		return
	}
	if role != "admin" && role != "member" {
		role = "member"
	}

	existing, _ := db.GetAccountByEmail(h.DB, email)
	if existing != nil {
		users, _ := db.ListAccounts(h.DB)
		h.render(w, "admin_users.html", PageData{
			Title: "Users", Authenticated: true, IsAdmin: true,
			Error: "An account with this email already exists.",
			Data:  adminUsersData{Users: users},
		})
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "Internal error", 500)
		return
	}

	account := &model.Account{
		ID:           uuid.New().String(),
		Email:        email,
		Name:         name,
		PasswordHash: hash,
		Role:         role,
		Enabled:      true,
	}
	if err := db.CreateAccount(h.DB, account); err != nil {
		http.Error(w, "Internal error", 500)
		return
	}

	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (h *Handler) AdminToggleUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	if id == accountID {
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	account, err := db.GetAccountByID(h.DB, id)
	if err != nil || account == nil {
		http.NotFound(w, r)
		return
	}

	db.UpdateAccountEnabled(h.DB, id, !account.Enabled)
	if account.Enabled {
		db.DeleteSessionsByAccount(h.DB, id)
	}

	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (h *Handler) AdminDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	if id == accountID {
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	db.DeleteSessionsByAccount(h.DB, id)
	db.DeleteAccount(h.DB, id)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (h *Handler) AdminPromoteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	if id == accountID {
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	account, err := db.GetAccountByID(h.DB, id)
	if err != nil || account == nil {
		http.NotFound(w, r)
		return
	}

	newRole := "admin"
	if account.Role == "admin" {
		newRole = "member"
	}
	db.UpdateAccountRole(h.DB, id, newRole)

	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (h *Handler) AdminCampaigns(w http.ResponseWriter, r *http.Request) {
	campaigns, err := db.ListCampaigns(h.DB, "", true)
	if err != nil {
		slog.Error("list all campaigns", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	h.renderAuth(w, r, "admin_campaigns.html", "All Campaigns", campaigns)
}
