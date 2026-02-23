package handler

import (
	"database/sql"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/ypk/downloadonce/internal/auth"
	"github.com/ypk/downloadonce/internal/config"
	"github.com/ypk/downloadonce/internal/email"
	"github.com/ypk/downloadonce/internal/sse"
	"github.com/ypk/downloadonce/internal/webhook"
)

type Handler struct {
	DB        *sql.DB
	Cfg       *config.Config
	Mailer    *email.Mailer
	Webhook   *webhook.Dispatcher
	SSE       *sse.Hub
	templates map[string]*template.Template
}

func New(database *sql.DB, cfg *config.Config, templateFS fs.FS, mailer *email.Mailer, webhookDispatcher *webhook.Dispatcher, sseHub *sse.Hub) *Handler {
	funcMap := template.FuncMap{
		"downloadURL": func(tokenID string) string {
			return cfg.BaseURL + "/d/" + tokenID
		},
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format("2006-01-02 15:04 UTC")
		},
		"formatTimePtr": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.Format("2006-01-02 15:04 UTC")
		},
		"formatBytes": func(b int64) string {
			switch {
			case b >= 1<<30:
				return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
			case b >= 1<<20:
				return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
			case b >= 1<<10:
				return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
			default:
				return fmt.Sprintf("%d B", b)
			}
		},
		"formatDuration": func(s *float64) string {
			if s == nil {
				return ""
			}
			d := time.Duration(*s * float64(time.Second))
			h := int(d.Hours())
			m := int(d.Minutes()) % 60
			sec := int(d.Seconds()) % 60
			if h > 0 {
				return fmt.Sprintf("%dh%02dm%02ds", h, m, sec)
			}
			return fmt.Sprintf("%dm%02ds", m, sec)
		},
		"shortenID": func(id string) string {
			if len(id) > 8 {
				return id[:8]
			}
			return id
		},
		"pct": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return (a * 100) / b
		},
		"derefInt": func(p *int) int {
			if p == nil {
				return 0
			}
			return *p
		},
		"derefInt64": func(p *int64) int64 {
			if p == nil {
				return 0
			}
			return *p
		},
		"isNil": func(v interface{}) bool {
			return v == nil
		},
		"stateBadge": func(state string) template.HTML {
			class := "badge"
			switch state {
			case "DRAFT":
				class += " badge-gray"
			case "PROCESSING":
				class += " badge-yellow"
			case "READY", "ACTIVE":
				class += " badge-green"
			case "EXPIRED", "CONSUMED", "FAILED":
				class += " badge-red"
			case "PENDING":
				class += " badge-blue"
			}
			return template.HTML(fmt.Sprintf(`<span class="%s">%s</span>`, class, state))
		},
	}

	// Parse layout template as the base
	layoutTmpl := template.Must(
		template.New("layout.html").Funcs(funcMap).ParseFS(templateFS, "layout.html"),
	)

	// Build per-page template sets: clone layout + parse page
	templates := make(map[string]*template.Template)
	entries, err := fs.ReadDir(templateFS, ".")
	if err != nil {
		panic("read template dir: " + err.Error())
	}
	for _, e := range entries {
		name := e.Name()
		if name == "layout.html" || e.IsDir() {
			continue
		}
		t := template.Must(template.Must(layoutTmpl.Clone()).ParseFS(templateFS, name))
		templates[name] = t
	}

	return &Handler{
		DB:        database,
		Cfg:       cfg,
		Mailer:    mailer,
		Webhook:   webhookDispatcher,
		SSE:       sseHub,
		templates: templates,
	}
}

type PageData struct {
	Title         string
	Authenticated bool
	IsAdmin       bool
	UserName      string
	Flash         string
	Error         string
	Data          interface{}
}

func (h *Handler) render(w http.ResponseWriter, name string, data PageData) {
	t, ok := h.templates[name]
	if !ok {
		slog.Error("template not found", "name", name)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "layout.html", data); err != nil {
		slog.Error("render template", "name", name, "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

func (h *Handler) renderAuth(w http.ResponseWriter, r *http.Request, name, title string, data interface{}) {
	h.render(w, name, PageData{
		Title:         title,
		Authenticated: true,
		IsAdmin:       auth.IsAdmin(r.Context()),
		UserName:      auth.NameFromContext(r.Context()),
		Data:          data,
	})
}
