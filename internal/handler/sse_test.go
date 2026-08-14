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
	"github.com/YannKr/downloadonce/internal/sse"
)

func TestCampaignSSERequiresOwnership(t *testing.T) {
	h := newTestHandler(t)
	h.SSE = sse.New()
	assetA, _ := seedAccountAssetRecipient(t, h.DB, "acct-a")
	seedAccountAssetRecipient(t, h.DB, "acct-b")

	campaignID := uuid.New().String()
	err := db.CreateCampaign(h.DB, &model.Campaign{
		ID: campaignID, AccountID: "acct-a", AssetID: assetA,
		Name: "acct-a campaign", State: "PROCESSING",
	})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}

	subscribe := func(accountID string) int {
		r := httptest.NewRequest(http.MethodGet, "/campaigns/"+campaignID+"/events", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", campaignID)
		ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
		ctx = auth.ContextWithAccountAndRole(ctx, accountID, "member", "tester")
		// Cancel straight away so the stream loop returns instead of blocking.
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		r = r.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.CampaignSSE(rec, r)
		return rec.Code
	}

	// The events on this topic carry token IDs, which are download URLs.
	if code := subscribe("acct-b"); code != http.StatusNotFound {
		t.Fatalf("acct-b subscribed to acct-a's campaign stream: status %d, want 404", code)
	}
	if code := subscribe("acct-a"); code != http.StatusOK {
		t.Fatalf("owner was refused its own stream: status %d, want 200", code)
	}
}

func TestDetectResultRequiresOwnership(t *testing.T) {
	h := newTestHandler(t)
	seedAccountAssetRecipient(t, h.DB, "acct-a")
	seedAccountAssetRecipient(t, h.DB, "acct-b")

	jobID := uuid.New().String()
	// EnqueueDetectJob stores the submitting account in campaign_id.
	if err := db.EnqueueDetectJob(h.DB, jobID, "acct-a", "detect/in.mp4", "detect_image"); err != nil {
		t.Fatalf("enqueue detect job: %v", err)
	}

	request := func(accountID string) int {
		r := httptest.NewRequest(http.MethodGet, "/detect/"+jobID, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", jobID)
		ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
		ctx = auth.ContextWithAccountAndRole(ctx, accountID, "member", "tester")
		rec := httptest.NewRecorder()
		h.DetectResult(rec, r.WithContext(ctx))
		return rec.Code
	}

	if code := request("acct-b"); code != http.StatusNotFound {
		t.Fatalf("acct-b read acct-a's detection result: status %d, want 404", code)
	}
	if code := request("acct-a"); code != http.StatusOK {
		t.Fatalf("owner was refused its own result: status %d, want 200", code)
	}
}
