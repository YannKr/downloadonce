package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YannKr/downloadonce/internal/auth"
	"github.com/YannKr/downloadonce/internal/db"
)

type apiDetectResult struct {
	JobID       string         `json:"job_id"`
	State       string         `json:"state"`
	Progress    int            `json:"progress"`
	CreatedAt   string         `json:"created_at"`
	StartedAt   *string        `json:"started_at"`
	CompletedAt *string        `json:"completed_at"`
	Result      *detectFinding `json:"result"`
}

type detectFinding struct {
	MatchFound     bool    `json:"match_found"`
	TokenID        *string `json:"token_id"`
	CampaignID     *string `json:"campaign_id"`
	RecipientID    *string `json:"recipient_id"`
	RecipientName  *string `json:"recipient_name"`
	RecipientEmail *string `json:"recipient_email"`
	Confidence     *string `json:"confidence"`
}

// APIDetectSubmit - POST /api/v1/detect
func (h *Handler) APIDetectSubmit(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())

	if err := r.ParseMultipartForm(2 << 30); err != nil {
		renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "failed to parse multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "missing file field")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowed := map[string]bool{
		".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
		".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".webm": true,
	}
	if !allowed[ext] {
		renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "unsupported file type")
		return
	}

	jobID := uuid.New().String()

	detectDir := filepath.Join(h.Cfg.DataDir, "detect", jobID)
	if err := os.MkdirAll(detectDir, 0755); err != nil {
		slog.Error("create detect dir", "error", err)
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create job directory")
		return
	}

	inputPath := filepath.Join(detectDir, "input"+ext)
	dst, err := os.Create(inputPath)
	if err != nil {
		slog.Error("create detect file", "error", err)
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to create input file")
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		slog.Error("save detect file", "error", err)
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to save file")
		return
	}

	if err := db.EnqueueDetectJob(h.DB, jobID, accountID, inputPath, "detect"); err != nil {
		slog.Error("enqueue detect job", "error", err)
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to enqueue job")
		return
	}

	job, _ := db.GetJob(h.DB, jobID)
	result := apiDetectResult{
		JobID:     jobID,
		State:     "PENDING",
		Progress:  0,
		CreatedAt: job.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
	renderJSON(w, http.StatusAccepted, result)
}

// APIDetectGet - GET /api/v1/detect/{jobID}
func (h *Handler) APIDetectGet(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobID")
	accountID := auth.AccountFromContext(r.Context())
	isAdmin := auth.IsAdmin(r.Context())

	job, err := db.GetJob(h.DB, jobID)
	if err != nil {
		slog.Error("api get detect job", "error", err)
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get job")
		return
	}
	if job == nil {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "job not found")
		return
	}

	if job.CampaignID != accountID && !isAdmin {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "job not found")
		return
	}

	result := apiDetectResult{
		JobID:     job.ID,
		State:     job.State,
		Progress:  job.Progress,
		CreatedAt: job.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}

	if job.StartedAt != nil {
		s := job.StartedAt.UTC().Format("2006-01-02T15:04:05Z")
		result.StartedAt = &s
	}
	if job.CompletedAt != nil {
		s := job.CompletedAt.UTC().Format("2006-01-02T15:04:05Z")
		result.CompletedAt = &s
	}

	if job.State == "COMPLETED" && job.ResultData != "" {
		var raw struct {
			Found          bool   `json:"found"`
			TokenID        string `json:"token_id"`
			CampaignID     string `json:"campaign_id"`
			RecipientName  string `json:"recipient_name"`
			RecipientEmail string `json:"recipient_email"`
		}
		if err := json.Unmarshal([]byte(job.ResultData), &raw); err == nil {
			finding := &detectFinding{
				MatchFound: raw.Found,
			}
			if raw.TokenID != "" {
				finding.TokenID = &raw.TokenID
			}
			if raw.CampaignID != "" {
				finding.CampaignID = &raw.CampaignID
			}
			if raw.RecipientName != "" {
				finding.RecipientName = &raw.RecipientName
			}
			if raw.RecipientEmail != "" {
				finding.RecipientEmail = &raw.RecipientEmail
			}
			result.Result = finding
		}
	}

	renderJSON(w, http.StatusOK, result)
}
