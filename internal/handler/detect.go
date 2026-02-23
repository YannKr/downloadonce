package handler

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/ypk/downloadonce/internal/auth"
	"github.com/ypk/downloadonce/internal/db"
)

func (h *Handler) DetectForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, "detect.html", PageData{
		Title:         "Detect Watermark",
		Authenticated: true,
	})
}

func (h *Handler) DetectSubmit(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())

	if err := r.ParseMultipartForm(h.Cfg.MaxUploadBytes); err != nil {
		h.render(w, "detect.html", PageData{
			Title: "Detect Watermark", Authenticated: true,
			Error: "Failed to parse upload.",
		})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		h.render(w, "detect.html", PageData{
			Title: "Detect Watermark", Authenticated: true,
			Error: "No file selected.",
		})
		return
	}
	defer file.Close()

	// Validate file extension
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
		".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".webm": true,
	}
	if !allowed[ext] {
		h.render(w, "detect.html", PageData{
			Title: "Detect Watermark", Authenticated: true,
			Error: "Unsupported file type. Please upload an image (JPEG/PNG/WebP) or video (MP4/MKV/AVI/MOV/WebM).",
		})
		return
	}

	jobID := uuid.New().String()

	// Save uploaded file
	detectDir := filepath.Join(h.Cfg.DataDir, "detect", jobID)
	if err := os.MkdirAll(detectDir, 0755); err != nil {
		slog.Error("create detect dir", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}

	inputPath := filepath.Join(detectDir, "input"+ext)
	dst, err := os.Create(inputPath)
	if err != nil {
		slog.Error("create detect file", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		slog.Error("save detect file", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}

	// Enqueue detection job
	if err := db.EnqueueDetectJob(h.DB, jobID, accountID, inputPath, "detect"); err != nil {
		slog.Error("enqueue detect job", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}

	http.Redirect(w, r, "/detect/"+jobID, http.StatusSeeOther)
}

func (h *Handler) DetectResult(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")

	job, err := db.GetJob(h.DB, jobID)
	if err != nil {
		slog.Error("get detect job", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	if job == nil {
		http.Error(w, "Not found", 404)
		return
	}

	h.render(w, "detect_result.html", PageData{
		Title:         "Detection Result",
		Authenticated: true,
		Data:          job,
	})
}
