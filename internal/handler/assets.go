package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	"github.com/ypk/downloadonce/internal/model"
	"github.com/ypk/downloadonce/internal/watermark"
)

func (h *Handler) AssetList(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	assets, err := db.ListAssets(h.DB, accountID)
	if err != nil {
		slog.Error("list assets", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}
	h.render(w, "assets.html", PageData{
		Title:         "Assets",
		Authenticated: true,
		Data:          assets,
	})
}

func (h *Handler) AssetUploadForm(w http.ResponseWriter, r *http.Request) {
	h.render(w, "asset_upload.html", PageData{Title: "Upload Asset", Authenticated: true})
}

func (h *Handler) AssetUploadSubmit(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		h.render(w, "asset_upload.html", PageData{
			Title: "Upload Asset", Authenticated: true,
			Error: "Failed to parse upload.",
		})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		h.render(w, "asset_upload.html", PageData{
			Title: "Upload Asset", Authenticated: true,
			Error: "No file selected.",
		})
		return
	}
	defer file.Close()

	originalName := header.Filename

	// Detect MIME type from first 512 bytes
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	mimeType := http.DetectContentType(buf[:n])
	file.Seek(0, io.SeekStart)

	// Check allowed types
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
			h.render(w, "asset_upload.html", PageData{
				Title: "Upload Asset", Authenticated: true,
				Error: fmt.Sprintf("Unsupported file type: %s", mimeType),
			})
			return
		}
	}

	assetType := watermark.MimeToAssetType[mimeType]
	assetID := uuid.New().String()

	assetDir := filepath.Join(h.Cfg.DataDir, "originals", assetID)
	if err := os.MkdirAll(assetDir, 0755); err != nil {
		slog.Error("create asset dir", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}

	srcPath := filepath.Join(assetDir, "source"+ext)

	dst, err := os.Create(srcPath)
	if err != nil {
		slog.Error("create file", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}

	hasher := sha256.New()
	written, err := io.Copy(dst, io.TeeReader(file, hasher))
	dst.Close()
	if err != nil {
		os.RemoveAll(assetDir)
		slog.Error("write file", "error", err)
		http.Error(w, "Internal error", 500)
		return
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
		slog.Error("insert asset", "error", err)
		http.Error(w, "Internal error", 500)
		return
	}

	http.Redirect(w, r, "/assets", http.StatusSeeOther)
}

func (h *Handler) AssetThumbnail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	thumbPath := filepath.Join(h.Cfg.DataDir, "originals", id, "thumb.jpg")
	if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, thumbPath)
}

func (h *Handler) AssetDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	accountID := auth.AccountFromContext(r.Context())

	asset, err := db.GetAsset(h.DB, id)
	if err != nil || asset == nil || asset.AccountID != accountID {
		http.NotFound(w, r)
		return
	}

	db.DeleteAsset(h.DB, id)
	os.RemoveAll(filepath.Join(h.Cfg.DataDir, "originals", id))

	http.Redirect(w, r, "/assets", http.StatusSeeOther)
}
