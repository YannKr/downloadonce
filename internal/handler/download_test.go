package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
)

// seedActiveToken builds a single-use token whose watermarked file is at
// relPath under DataDir. The file itself is only written when write is true.
func seedActiveToken(t *testing.T, h *Handler, relPath string, write bool) string {
	t.Helper()
	assetID, recipientID := seedAccountAssetRecipient(t, h.DB, "acct-a")

	one := 1
	campaign := &model.Campaign{
		ID: uuid.New().String(), AccountID: "acct-a", AssetID: assetID,
		Name: "campaign", MaxDownloads: &one, State: "READY",
	}
	if err := db.CreateCampaign(h.DB, campaign); err != nil {
		t.Fatalf("create campaign: %v", err)
	}

	tokenID := uuid.New().String()
	token := &model.DownloadToken{
		ID: tokenID, CampaignID: campaign.ID, RecipientID: recipientID,
		MaxDownloads: &one, State: "PENDING",
	}
	if err := db.CreateToken(h.DB, token); err != nil {
		t.Fatalf("create token: %v", err)
	}
	if err := db.ActivateToken(h.DB, tokenID, relPath, "sha", 5); err != nil {
		t.Fatalf("activate token: %v", err)
	}

	if write {
		full := filepath.Join(h.Cfg.DataDir, relPath)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("hello"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
	return tokenID
}

func getDownload(t *testing.T, h *Handler, tokenID string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/d/"+tokenID+"/file", nil)
	r.RemoteAddr = "203.0.113.9:5555"

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", tokenID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	h.DownloadFile(rec, r)
	return rec
}

func downloadCount(t *testing.T, h *Handler, tokenID string) (int, string) {
	t.Helper()
	token, err := db.GetToken(h.DB, tokenID)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	return token.DownloadCount, token.State
}

func TestDownloadFileServesOnceThenRefuses(t *testing.T) {
	h := newTestHandler(t)
	tokenID := seedActiveToken(t, h, "watermarked/clip.mp4", true)

	rec := getDownload(t, h, tokenID)
	if rec.Code != http.StatusOK {
		t.Fatalf("first download = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "hello")
	}

	count, state := downloadCount(t, h, tokenID)
	if count != 1 || state != "CONSUMED" {
		t.Fatalf("after one download: count=%d state=%s, want 1/CONSUMED", count, state)
	}

	// The link is spent. The second request must not get the file.
	rec2 := getDownload(t, h, tokenID)
	if rec2.Code == http.StatusOK {
		t.Fatalf("second download returned 200 and %d bytes, want a refusal", rec2.Body.Len())
	}
	count, _ = downloadCount(t, h, tokenID)
	if count != 1 {
		t.Fatalf("download_count = %d after a refused request, want 1", count)
	}
}

func TestDownloadFileDoesNotCountAMissingFile(t *testing.T) {
	h := newTestHandler(t)
	tokenID := seedActiveToken(t, h, "watermarked/gone.mp4", false)

	rec := getDownload(t, h, tokenID)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}

	count, state := downloadCount(t, h, tokenID)
	if count != 0 {
		t.Fatalf("download_count = %d, want 0: a missing file must not burn the download", count)
	}
	if state != "ACTIVE" {
		t.Fatalf("state = %s, want ACTIVE", state)
	}

	// Once the file appears the same link still works.
	full := filepath.Join(h.Cfg.DataDir, "watermarked/gone.mp4")
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte("hello"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if rec := getDownload(t, h, tokenID); rec.Code != http.StatusOK {
		t.Fatalf("retry after the file appeared = %d, want 200", rec.Code)
	}
}

func TestDownloadFileRejectsAnUnknownToken(t *testing.T) {
	h := newTestHandler(t)
	if rec := getDownload(t, h, uuid.New().String()); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if rec := getDownload(t, h, "not-a-uuid"); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDownloadFileSurvivesADeletedCampaign(t *testing.T) {
	h := newTestHandler(t)
	tokenID := seedActiveToken(t, h, "watermarked/clip.mp4", true)

	token, err := db.GetToken(h.DB, tokenID)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	// Drop the campaign but keep the token, which is the state an older
	// database can be left in. The cascade would take the token with it.
	if _, err := h.DB.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatalf("pragma off: %v", err)
	}
	if _, err := h.DB.Exec(`DELETE FROM campaigns WHERE id = ?`, token.CampaignID); err != nil {
		t.Fatalf("delete campaign: %v", err)
	}
	if _, err := h.DB.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("pragma on: %v", err)
	}

	// Used to be a nil dereference on an unauthenticated route.
	rec := getDownload(t, h, tokenID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if count, _ := downloadCount(t, h, tokenID); count != 0 {
		t.Fatalf("download_count = %d, want 0", count)
	}
}
