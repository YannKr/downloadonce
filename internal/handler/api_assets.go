package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YannKr/downloadonce/internal/auth"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
	"github.com/YannKr/downloadonce/internal/watermark"
)

type apiAsset struct {
	ID            string   `json:"id"`
	AccountID     string   `json:"account_id"`
	Title         string   `json:"title"`
	AssetType     string   `json:"asset_type"`
	MimeType      string   `json:"mime_type"`
	FileSizeBytes int64    `json:"file_size_bytes"`
	SHA256        string   `json:"sha256"`
	DurationSecs  *float64 `json:"duration_secs"`
	Width         *int64   `json:"width"`
	Height        *int64   `json:"height"`
	CreatedAt     string   `json:"created_at"`
}

func assetToAPI(a *model.Asset) apiAsset {
	return apiAsset{
		ID:            a.ID,
		AccountID:     a.AccountID,
		Title:         a.OriginalName,
		AssetType:     a.AssetType,
		MimeType:      a.MimeType,
		FileSizeBytes: a.FileSize,
		SHA256:        a.SHA256,
		DurationSecs:  a.Duration,
		Width:         a.Width,
		Height:        a.Height,
		CreatedAt:     a.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// APIAssetUpload — POST /api/v1/assets
func (h *Handler) APIAssetUpload(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())

	if err := r.ParseMultipartForm(2 << 30); err != nil {
		renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "failed to parse multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		renderJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "missing 'file' field in form")
		return
	}
	defer file.Close()

	asset, err := h.processUploadReturn(accountID, header, file)
	if err != nil {
		if err.Error() == "unsupported_media_type" {
			renderJSONError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "unsupported file type")
			return
		}
		slog.Error("api asset upload", "error", err)
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to upload asset")
		return
	}

	db.InsertAuditLog(h.DB, accountID, "asset_uploaded", "asset", asset.ID, asset.OriginalName, r.RemoteAddr)
	renderJSON(w, http.StatusCreated, assetToAPI(asset))
}

// processUploadReturn is like processOneUpload but returns the created asset.
func (h *Handler) processUploadReturn(accountID string, header *multipart.FileHeader, file multipart.File) (*model.Asset, error) {
	originalName := header.Filename

	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	mimeType := http.DetectContentType(buf[:n])
	file.Seek(0, io.SeekStart)

	ext, ok := watermark.MimeToExt[mimeType]
	if !ok {
		origExt := strings.ToLower(filepath.Ext(header.Filename))
		found := false
		for _, e := range watermark.MimeToExt {
			if e == origExt {
				ext = origExt
				for m, x := range watermark.MimeToExt {
					if x == origExt {
						mimeType = m
						break
					}
				}
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unsupported_media_type")
		}
	}

	assetType := watermark.MimeToAssetType[mimeType]
	assetID := uuid.New().String()

	assetDir := filepath.Join(h.Cfg.DataDir, "originals", assetID)
	if err := os.MkdirAll(assetDir, 0755); err != nil {
		return nil, fmt.Errorf("create asset dir: %w", err)
	}

	srcPath := filepath.Join(assetDir, "source"+ext)
	dst, err := os.Create(srcPath)
	if err != nil {
		os.RemoveAll(assetDir)
		return nil, fmt.Errorf("create file: %w", err)
	}

	hasher := sha256.New()
	written, err := io.Copy(dst, io.TeeReader(file, hasher))
	dst.Close()
	if err != nil {
		os.RemoveAll(assetDir)
		return nil, fmt.Errorf("write file: %w", err)
	}

	sha256Hex := hex.EncodeToString(hasher.Sum(nil))

	var duration *float64
	var width, height *int64
	if assetType == "video" {
		probe, err := watermark.Probe(srcPath)
		if err != nil {
			slog.Warn("ffprobe failed", "error", err)
		} else {
			duration = &probe.DurationSecs
			w64 := int64(probe.Width)
			h64 := int64(probe.Height)
			width = &w64
			height = &h64
		}
	} else {
		probe, err := watermark.Probe(srcPath)
		if err == nil && probe.Width > 0 {
			w64 := int64(probe.Width)
			h64 := int64(probe.Height)
			width = &w64
			height = &h64
		}
	}

	thumbPath := filepath.Join(assetDir, "thumb.jpg")
	ctx := context.Background()
	if assetType == "video" {
		seekSec := 1.0
		if duration != nil && *duration > 10 {
			seekSec = *duration * 0.1
		}
		if err := watermark.ExtractVideoThumbnail(ctx, srcPath, thumbPath, seekSec); err != nil {
			slog.Warn("thumbnail extraction failed", "error", err)
		}
	} else {
		if err := watermark.ExtractImageThumbnail(ctx, srcPath, thumbPath); err != nil {
			slog.Warn("thumbnail extraction failed", "error", err)
		}
	}

	asset := &model.Asset{
		ID:           assetID,
		AccountID:    accountID,
		OriginalName: originalName,
		AssetType:    assetType,
		OriginalPath: filepath.Join("originals", assetID, "source"+ext),
		FileSize:     written,
		SHA256:       sha256Hex,
		MimeType:     mimeType,
		Duration:     duration,
		Width:        width,
		Height:       height,
	}

	if err := db.CreateAsset(h.DB, asset); err != nil {
		os.RemoveAll(assetDir)
		return nil, fmt.Errorf("insert asset: %w", err)
	}

	return asset, nil
}

// APIAssetList — GET /api/v1/assets
func (h *Handler) APIAssetList(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	isAdmin := auth.IsAdmin(r.Context())

	assets, err := db.ListAssets(h.DB)
	if err != nil {
		slog.Error("api list assets", "error", err)
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list assets")
		return
	}

	// Filter by account unless admin
	if !isAdmin {
		filtered := assets[:0]
		for _, a := range assets {
			if a.AccountID == accountID {
				filtered = append(filtered, a)
			}
		}
		assets = filtered
	}

	page, perPage := paginate(r)
	total := len(assets)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	slice := assets[start:end]

	result := make([]apiAsset, len(slice))
	for i, a := range slice {
		result[i] = assetToAPI(&a)
	}

	renderJSON(w, http.StatusOK, paginatedResult{
		Data:    result,
		Total:   total,
		Page:    page,
		PerPage: perPage,
	})
}

// APIAssetGet — GET /api/v1/assets/{id}
func (h *Handler) APIAssetGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	asset, err := db.GetAsset(h.DB, id)
	if err != nil {
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get asset")
		return
	}
	if asset == nil || (asset.AccountID != accountID && !auth.IsAdmin(r.Context())) {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "asset not found")
		return
	}

	renderJSON(w, http.StatusOK, assetToAPI(asset))
}

// APIAssetDelete — DELETE /api/v1/assets/{id}
func (h *Handler) APIAssetDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	asset, err := db.GetAsset(h.DB, id)
	if err != nil {
		renderJSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to get asset")
		return
	}
	if asset == nil || (asset.AccountID != accountID && !auth.IsAdmin(r.Context())) {
		renderJSONError(w, http.StatusNotFound, "NOT_FOUND", "asset not found")
		return
	}

	db.DeleteAsset(h.DB, id)
	os.RemoveAll(filepath.Join(h.Cfg.DataDir, "originals", id))
	db.InsertAuditLog(h.DB, accountID, "asset_deleted", "asset", id, "", r.RemoteAddr)

	w.WriteHeader(http.StatusNoContent)
}
