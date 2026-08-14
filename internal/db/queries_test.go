package db

import (
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"

	"github.com/YannKr/downloadonce/internal/model"
)

// TestMain silences the migration and handler logs so a failing test is
// readable.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// newTestDB opens a migrated database in a temporary directory.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := Migrate(database, os.DirFS("../..")); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

func mustAccount(t *testing.T, database *sql.DB, id string) {
	t.Helper()
	err := CreateAccount(database, &model.Account{
		ID:           id,
		Email:        id + "@example.test",
		Name:         id,
		PasswordHash: "x",
		Role:         "member",
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("create account %s: %v", id, err)
	}
}

func mustAsset(t *testing.T, database *sql.DB, id, accountID string) {
	t.Helper()
	err := CreateAsset(database, &model.Asset{
		ID:           id,
		AccountID:    accountID,
		OriginalName: id,
		AssetType:    "video",
		OriginalPath: "originals/" + id + "/source.mp4",
		MimeType:     "video/mp4",
	})
	if err != nil {
		t.Fatalf("create asset %s: %v", id, err)
	}
}

func mustRecipient(t *testing.T, database *sql.DB, id, accountID string) {
	t.Helper()
	err := CreateRecipient(database, &model.Recipient{
		ID:        id,
		AccountID: accountID,
		Name:      id,
		Email:     id + "@example.test",
	})
	if err != nil {
		t.Fatalf("create recipient %s: %v", id, err)
	}
}

func TestListAssetsIsScopedToAccount(t *testing.T) {
	database := newTestDB(t)
	mustAccount(t, database, "acct-a")
	mustAccount(t, database, "acct-b")
	mustAsset(t, database, "asset-a", "acct-a")
	mustAsset(t, database, "asset-b", "acct-b")

	assets, err := ListAssets(database, "acct-a", false)
	if err != nil {
		t.Fatalf("list assets: %v", err)
	}
	if len(assets) != 1 || assets[0].ID != "asset-a" {
		t.Fatalf("acct-a saw %d assets %v, want only asset-a", len(assets), assets)
	}

	assets, err = ListAssets(database, "acct-b", false)
	if err != nil {
		t.Fatalf("list assets: %v", err)
	}
	if len(assets) != 1 || assets[0].ID != "asset-b" {
		t.Fatalf("acct-b saw %d assets %v, want only asset-b", len(assets), assets)
	}
}

func TestListAssetsShowAllReturnsEveryAccount(t *testing.T) {
	database := newTestDB(t)
	mustAccount(t, database, "acct-a")
	mustAccount(t, database, "acct-b")
	mustAsset(t, database, "asset-a", "acct-a")
	mustAsset(t, database, "asset-b", "acct-b")

	assets, err := ListAssets(database, "acct-a", true)
	if err != nil {
		t.Fatalf("list assets: %v", err)
	}
	if len(assets) != 2 {
		t.Fatalf("showAll returned %d assets, want 2", len(assets))
	}
}

func TestListRecipientsIsScopedToAccount(t *testing.T) {
	database := newTestDB(t)
	mustAccount(t, database, "acct-a")
	mustAccount(t, database, "acct-b")
	mustRecipient(t, database, "rec-a", "acct-a")
	mustRecipient(t, database, "rec-b", "acct-b")

	recipients, err := ListRecipients(database, "acct-a", false)
	if err != nil {
		t.Fatalf("list recipients: %v", err)
	}
	if len(recipients) != 1 || recipients[0].ID != "rec-a" {
		t.Fatalf("acct-a saw %d recipients %v, want only rec-a", len(recipients), recipients)
	}

	all, err := ListRecipients(database, "acct-a", true)
	if err != nil {
		t.Fatalf("list recipients: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("showAll returned %d recipients, want 2", len(all))
	}
}

func TestListRecipientsWithGroupsIsScopedToAccount(t *testing.T) {
	database := newTestDB(t)
	mustAccount(t, database, "acct-a")
	mustAccount(t, database, "acct-b")
	mustRecipient(t, database, "rec-a", "acct-a")
	mustRecipient(t, database, "rec-b", "acct-b")

	recipients, err := ListRecipientsWithGroups(database, "acct-a", false)
	if err != nil {
		t.Fatalf("list recipients with groups: %v", err)
	}
	if len(recipients) != 1 || recipients[0].ID != "rec-a" {
		t.Fatalf("acct-a saw %d recipients, want only rec-a", len(recipients))
	}
}

// seedToken creates the account, asset, recipient, campaign and token needed to
// exercise IncrementDownloadCount, and returns the token ID.
func seedToken(t *testing.T, database *sql.DB, maxDownloads *int) string {
	t.Helper()
	mustAccount(t, database, "acct-a")
	mustAsset(t, database, "asset-a", "acct-a")
	mustRecipient(t, database, "rec-a", "acct-a")

	campaign := &model.Campaign{
		ID:           "camp-a",
		AccountID:    "acct-a",
		AssetID:      "asset-a",
		Name:         "campaign",
		MaxDownloads: maxDownloads,
		State:        "READY",
	}
	if err := CreateCampaign(database, campaign); err != nil {
		t.Fatalf("create campaign: %v", err)
	}

	token := &model.DownloadToken{
		ID:           "token-a",
		CampaignID:   "camp-a",
		RecipientID:  "rec-a",
		MaxDownloads: maxDownloads,
		State:        "ACTIVE",
	}
	if err := CreateToken(database, token); err != nil {
		t.Fatalf("create token: %v", err)
	}
	return token.ID
}

func TestIncrementDownloadCountConsumesSingleUseToken(t *testing.T) {
	database := newTestDB(t)
	one := 1
	tokenID := seedToken(t, database, &one)

	count, _, err := IncrementDownloadCount(database, tokenID)
	if err != nil {
		t.Fatalf("first increment: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	// The second attempt must not match a row. The handler treats ErrNoRows as
	// "this link is used" and serves nothing.
	if _, _, err := IncrementDownloadCount(database, tokenID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("second increment err = %v, want sql.ErrNoRows", err)
	}

	token, err := GetToken(database, tokenID)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if token.State != "CONSUMED" {
		t.Fatalf("state = %q, want CONSUMED", token.State)
	}
	if token.DownloadCount != 1 {
		t.Fatalf("download_count = %d, want 1", token.DownloadCount)
	}
}

func TestIncrementDownloadCountLetsExactlyOneParallelRequestWin(t *testing.T) {
	database := newTestDB(t)
	one := 1
	tokenID := seedToken(t, database, &one)

	const racers = 8
	var wg sync.WaitGroup
	results := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, results[i] = IncrementDownloadCount(database, tokenID)
		}(i)
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, sql.ErrNoRows):
		default:
			t.Fatalf("racer %d: unexpected error %v", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d parallel requests were granted the download, want 1", winners)
	}

	token, err := GetToken(database, tokenID)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if token.DownloadCount != 1 {
		t.Fatalf("download_count = %d, want 1", token.DownloadCount)
	}
}

func TestIncrementDownloadCountAllowsSecondUseWhenMaxIsTwo(t *testing.T) {
	database := newTestDB(t)
	two := 2
	tokenID := seedToken(t, database, &two)

	if _, _, err := IncrementDownloadCount(database, tokenID); err != nil {
		t.Fatalf("first increment: %v", err)
	}
	count, _, err := IncrementDownloadCount(database, tokenID)
	if err != nil {
		t.Fatalf("second increment: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	if _, _, err := IncrementDownloadCount(database, tokenID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("third increment err = %v, want sql.ErrNoRows", err)
	}
}

func TestListNonMembersIsScopedToAccount(t *testing.T) {
	database := newTestDB(t)
	mustAccount(t, database, "acct-a")
	mustAccount(t, database, "acct-b")
	mustRecipient(t, database, "rec-a1", "acct-a")
	mustRecipient(t, database, "rec-a2", "acct-a")
	mustRecipient(t, database, "rec-b", "acct-b")

	if err := CreateRecipientGroup(database, "grp-a", "acct-a", "Group A", ""); err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := AddGroupMember(database, "grp-a", "rec-a1"); err != nil {
		t.Fatalf("add member: %v", err)
	}

	nonMembers, err := ListNonMembers(database, "grp-a", "acct-a")
	if err != nil {
		t.Fatalf("list non-members: %v", err)
	}
	if len(nonMembers) != 1 {
		t.Fatalf("got %d non-members %v, want only rec-a2", len(nonMembers), nonMembers)
	}
	if nonMembers[0].ID != "rec-a2" {
		t.Fatalf("non-member = %q, want rec-a2", nonMembers[0].ID)
	}
	for _, r := range nonMembers {
		if r.AccountID != "acct-a" {
			t.Fatalf("group page would show %s of account %s", r.Email, r.AccountID)
		}
	}
}

func TestGetOrCreateRecipientByEmailIsScopedToAccount(t *testing.T) {
	database := newTestDB(t)
	mustAccount(t, database, "acct-a")
	mustAccount(t, database, "acct-b")

	shared := "shared@example.test"
	err := CreateRecipient(database, &model.Recipient{
		ID: "rec-a", AccountID: "acct-a", Name: "Victim", Email: shared, Org: "Victim Ltd",
	})
	if err != nil {
		t.Fatalf("create recipient: %v", err)
	}

	// acct-b asks for the same address. It must get a new unsaved record for
	// its own account, not acct-a's row.
	got, err := GetOrCreateRecipientByEmail(database, "acct-b", "Attacker", shared, "")
	if err != nil {
		t.Fatalf("get or create: %v", err)
	}
	if got.ID != "" {
		t.Fatalf("returned an existing row %q owned by %s", got.ID, got.AccountID)
	}
	if got.AccountID != "acct-b" {
		t.Fatalf("account = %q, want acct-b", got.AccountID)
	}
	if got.Name != "Attacker" || got.Org != "" {
		t.Fatalf("returned acct-a's details: name=%q org=%q", got.Name, got.Org)
	}

	// The owning account still gets its own row back.
	mine, err := GetOrCreateRecipientByEmail(database, "acct-a", "ignored", shared, "")
	if err != nil {
		t.Fatalf("get or create: %v", err)
	}
	if mine.ID != "rec-a" || mine.Name != "Victim" {
		t.Fatalf("owner got %+v, want rec-a", mine)
	}
}

// The consumed flag must agree with the token's state for every max_downloads,
// including the boundary where one download is the whole allowance.
func TestIncrementDownloadCountConsumedFlagTracksState(t *testing.T) {
	for _, max := range []int{1, 2, 3} {
		database := newTestDB(t)
		m := max
		tokenID := seedToken(t, database, &m)

		for call := 1; call <= max; call++ {
			count, consumed, err := IncrementDownloadCount(database, tokenID)
			if err != nil {
				t.Fatalf("max=%d call=%d: %v", max, call, err)
			}
			if count != call {
				t.Errorf("max=%d call=%d: count = %d, want %d", max, call, count, call)
			}

			token, err := GetToken(database, tokenID)
			if err != nil {
				t.Fatalf("get token: %v", err)
			}
			wantConsumed := call >= max
			if consumed != wantConsumed {
				t.Errorf("max=%d call=%d: consumed = %v, want %v", max, call, consumed, wantConsumed)
			}
			if got := token.State == "CONSUMED"; consumed != got {
				t.Errorf("max=%d call=%d: consumed = %v but state = %s", max, call, consumed, token.State)
			}
		}
	}
}

// An unlimited token is never consumed and stays usable.
func TestIncrementDownloadCountUnlimitedTokenIsNeverConsumed(t *testing.T) {
	database := newTestDB(t)
	tokenID := seedToken(t, database, nil)

	for call := 1; call <= 3; call++ {
		count, consumed, err := IncrementDownloadCount(database, tokenID)
		if err != nil {
			t.Fatalf("call %d: %v", call, err)
		}
		if count != call {
			t.Errorf("call %d: count = %d, want %d", call, count, call)
		}
		if consumed {
			t.Errorf("call %d: consumed = true for a token with no max_downloads", call)
		}
	}
}
