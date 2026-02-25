package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YannKr/downloadonce/internal/auth"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
	"github.com/YannKr/downloadonce/internal/watermark"
)

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// UploadInit handles POST /upload/chunks/init
func (h *Handler) UploadInit(w http.ResponseWriter, r *http.Request) {
	accountID := auth.AccountFromContext(r.Context())
	var req struct {
		Filename  string `json:"filename"`
		Size      int64  `json:"size"`
		MimeType  string `json:"mime_type"`
		ChunkSize int64  `json:"chunk_size"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if len(req.Filename) == 0 || req.Size <= 0 || len(req.MimeType) == 0 || req.ChunkSize <= 0 {
		jsonError(w, "filename, size, mime_type, chunk_size required", http.StatusBadRequest)
		return
	}
	_, mimeOK := watermark.MimeToExt[req.MimeType]
	if !mimeOK {
		ext := strings.ToLower(filepath.Ext(req.Filename))
		for _, e := range watermark.MimeToExt {
			if e == ext {
				mimeOK = true
				break
			}
		}
	}
	if !mimeOK {
		jsonError(w, "unsupported file type", http.StatusBadRequest)
		return
	}
	totalChunks := int((req.Size + req.ChunkSize - 1) / req.ChunkSize)
	sessionID := uuid.New().String()
	now := time.Now()
	expiresAt := now.Add(time.Duration(h.Cfg.UploadSessionTTLHours) * time.Hour)
	sessionDir := filepath.Join(h.Cfg.DataDir, "uploads", sessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		slog.Error("upload init: mkdir", "error", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	session := &model.UploadSession{
		ID:          sessionID,
		AccountID:   accountID,
		Filename:    req.Filename,
		Size:        req.Size,
		MimeType:    req.MimeType,
		ChunkSize:   req.ChunkSize,
		TotalChunks: totalChunks,
		Status:      "PENDING",
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   expiresAt,
	}
	if err := db.CreateUploadSession(h.DB, session); err != nil {
		slog.Error("upload init: db create", "error", err)
		os.RemoveAll(sessionDir)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]interface{}{
		"session_id":   sessionID,
		"total_chunks": totalChunks,
		"chunk_size":   req.ChunkSize,
		"expires_at":   expiresAt.Format(time.RFC3339),
	})
}

// UploadChunk handles PUT /upload/chunks/{sessionID}/{chunkIndex}
func (h *Handler) UploadChunk(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	chunkIndex, err := strconv.Atoi(chi.URLParam(r, "chunkIndex"))
	if err != nil || chunkIndex < 0 {
		jsonError(w, "invalid chunk index", http.StatusBadRequest)
		return
	}
	session, err := db.GetUploadSession(h.DB, sessionID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}
	if session.AccountID != auth.AccountFromContext(r.Context()) {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}
	if session.Status != "PENDING" {
		jsonError(w, "session not in PENDING state", http.StatusBadRequest)
		return
	}
	if time.Now().After(session.ExpiresAt) {
		jsonError(w, "session expired", http.StatusGone)
		return
	}
	if chunkIndex >= session.TotalChunks {
		jsonError(w, "chunk index out of range", http.StatusBadRequest)
		return
	}
	chunkPath := filepath.Join(h.Cfg.DataDir, "uploads", sessionID, fmt.Sprintf("chunk_%d", chunkIndex))
	f, err := os.Create(chunkPath)
	if err != nil {
		slog.Error("upload chunk: create file", "error", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	if _, err = io.Copy(f, r.Body); err != nil {
		slog.Error("upload chunk: copy body", "error", err)
		os.Remove(chunkPath)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	recvd := session.ReceivedChunks
	found := false
	for _, c := range recvd {
		if c == chunkIndex {
			found = true
			break
		}
	}
	if !found {
		recvd = append(recvd, chunkIndex)
	}
	db.UpdateUploadSessionChunks(h.DB, sessionID, recvd)
	jsonOK(w, map[string]interface{}{
		"chunk_index":    chunkIndex,
		"received_count": len(recvd),
		"total_chunks":   session.TotalChunks,
	})
}

// UploadStatus handles GET /upload/chunks/{sessionID}/status
func (h *Handler) UploadStatus(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	session, err := db.GetUploadSession(h.DB, sessionID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}
	if session.AccountID != auth.AccountFromContext(r.Context()) {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}
	jsonOK(w, map[string]interface{}{
		"session_id":      session.ID,
		"filename":        session.Filename,
		"size":            session.Size,
		"total_chunks":    session.TotalChunks,
		"received_chunks": session.ReceivedChunks,
		"status":          session.Status,
		"expires_at":      session.ExpiresAt.Format(time.RFC3339),
	})
}

// UploadComplete handles POST /upload/chunks/{sessionID}/complete
func (h *Handler) UploadComplete(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	session, err := db.GetUploadSession(h.DB, sessionID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}
	accountID := auth.AccountFromContext(r.Context())
	if session.AccountID != accountID {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}
	if session.Status != "PENDING" {
		jsonError(w, "session not in PENDING state", http.StatusBadRequest)
		return
	}
	if time.Now().After(session.ExpiresAt) {
		jsonError(w, "session expired", http.StatusGone)
		return
	}
	if len(session.ReceivedChunks) != session.TotalChunks {
		jsonError(w, fmt.Sprintf("only %d of %d chunks received", len(session.ReceivedChunks), session.TotalChunks), http.StatusBadRequest)
		return
	}
	sort.Ints(session.ReceivedChunks)
	ext := strings.ToLower(filepath.Ext(session.Filename))
	if ext == "" {
		if mappedExt, ok := watermark.MimeToExt[session.MimeType]; ok {
			ext = mappedExt
		}
	}
	sessionDir := filepath.Join(h.Cfg.DataDir, "uploads", sessionID)
	finalPath := filepath.Join(sessionDir, "final"+ext)
	dst, err := os.Create(finalPath)
	if err != nil {
		slog.Error("upload complete: create final", "error", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	hasher := sha256.New()
	var assembleErr error
	for i := 0; i < session.TotalChunks; i++ {
		chunkPath := filepath.Join(sessionDir, fmt.Sprintf("chunk_%d", i))
		f, openErr := os.Open(chunkPath)
		if openErr != nil {
			assembleErr = openErr
			break
		}
		_, copyErr := io.Copy(dst, io.TeeReader(f, hasher))
		f.Close()
		if copyErr != nil {
			assembleErr = copyErr
			break
		}
	}
	dst.Close()
	if assembleErr != nil {
		slog.Error("upload complete: assemble", "error", assembleErr)
		os.Remove(finalPath)
		jsonError(w, "failed to assemble chunks", http.StatusInternalServerError)
		return
	}
	sha256Hex := hex.EncodeToString(hasher.Sum(nil))
	assetID := uuid.New().String()
	assetDir := filepath.Join(h.Cfg.DataDir, "originals", assetID)
	if err := os.MkdirAll(assetDir, 0755); err != nil {
		os.Remove(finalPath)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	destPath := filepath.Join(assetDir, "source"+ext)
	if err := os.Rename(finalPath, destPath); err != nil {
		if cpErr := copyFileUpload(finalPath, destPath); cpErr != nil {
			slog.Error("upload complete: move file", "error", cpErr)
			os.RemoveAll(assetDir)
			jsonError(w, "internal error", http.StatusInternalServerError)
			return
		}
		os.Remove(finalPath)
	}
	assetType := watermark.MimeToAssetType[session.MimeType]
	if assetType == "" {
		assetType = "video"
	}
	var duration *float64
	var width, height *int64
	if probe, probeErr := watermark.Probe(destPath); probeErr == nil && probe.Width > 0 {
		w64 := int64(probe.Width)
		h64 := int64(probe.Height)
		width = &w64
		height = &h64
		if probe.DurationSecs > 0 {
			duration = &probe.DurationSecs
		}
	}
	thumbPath := filepath.Join(assetDir, "thumb.jpg")
	ctx := context.Background()
	if assetType == "video" {
		seekSec := 1.0
		if duration != nil && *duration > 10 {
			seekSec = *duration * 0.1
		}
		watermark.ExtractVideoThumbnail(ctx, destPath, thumbPath, seekSec)
	} else {
		watermark.ExtractImageThumbnail(ctx, destPath, thumbPath)
	}
	var fileSize int64
	if fi, statErr := os.Stat(destPath); statErr == nil {
		fileSize = fi.Size()
	}
	asset := &model.Asset{
		ID:           assetID,
		AccountID:    accountID,
		OriginalName: session.Filename,
		AssetType:    assetType,
		OriginalPath: filepath.Join("originals", assetID, "source"+ext),
		FileSize:     fileSize,
		SHA256:       sha256Hex,
		MimeType:     session.MimeType,
		Duration:     duration,
		Width:        width,
		Height:       height,
	}
	if err := db.CreateAsset(h.DB, asset); err != nil {
		slog.Error("upload complete: insert asset", "error", err)
		os.RemoveAll(assetDir)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	db.CompleteUploadSession(h.DB, sessionID, destPath)
	cleanupUploadChunks(sessionDir, session.TotalChunks)
	db.InsertAuditLog(h.DB, accountID, "asset_uploaded_chunked", "asset", assetID, session.Filename, r.RemoteAddr)
	jsonOK(w, map[string]string{
		"asset_id": assetID,
		"filename": session.Filename,
	})
}

// UploadCancel handles DELETE /upload/chunks/{sessionID}
func (h *Handler) UploadCancel(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionID")
	session, err := db.GetUploadSession(h.DB, sessionID)
	if err != nil {
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}
	if session == nil {
		jsonError(w, "session not found", http.StatusNotFound)
		return
	}
	if session.AccountID != auth.AccountFromContext(r.Context()) {
		jsonError(w, "forbidden", http.StatusForbidden)
		return
	}
	os.RemoveAll(filepath.Join(h.Cfg.DataDir, "uploads", sessionID))
	db.DeleteUploadSession(h.DB, sessionID)
	w.WriteHeader(http.StatusNoContent)
}

func copyFileUpload(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func cleanupUploadChunks(sessionDir string, totalChunks int) {
	for i := 0; i < totalChunks; i++ {
		os.Remove(filepath.Join(sessionDir, fmt.Sprintf("chunk_%d", i)))
	}
}
