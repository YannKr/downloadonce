package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/YannKr/downloadonce/internal/auth"
	"github.com/YannKr/downloadonce/internal/db"
	"github.com/YannKr/downloadonce/internal/model"
)

// twoCampaigns builds one campaign per account, each with its own ACTIVE token,
// and returns the two campaign ids and the two token ids.
func twoCampaigns(t *testing.T, h *Handler) (campA, tokenA, campB, tokenB string) {
	t.Helper()
	mk := func(account string) (string, string) {
		assetID, recipientID := seedAccountAssetRecipient(t, h.DB, account)
		one := 1
		campaignID := uuid.New().String()
		err := db.CreateCampaign(h.DB, &model.Campaign{
			ID: campaignID, AccountID: account, AssetID: assetID,
			Name: account + " campaign", MaxDownloads: &one, State: "READY",
		})
		if err != nil {
			t.Fatalf("create campaign: %v", err)
		}
		tokenID := uuid.New().String()
		err = db.CreateToken(h.DB, &model.DownloadToken{
			ID: tokenID, CampaignID: campaignID, RecipientID: recipientID,
			MaxDownloads: &one, State: "ACTIVE",
		})
		if err != nil {
			t.Fatalf("create token: %v", err)
		}
		return campaignID, tokenID
	}
	campA, tokenA = mk("acct-a")
	campB, tokenB = mk("acct-b")
	return
}

func tokenRequest(accountID, campaignID, tokenID string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/campaigns/"+campaignID+"/tokens/"+tokenID, nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", campaignID)
	rctx.URLParams.Add("tokenID", tokenID)
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	ctx = auth.ContextWithAccountAndRole(ctx, accountID, "member", "tester")
	return r.WithContext(ctx)
}

func tokenState(t *testing.T, h *Handler, tokenID string) string {
	t.Helper()
	token, err := db.GetToken(h.DB, tokenID)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if token == nil {
		t.Fatalf("token %s is gone", tokenID)
	}
	return token.State
}

func TestTokenRevokeOnlyAffectsTokensOfTheNamedCampaign(t *testing.T) {
	h := newTestHandler(t)
	campA, tokenA, _, tokenB := twoCampaigns(t, h)

	// acct-a owns campA. Naming campA but another campaign's token must not work.
	rec := httptest.NewRecorder()
	h.TokenRevoke(rec, tokenRequest("acct-a", campA, tokenB))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if state := tokenState(t, h, tokenB); state != "ACTIVE" {
		t.Fatalf("acct-b's token state = %s, want ACTIVE", state)
	}

	// The owner's own token still revokes.
	rec2 := httptest.NewRecorder()
	h.TokenRevoke(rec2, tokenRequest("acct-a", campA, tokenA))
	if rec2.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec2.Code)
	}
	if state := tokenState(t, h, tokenA); state != "EXPIRED" {
		t.Fatalf("own token state = %s, want EXPIRED", state)
	}
}

func TestAPICampaignRevokeTokenOnlyAffectsTokensOfTheNamedCampaign(t *testing.T) {
	h := newTestHandler(t)
	campA, tokenA, _, tokenB := twoCampaigns(t, h)

	rec := httptest.NewRecorder()
	h.APICampaignRevokeToken(rec, tokenRequest("acct-a", campA, tokenB))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404. Body: %s", rec.Code, rec.Body.String())
	}
	if state := tokenState(t, h, tokenB); state != "ACTIVE" {
		t.Fatalf("acct-b's token state = %s, want ACTIVE", state)
	}

	rec2 := httptest.NewRecorder()
	h.APICampaignRevokeToken(rec2, tokenRequest("acct-a", campA, tokenA))
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204. Body: %s", rec2.Code, rec2.Body.String())
	}
	if state := tokenState(t, h, tokenA); state != "EXPIRED" {
		t.Fatalf("own token state = %s, want EXPIRED", state)
	}
}

func TestTokenRetryOnlyAffectsJobsOfTheNamedCampaign(t *testing.T) {
	h := newTestHandler(t)
	campA, _, campB, tokenB := twoCampaigns(t, h)

	// A failed job on acct-b's campaign.
	jobB := uuid.New().String()
	err := db.EnqueueJob(h.DB, &model.Job{
		ID: jobB, JobType: "watermark_video", CampaignID: campB, TokenID: tokenB,
	})
	if err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	if err := db.FailJob(h.DB, jobB, "boom"); err != nil {
		t.Fatalf("fail job: %v", err)
	}

	rec := httptest.NewRecorder()
	h.TokenRetry(rec, tokenRequest("acct-a", campA, tokenB))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 back to the campaign page", rec.Code)
	}

	job, err := db.GetJob(h.DB, jobB)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if job.State != "FAILED" {
		t.Fatalf("acct-b's job state = %s, want it left FAILED", job.State)
	}
}
