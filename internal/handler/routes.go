package handler

import (
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/gorilla/csrf"
)

func (h *Handler) Routes(staticFS fs.FS, authRL *RateLimiter) chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(h.RequireSetup)

	// CSRF protection — exempt API key (Bearer) requests
	csrfProtect := csrf.Protect(
		[]byte(h.Cfg.SessionSecret),
		csrf.Secure(strings.HasPrefix(h.Cfg.BaseURL, "https")),
		csrf.Path("/"),
		csrf.SameSite(csrf.SameSiteLaxMode),
	)
	r.Use(func(next http.Handler) http.Handler {
		protected := csrfProtect(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer do_") {
				next.ServeHTTP(w, r)
				return
			}
			protected.ServeHTTP(w, r)
		})
	})

	// Static files
	r.Handle("/static/*", http.StripPrefix("/static/",
		http.FileServer(http.FS(staticFS))))

	// Public routes (rate-limited)
	r.Group(func(r chi.Router) {
		r.Use(authRL.Middleware)
		r.Get("/login", h.LoginForm)
		r.Post("/login", h.LoginSubmit)
		r.Get("/setup", h.SetupForm)
		r.Post("/setup", h.SetupSubmit)
		r.Get("/register", h.RegisterForm)
		r.Post("/register", h.RegisterSubmit)
		r.Get("/forgot-password", h.ForgotPasswordForm)
		r.Post("/forgot-password", h.ForgotPasswordSubmit)
		r.Get("/reset-password", h.ResetPasswordForm)
		r.Post("/reset-password", h.ResetPasswordSubmit)
	})

	// Public download routes
	r.Get("/d/{token}", h.DownloadPage)
	r.Get("/d/{token}/file", h.DownloadFile)
	r.Get("/d/{token}/events", h.TokenSSE)

	// Authenticated routes
	r.Group(func(r chi.Router) {
		r.Use(h.RequireAuth)

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		})
		r.Post("/logout", h.Logout)

		r.Get("/dashboard", h.Dashboard)

		r.Get("/assets", h.AssetList)
		r.Get("/assets/upload", h.AssetUploadForm)
		r.Post("/assets/upload", h.AssetUploadSubmit)
		r.Get("/assets/{id}/thumb", h.AssetThumbnail)
		r.Post("/assets/{id}/delete", h.AssetDelete)

		r.Get("/recipients", h.RecipientList)
		r.Post("/recipients", h.RecipientCreate)
		r.Post("/recipients/import", h.RecipientImport)
		r.Post("/recipients/{id}/delete", h.RecipientDelete)

		// Recipient groups
		r.Get("/recipients/groups", h.GroupList)
		r.Post("/recipients/groups", h.GroupCreate)
		r.Get("/recipients/groups/{id}", h.GroupDetail)
		r.Post("/recipients/groups/{id}/edit", h.GroupEdit)
		r.Post("/recipients/groups/{id}/delete", h.GroupDelete)
		r.Post("/recipients/groups/{id}/add-members", h.GroupAddMembers)
		r.Post("/recipients/groups/{id}/members/{recipientID}/remove", h.GroupRemoveMember)
		r.Post("/recipients/groups/{id}/import", h.GroupImport)

		r.Get("/campaigns", h.CampaignList)
		r.Get("/campaigns/new", h.CampaignNewForm)
		r.Post("/campaigns/new", h.CampaignCreate)
		r.Get("/campaigns/{id}", h.CampaignDetail)
		r.Post("/campaigns/{id}/publish", h.CampaignPublish)
		r.Post("/campaigns/{id}/tokens/{tokenID}/revoke", h.TokenRevoke)
		r.Get("/campaigns/{id}/events", h.CampaignSSE)

		r.Get("/detect", h.DetectForm)
		r.Post("/detect", h.DetectSubmit)
		r.Get("/detect/{id}", h.DetectResult)

		r.Get("/analytics", h.Analytics)
		r.Get("/analytics/export", h.AnalyticsExport)

		r.Get("/settings", h.SettingsPage)
		r.Post("/settings/notify", h.NotifyOnDownloadUpdate)
		r.Post("/settings/apikeys", h.APIKeyCreate)
		r.Post("/settings/apikeys/{id}/delete", h.APIKeyDelete)
		r.Post("/settings/webhooks", h.WebhookCreate)
		r.Post("/settings/webhooks/{id}/delete", h.WebhookDelete)

		// Chunked upload API
		r.Post("/upload/chunks/init", h.UploadInit)
		r.Put("/upload/chunks/{sessionID}/{chunkIndex}", h.UploadChunk)
		r.Get("/upload/chunks/{sessionID}/status", h.UploadStatus)
		r.Post("/upload/chunks/{sessionID}/complete", h.UploadComplete)
		r.Delete("/upload/chunks/{sessionID}", h.UploadCancel)

		// Admin routes
		r.Route("/admin", func(r chi.Router) {
			r.Use(h.RequireAdmin)
			r.Get("/users", h.AdminUsers)
			r.Post("/users", h.AdminCreateUser)
			r.Post("/users/{id}/toggle", h.AdminToggleUser)
			r.Post("/users/{id}/delete", h.AdminDeleteUser)
			r.Post("/users/{id}/promote", h.AdminPromoteUser)
			r.Get("/campaigns", h.AdminCampaigns)
			r.Get("/audit", h.AdminAudit)
			r.Get("/storage", h.AdminStorage)
			r.Get("/storage.json", h.AdminStorageJSON)
		})
	})

	return r
}
