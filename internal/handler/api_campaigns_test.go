package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/YannKr/downloadonce/internal/auth"
	"github.com/YannKr/downloadonce/internal/config"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
)

// TestMain silences the migration and handler logs so a failing test is
// readable.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	dir := t.TempDir()
	database, err := db.Open(dir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := db.Migrate(database, os.DirFS("../..")); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := &config.Config{DataDir: dir, BaseURL: "http://localhost:8080"}
	return New(database, cfg, os.DirFS("../../templates"), nil, nil, nil)
}

func seedAccountAssetRecipient(t *testing.T, database *sql.DB, accountID string) (assetID, recipientID string) {
	t.Helper()
	err := db.CreateAccount(database, &model.Account{
		ID: accountID, Email: accountID + "@example.test", Name: accountID,
		PasswordHash: "x", Role: "member", Enabled: true,
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	assetID = accountID + "-asset"
	err = db.CreateAsset(database, &model.Asset{
		ID: assetID, AccountID: accountID, OriginalName: "clip.mp4",
		AssetType: "video", OriginalPath: "originals/" + assetID + "/source.mp4",
		MimeType: "video/mp4",
	})
	if err != nil {
		t.Fatalf("create asset: %v", err)
	}
	recipientID = accountID + "-recipient"
	err = db.CreateRecipient(database, &model.Recipient{
		ID: recipientID, AccountID: accountID, Name: "Someone",
		Email: recipientID + "@example.test",
	})
	if err != nil {
		t.Fatalf("create recipient: %v", err)
	}
	return assetID, recipientID
}

func postCampaign(t *testing.T, h *Handler, accountID, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/campaigns", strings.NewReader(body))
	r = r.WithContext(auth.ContextWithAccountAndRole(r.Context(), accountID, "member", "tester"))
	rec := httptest.NewRecorder()
	h.APICampaignCreate(rec, r)
	return rec
}

func TestAPICampaignCreateDefaultsMaxDownloadsToOne(t *testing.T) {
	h := newTestHandler(t)
	assetID, recipientID := seedAccountAssetRecipient(t, h.DB, "acct-a")

	rec := postCampaign(t, h, "acct-a", `{
		"name": "no max given",
		"asset_id": "`+assetID+`",
		"recipient_ids": ["`+recipientID+`"]
	}`)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	var got struct {
		ID           string `json:"id"`
		MaxDownloads *int   `json:"max_downloads"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}
	if got.MaxDownloads == nil {
		t.Fatal("max_downloads is null, want 1: an omitted field must not mean unlimited")
	}
	if *got.MaxDownloads != 1 {
		t.Fatalf("max_downloads = %d, want 1", *got.MaxDownloads)
	}

	// The tokens must carry the same cap, since that is what the download
	// path actually enforces.
	tokens, err := db.ListTokensByCampaign(h.DB, got.ID)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("got %d tokens, want 1", len(tokens))
	}
	if tokens[0].MaxDownloads == nil || *tokens[0].MaxDownloads != 1 {
		t.Fatalf("token max_downloads = %v, want 1", tokens[0].MaxDownloads)
	}
}

func TestAPICampaignCreateKeepsAnExplicitMaxDownloads(t *testing.T) {
	h := newTestHandler(t)
	assetID, recipientID := seedAccountAssetRecipient(t, h.DB, "acct-a")

	rec := postCampaign(t, h, "acct-a", `{
		"name": "three downloads",
		"asset_id": "`+assetID+`",
		"recipient_ids": ["`+recipientID+`"],
		"max_downloads": 3
	}`)

	var got struct {
		MaxDownloads *int `json:"max_downloads"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}
	if got.MaxDownloads == nil || *got.MaxDownloads != 3 {
		t.Fatalf("max_downloads = %v, want 3", got.MaxDownloads)
	}
}

func TestAPICampaignCreateRejectsAnotherAccountsAsset(t *testing.T) {
	h := newTestHandler(t)
	assetA, _ := seedAccountAssetRecipient(t, h.DB, "acct-a")
	_, recipientB := seedAccountAssetRecipient(t, h.DB, "acct-b")

	rec := postCampaign(t, h, "acct-b", `{
		"name": "stealing acct-a's file",
		"asset_id": "`+assetA+`",
		"recipient_ids": ["`+recipientB+`"]
	}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404. Body: %s", rec.Code, rec.Body.String())
	}
}

func TestAPICampaignCreateIgnoresAnotherAccountsRecipient(t *testing.T) {
	h := newTestHandler(t)
	assetA, recipientA := seedAccountAssetRecipient(t, h.DB, "acct-a")
	_, recipientB := seedAccountAssetRecipient(t, h.DB, "acct-b")

	rec := postCampaign(t, h, "acct-a", `{
		"name": "mixed recipients",
		"asset_id": "`+assetA+`",
		"recipient_ids": ["`+recipientA+`", "`+recipientB+`"]
	}`)

	var got struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}

	tokens, err := db.ListTokensByCampaign(h.DB, got.ID)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	for _, tok := range tokens {
		if tok.RecipientID == recipientB {
			t.Fatalf("acct-b's recipient %s was bound to acct-a's campaign", recipientB)
		}
	}
	if len(tokens) != 1 || tokens[0].RecipientID != recipientA {
		t.Fatalf("got %d tokens, want exactly one for %s", len(tokens), recipientA)
	}
}

func TestAPICampaignAddRecipientsIgnoresAnotherAccountsRecipient(t *testing.T) {
	h := newTestHandler(t)
	assetA, recipientA := seedAccountAssetRecipient(t, h.DB, "acct-a")
	_, recipientB := seedAccountAssetRecipient(t, h.DB, "acct-b")

	rec := postCampaign(t, h, "acct-a", `{
		"name": "campaign",
		"asset_id": "`+assetA+`",
		"recipient_ids": ["`+recipientA+`"]
	}`)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}

	r := httptest.NewRequest(http.MethodPost, "/api/v1/campaigns/"+created.ID+"/recipients",
		strings.NewReader(`{"recipient_ids": ["`+recipientB+`"]}`))
	r = r.WithContext(auth.ContextWithAccountAndRole(r.Context(), "acct-a", "member", "tester"))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", created.ID)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	rec2 := httptest.NewRecorder()
	h.APICampaignAddRecipients(rec2, r)

	tokens, err := db.ListTokensByCampaign(h.DB, created.ID)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	for _, tok := range tokens {
		if tok.RecipientID == recipientB {
			t.Fatalf("acct-b's recipient %s was added to acct-a's campaign", recipientB)
		}
	}
}

func TestOwnedRecipientIDsDropsForeignAndUnknownIDs(t *testing.T) {
	h := newTestHandler(t)
	_, recipientA := seedAccountAssetRecipient(t, h.DB, "acct-a")
	_, recipientB := seedAccountAssetRecipient(t, h.DB, "acct-b")

	got := h.ownedRecipientIDs([]string{recipientA, recipientB, "does-not-exist"}, "acct-a")
	if len(got) != 1 || got[0] != recipientA {
		t.Fatalf("ownedRecipientIDs = %v, want [%s]", got, recipientA)
	}
	if len(h.ownedRecipientIDs([]string{recipientA}, "acct-b")) != 0 {
		t.Fatal("acct-b was allowed to use acct-a's recipient")
	}
}
