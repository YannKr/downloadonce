package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YannKr/downloadonce/internal/auth"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
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
		h.render(w, r, "admin_users.html", PageData{
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

	db.InsertAuditLog(h.DB, auth.AccountFromContext(r.Context()), "user_created", "account", account.ID, fmt.Sprintf("Created user %s (%s)", name, email), r.RemoteAddr)
	setFlash(w, "User created successfully.")
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

	action := "user_enabled"
	if account.Enabled {
		action = "user_disabled"
	}
	db.InsertAuditLog(h.DB, accountID, action, "account", id, fmt.Sprintf("Toggled user %s", account.Email), r.RemoteAddr)
	setFlash(w, "User status updated.")
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
	db.InsertAuditLog(h.DB, accountID, "user_deleted", "account", id, "", r.RemoteAddr)
	setFlash(w, "User deleted.")
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
	db.InsertAuditLog(h.DB, accountID, "user_promoted", "account", id, fmt.Sprintf("Role changed to %s for %s", newRole, account.Email), r.RemoteAddr)
	setFlash(w, "User role updated.")
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (h *Handler) AdminCampaigns(w http.ResponseWriter, r *http.Request) {
	campaigns, err := db.ListCampaigns(h.DB, "", true, false)
	if err != nil {
		slog.Error("list all campaigns", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	h.renderAuth(w, r, "admin_campaigns.html", "All Campaigns", campaigns)
}

type auditPageData struct {
	Logs         []db.AuditLog
	FilterAction string
	Actions      []string
	Pagination   *PaginationData
}

type PaginationData struct {
	Page       int
	TotalPages int
	HasPrev    bool
	HasNext    bool
	PrevPage   int
	NextPage   int
}

func (h *Handler) AdminAudit(w http.ResponseWriter, r *http.Request) {
	filterAction := r.URL.Query().Get("action")
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}

	perPage := 50
	total, _ := db.CountAuditLogs(h.DB, filterAction)
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * perPage

	logs, err := db.ListAuditLogs(h.DB, perPage, offset, filterAction)
	if err != nil {
		slog.Error("list audit logs", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}

	actions := []string{
		"login", "logout", "user_created", "user_deleted", "user_promoted",
		"user_enabled", "user_disabled", "campaign_created", "campaign_published",
		"token_revoked", "asset_deleted", "recipient_deleted", "recipient_created",
		"api_key_created", "api_key_deleted", "webhook_created", "webhook_deleted",
		"password_reset_requested", "password_changed",
	}

	var pagination *PaginationData
	if total > perPage {
		pagination = &PaginationData{
			Page:       page,
			TotalPages: totalPages,
			HasPrev:    page > 1,
			HasNext:    page < totalPages,
			PrevPage:   page - 1,
			NextPage:   page + 1,
		}
	}

	h.renderAuth(w, r, "admin_audit.html", "Audit Log", auditPageData{
		Logs:         logs,
		FilterAction: filterAction,
		Actions:      actions,
		Pagination:   pagination,
	})
}
