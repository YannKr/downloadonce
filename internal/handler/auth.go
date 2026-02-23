package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/ypk/downloadonce/internal/auth"
	"github.com/ypk/downloadonce/internal/db"
	"github.com/ypk/downloadonce/internal/model"
)

func (h *Handler) SetupForm(w http.ResponseWriter, r *http.Request) {
	exists, _ := db.AccountExists(h.DB)
	if exists {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	h.render(w, "setup.html", PageData{Title: "Setup"})
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
		h.render(w, "setup.html", PageData{Title: "Setup", Error: "All fields are required.",
			Data: map[string]string{"Name": name, "Email": email}})
		return
	}
	if len(password) < 8 {
		h.render(w, "setup.html", PageData{Title: "Setup", Error: "Password must be at least 8 characters.",
			Data: map[string]string{"Name": name, "Email": email}})
		return
	}
	if password != confirm {
		h.render(w, "setup.html", PageData{Title: "Setup", Error: "Passwords do not match.",
			Data: map[string]string{"Name": name, "Email": email}})
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		h.render(w, "setup.html", PageData{Title: "Setup", Error: "Internal error."})
		return
	}

	account := &model.Account{
		ID:           uuid.New().String(),
		Email:        email,
		Name:         name,
		PasswordHash: hash,
	}
	if err := db.CreateAccount(h.DB, account); err != nil {
		h.render(w, "setup.html", PageData{Title: "Setup", Error: "Failed to create account."})
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
	h.render(w, "login.html", PageData{Title: "Login"})
}

func (h *Handler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	account, err := db.GetAccountByEmail(h.DB, email)
	if err != nil || account == nil || !auth.CheckPassword(account.PasswordHash, password) {
		h.render(w, "login.html", PageData{Title: "Login", Error: "Invalid email or password.",
			Data: map[string]string{"Email": email}})
		return
	}

	sessionID, err := auth.GenerateToken(32)
	if err != nil {
		h.render(w, "login.html", PageData{Title: "Login", Error: "Internal error."})
		return
	}

	session := &model.Session{
		ID:        sessionID,
		AccountID: account.ID,
		ExpiresAt: time.Now().Add(auth.SessionMaxAge),
	}
	if err := db.CreateSession(h.DB, session); err != nil {
		h.render(w, "login.html", PageData{Title: "Login", Error: "Internal error."})
		return
	}

	auth.SetSessionCookie(w, sessionID, h.Cfg.SessionSecret)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := auth.GetSessionID(r, h.Cfg.SessionSecret)
	if ok {
		db.DeleteSession(h.DB, sessionID)
	}
	auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
