package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/YannKr/downloadonce/internal/auth"
)

func TestAssetThumbnailRequiresOwnership(t *testing.T) {
	h := newTestHandler(t)
	assetA, _ := seedAccountAssetRecipient(t, h.DB, "acct-a")
	seedAccountAssetRecipient(t, h.DB, "acct-b")

	dir := filepath.Join(h.Cfg.DataDir, "originals", assetA)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "thumb.jpg"), []byte("jpegdata"), 0644); err != nil {
		t.Fatalf("write thumb: %v", err)
	}

	request := func(accountID string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/assets/"+assetA+"/thumb", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", assetA)
		ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
		ctx = auth.ContextWithAccountAndRole(ctx, accountID, "member", "tester")
		rec := httptest.NewRecorder()
		h.AssetThumbnail(rec, r.WithContext(ctx))
		return rec
	}

	if rec := request("acct-b"); rec.Code != http.StatusNotFound {
		t.Fatalf("acct-b fetched acct-a's thumbnail: status %d body %q", rec.Code, rec.Body.String())
	}
	rec := request("acct-a")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner was refused its own thumbnail: status %d", rec.Code)
	}
	if rec.Body.String() != "jpegdata" {
		t.Fatalf("body = %q, want the thumbnail bytes", rec.Body.String())
	}
}
