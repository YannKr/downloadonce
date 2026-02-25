package handler

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/YannKr/downloadonce/internal/auth"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
)

func (h *Handler) SetupForm(w http.ResponseWriter, r *http.Request) {
	exists, _ := db.AccountExists(h.DB)
	if exists {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	h.render(w, r, "setup.html", PageData{Title: "Setup"})
}

func (h *Handler) SetupSubmit(w http.ResponseWriter, r *http.Request) {
	exists, _ := db.AccountExists(h.DB)
	if exists {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	if name == "" || email == "" || password == "" {
		h.render(w, r, "setup.html", PageData{Title: "Setup", Error: "All fields are required.",
			Data: map[string]string{"Name": name, "Email": email}})
		return
	}
	if len(password) < 8 {
		h.render(w, r, "setup.html", PageData{Title: "Setup", Error: "Password must be at least 8 characters.",
			Data: map[string]string{"Name": name, "Email": email}})
		return
	}
	if password != confirm {
		h.render(w, r, "setup.html", PageData{Title: "Setup", Error: "Passwords do not match.",
			Data: map[string]string{"Name": name, "Email": email}})
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		h.render(w, r, "setup.html", PageData{Title: "Setup", Error: "Internal error."})
		return
	}

	account := &model.Account{
		ID:           uuid.New().String(),
		Email:        email,
		Name:         name,
		PasswordHash: hash,
		Role:         "admin",
		Enabled:      true,
	}
	if err := db.CreateAccount(h.DB, account); err != nil {
		h.render(w, r, "setup.html", PageData{Title: "Setup", Error: "Failed to create account."})
		return
	}

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handler) LoginForm(w http.ResponseWriter, r *http.Request) {
	exists, _ := db.AccountExists(h.DB)
	if !exists {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	h.render(w, r, "login.html", PageData{Title: "Login", Data: map[string]interface{}{
		"AllowRegistration": h.Cfg.AllowRegistration,
	}})
}

func (h *Handler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	account, err := db.GetAccountByEmail(h.DB, email)
	if err != nil || account == nil || !auth.CheckPassword(account.PasswordHash, password) {
		h.render(w, r, "login.html", PageData{Title: "Login", Error: "Invalid email or password.",
			Data: map[string]interface{}{"Email": email, "AllowRegistration": h.Cfg.AllowRegistration}})
		return
	}

	if !account.Enabled {
		h.render(w, r, "login.html", PageData{Title: "Login", Error: "Your account has been disabled.",
			Data: map[string]interface{}{"Email": email, "AllowRegistration": h.Cfg.AllowRegistration}})
		return
	}

	sessionID, err := auth.GenerateToken(32)
	if err != nil {
		h.render(w, r, "login.html", PageData{Title: "Login", Error: "Internal error.",
			Data: map[string]interface{}{"AllowRegistration": h.Cfg.AllowRegistration}})
		return
	}

	session := &model.Session{
		ID:        sessionID,
		AccountID: account.ID,
		ExpiresAt: time.Now().Add(auth.SessionMaxAge),
	}
	if err := db.CreateSession(h.DB, session); err != nil {
		h.render(w, r, "login.html", PageData{Title: "Login", Error: "Internal error.",
			Data: map[string]interface{}{"AllowRegistration": h.Cfg.AllowRegistration}})
		return
	}

	auth.SetSessionCookie(w, sessionID, h.Cfg.SessionSecret)
	db.InsertAuditLog(h.DB, account.ID, "login", "account", account.ID, "", r.RemoteAddr)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := auth.GetSessionID(r, h.Cfg.SessionSecret)
	if ok {
		db.DeleteSession(h.DB, sessionID)
	}
	db.InsertAuditLog(h.DB, auth.AccountFromContext(r.Context()), "logout", "", "", "", r.RemoteAddr)
	auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handler) RegisterForm(w http.ResponseWriter, r *http.Request) {
	if !h.Cfg.AllowRegistration {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	h.render(w, r, "register.html", PageData{Title: "Register"})
}

func (h *Handler) RegisterSubmit(w http.ResponseWriter, r *http.Request) {
	if !h.Cfg.AllowRegistration {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	if name == "" || email == "" || password == "" {
		h.render(w, r, "register.html", PageData{Title: "Register", Error: "All fields are required.",
			Data: map[string]string{"Name": name, "Email": email}})
		return
	}
	if len(password) < 8 {
		h.render(w, r, "register.html", PageData{Title: "Register", Error: "Password must be at least 8 characters.",
			Data: map[string]string{"Name": name, "Email": email}})
		return
	}
	if password != confirm {
		h.render(w, r, "register.html", PageData{Title: "Register", Error: "Passwords do not match.",
			Data: map[string]string{"Name": name, "Email": email}})
		return
	}

	existing, _ := db.GetAccountByEmail(h.DB, email)
	if existing != nil {
		h.render(w, r, "register.html", PageData{Title: "Register", Error: "An account with this email already exists.",
			Data: map[string]string{"Name": name, "Email": email}})
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		h.render(w, r, "register.html", PageData{Title: "Register", Error: "Internal error."})
		return
	}

	account := &model.Account{
		ID:           uuid.New().String(),
		Email:        email,
		Name:         name,
		PasswordHash: hash,
		Role:         "member",
		Enabled:      true,
	}
	if err := db.CreateAccount(h.DB, account); err != nil {
		h.render(w, r, "register.html", PageData{Title: "Register", Error: "Failed to create account."})
		return
	}

	db.InsertAuditLog(h.DB, account.ID, "user_created", "account", account.ID, "Self-registered", r.RemoteAddr)
	http.Redirect(w, r, "/login?registered=1", http.StatusSeeOther)
}

func (h *Handler) ForgotPasswordForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, "forgot_password.html", PageData{Title: "Forgot Password"})
}

func (h *Handler) ForgotPasswordSubmit(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))

	// Always show the same message to prevent user enumeration
	successMsg := "If an account with that email exists, a password reset link has been sent."

	if email == "" {
		h.render(w, r, "forgot_password.html", PageData{Title: "Forgot Password", Error: "Email is required."})
		return
	}

	if !h.Mailer.Enabled() {
		h.render(w, r, "forgot_password.html", PageData{Title: "Forgot Password",
			Error: "Email is not configured. Contact your administrator."})
		return
	}

	account, err := db.GetAccountByEmail(h.DB, email)
	if err != nil || account == nil {
		// Don't reveal whether the account exists
		h.render(w, r, "forgot_password.html", PageData{Title: "Forgot Password", Flash: successMsg})
		return
	}

	// Generate token
	token, err := auth.GenerateToken(32)
	if err != nil {
		slog.Error("generate reset token", "error", err)
		h.render(w, r, "forgot_password.html", PageData{Title: "Forgot Password", Flash: successMsg})
		return
	}

	tokenHash := db.HashToken(token)
	expiresAt := time.Now().Add(1 * time.Hour)

	if err := db.CreatePasswordReset(h.DB, uuid.New().String(), account.ID, tokenHash, expiresAt); err != nil {
		slog.Error("create password reset", "error", err)
		h.render(w, r, "forgot_password.html", PageData{Title: "Forgot Password", Flash: successMsg})
		return
	}

	db.InsertAuditLog(h.DB, account.ID, "password_reset_requested", "account", account.ID, "", r.RemoteAddr)

	resetURL := h.Cfg.BaseURL + "/reset-password?token=" + token
	if err := h.Mailer.SendPasswordReset(account.Email, account.Name, resetURL); err != nil {
		slog.Error("send password reset email", "error", err)
	}

	h.render(w, r, "forgot_password.html", PageData{Title: "Forgot Password", Flash: successMsg})
}

func (h *Handler) ResetPasswordForm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	tokenHash := db.HashToken(token)
	pr, err := db.GetPasswordResetByTokenHash(h.DB, tokenHash)
	if err != nil || pr == nil || pr.Used || time.Now().After(pr.ExpiresAt) {
		h.render(w, r, "forgot_password.html", PageData{Title: "Forgot Password",
			Error: "This reset link is invalid or has expired. Please request a new one."})
		return
	}

	h.render(w, r, "reset_password.html", PageData{
		Title: "Reset Password",
		Data:  map[string]string{"Token": token},
	})
}

func (h *Handler) ResetPasswordSubmit(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	if token == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	tokenHash := db.HashToken(token)
	pr, err := db.GetPasswordResetByTokenHash(h.DB, tokenHash)
	if err != nil || pr == nil || pr.Used || time.Now().After(pr.ExpiresAt) {
		h.render(w, r, "forgot_password.html", PageData{Title: "Forgot Password",
			Error: "This reset link is invalid or has expired. Please request a new one."})
		return
	}

	if len(password) < 8 {
		h.render(w, r, "reset_password.html", PageData{Title: "Reset Password",
			Error: "Password must be at least 8 characters.",
			Data:  map[string]string{"Token": token}})
		return
	}
	if password != confirm {
		h.render(w, r, "reset_password.html", PageData{Title: "Reset Password",
			Error: "Passwords do not match.",
			Data:  map[string]string{"Token": token}})
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		h.render(w, r, "reset_password.html", PageData{Title: "Reset Password",
			Error: "Internal error.",
			Data:  map[string]string{"Token": token}})
		return
	}

	if err := db.UpdateAccountPassword(h.DB, pr.AccountID, hash); err != nil {
		slog.Error("update password", "error", err)
		h.render(w, r, "reset_password.html", PageData{Title: "Reset Password",
			Error: "Internal error.",
			Data:  map[string]string{"Token": token}})
		return
	}

	db.InsertAuditLog(h.DB, pr.AccountID, "password_changed", "account", pr.AccountID, "Via password reset", r.RemoteAddr)

	db.MarkPasswordResetUsed(h.DB, pr.ID)
	// Invalidate all sessions for this user
	db.DeleteSessionsByAccount(h.DB, pr.AccountID)

	http.Redirect(w, r, "/login?reset=1", http.StatusSeeOther)
}
