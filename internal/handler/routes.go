package handler

import (
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func (h *Handler) Routes(staticFS fs.FS) chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RealIP)
	r.Use(h.RequireSetup)

	// Static files
	r.Handle("/static/*", http.StripPrefix("/static/",
		http.FileServer(http.FS(staticFS))))

	// Public routes
	r.Get("/login", h.LoginForm)
	r.Post("/login", h.LoginSubmit)
	r.Get("/setup", h.SetupForm)
	r.Post("/setup", h.SetupSubmit)
	r.Get("/register", h.RegisterForm)
	r.Post("/register", h.RegisterSubmit)

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

		r.Get("/settings", h.SettingsPage)
		r.Post("/settings/apikeys", h.APIKeyCreate)
		r.Post("/settings/apikeys/{id}/delete", h.APIKeyDelete)
		r.Post("/settings/webhooks", h.WebhookCreate)
		r.Post("/settings/webhooks/{id}/delete", h.WebhookDelete)

		// Admin routes
		r.Route("/admin", func(r chi.Router) {
			r.Use(h.RequireAdmin)
			r.Get("/users", h.AdminUsers)
			r.Post("/users", h.AdminCreateUser)
			r.Post("/users/{id}/toggle", h.AdminToggleUser)
			r.Post("/users/{id}/delete", h.AdminDeleteUser)
			r.Post("/users/{id}/promote", h.AdminPromoteUser)
			r.Get("/campaigns", h.AdminCampaigns)
		})
	})

	return r
}
