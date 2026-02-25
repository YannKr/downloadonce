# Spec 08: Campaign Cloning + Export / Copy All Links

**Status:** Draft
**Date:** 2026-02-23
**Scope:** Two UX improvements to the campaign detail page. No schema changes required. Can be implemented independently.

---

## Feature A: Campaign Cloning

### Problem

Users regularly distribute the same content to an evolving recipient list, or a recurring audience (e.g., a festival circuit) for a new film cut. Every campaign today requires re-selecting recipients and re-entering all settings from scratch. There is no reuse path.

### User Stories

1. **Same audience, new film** — "I sent last year's screener to 60 festival programmers. I just finished the new film. I want to clone the campaign, swap the asset, and publish."
2. **Same asset, fresh list** — "I want a copy of this campaign's settings (watermark options, download limits, expiry) as a starting point for a different recipient group."
3. **Template-style reuse** — "I maintain a standard distribution template: 3 max downloads, 30-day expiry, both watermark layers on. I want to spin up campaigns from it without re-entering those fields."

### HTTP Endpoint

```
POST /campaigns/{id}/clone
Content-Type: application/x-www-form-urlencoded

name=      (optional override; defaults to "<original name> (copy)")
asset_id=  (optional override; defaults to original asset_id)
expires_at= (optional override; ISO datetime string "2006-01-02T15:04")
```

**Response:** `303 See Other` → `/campaigns/<new_id>` (the new DRAFT campaign's detail page).

**Errors:**
- `404` if the source campaign does not exist or does not belong to the authenticated account.
- `500` on transaction failure (logged with `slog.Error`).

The endpoint is idempotent in the sense that each POST always produces a distinct new campaign. CSRF protection applies (same as all other POST routes).

### What Gets Copied

| Field | Behaviour |
|---|---|
| `name` | Copied with " (copy)" suffix appended, unless `name` override is provided in the POST body |
| `asset_id` | Copied as-is, unless `asset_id` override is provided |
| `max_downloads` | Copied |
| `expires_at` | Copied, unless `expires_at` override is provided |
| `visible_wm` | Copied |
| `invisible_wm` | Copied |
| `state` | Always `DRAFT` regardless of source state |
| `account_id` | Set to the authenticated user's account ID |
| `published_at` | Not set (NULL) |

**Recipient tokens:** New `download_tokens` rows are created (state `PENDING`) for each recipient ID found in the source campaign's token list. One token per recipient, inheriting `max_downloads` and `expires_at` from the new campaign.

**What is NOT copied:** watermarked file paths, watermark payloads, job rows, download events, download counts.

### Edge Cases

**Source asset deleted:**
`db.GetAsset` will return `nil`. The clone proceeds — the new campaign is created with the original `asset_id` value. A flash warning is set: `"Campaign cloned, but the original asset no longer exists. Please select a new asset before publishing."` The DRAFT campaign page already shows the asset name; the handler should tolerate a nil asset on the clone path (unlike `CampaignDetail`, which requires a valid asset for display — that page will render a broken-asset warning via the existing asset lookup).

A cleaner alternative: before creating the clone, attempt `db.GetAsset(h.DB, campaign.AssetID)`. If nil, set `assetID = ""` on the new campaign row and display the warning. The campaign form is shown in edit/new mode so the user can pick a replacement asset. This is the recommended approach.

**Recipient deleted since original campaign was created:**
`db.ListTokensByCampaign` JOINs `recipients` — deleted recipients would only appear if the row still exists. If a recipient was hard-deleted (the `recipients` table uses `ON DELETE CASCADE` from `accounts`), their token row in the source campaign is also gone. The clone loop should query recipient IDs from source tokens and attempt `db.CreateToken` for each. Any `db.CreateToken` failure for a missing recipient is logged and skipped. After the loop, if any were skipped, a flash message is appended: `"N recipient(s) were skipped because they no longer exist."` The new campaign is still created successfully.

**Cloning a PROCESSING or READY campaign:**
Allowed. The new campaign is always DRAFT regardless. The watermarked files of the source campaign are not referenced or moved.

### DB Query: `CloneCampaign`

Add to `internal/db/queries_campaigns.go`:

```go
// CloneCampaign creates a new DRAFT campaign and its PENDING tokens
// inside a single transaction. recipientIDs is the list of recipient IDs
// to re-create tokens for. Returns the new campaign ID and the number of
// tokens that could not be inserted (e.g., recipient deleted).
func CloneCampaign(
    database *sql.DB,
    newCampaign *model.Campaign,
    recipientIDs []string,
) (skipped int, err error) {
    tx, err := database.Begin()
    if err != nil {
        return 0, err
    }
    defer tx.Rollback()

    var expiresAt *string
    if newCampaign.ExpiresAt != nil {
        s := newCampaign.ExpiresAt.UTC().Format(time.RFC3339)
        expiresAt = &s
    }

    _, err = tx.Exec(
        `INSERT INTO campaigns
             (id, account_id, asset_id, name, max_downloads, expires_at,
              visible_wm, invisible_wm, state)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'DRAFT')`,
        newCampaign.ID, newCampaign.AccountID, newCampaign.AssetID,
        newCampaign.Name, newCampaign.MaxDownloads, expiresAt,
        boolToInt(newCampaign.VisibleWM), boolToInt(newCampaign.InvisibleWM),
    )
    if err != nil {
        return 0, err
    }

    for _, rid := range recipientIDs {
        tokenID := uuid.New().String()
        _, terr := tx.Exec(
            `INSERT INTO download_tokens
                 (id, campaign_id, recipient_id, max_downloads, state, expires_at)
             VALUES (?, ?, ?, ?, 'PENDING', ?)`,
            tokenID, newCampaign.ID, rid, newCampaign.MaxDownloads, expiresAt,
        )
        if terr != nil {
            skipped++
            slog.Warn("clone: skip recipient", "recipient_id", rid, "error", terr)
        }
    }

    return skipped, tx.Commit()
}
```

### Handler: `CampaignClone`

Add to `internal/handler/campaigns.go`:

```go
func (h *Handler) CampaignClone(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    accountID := auth.AccountFromContext(r.Context())

    src, err := db.GetCampaign(h.DB, id)
    if err != nil || src == nil || (src.AccountID != accountID && !auth.IsAdmin(r.Context())) {
        http.NotFound(w, r)
        return
    }

    // Collect source recipient IDs
    srcTokens, err := db.ListTokensByCampaign(h.DB, id)
    if err != nil {
        slog.Error("clone: list tokens", "error", err)
        http.Error(w, "Internal error", 500)
        return
    }
    recipientIDs := make([]string, 0, len(srcTokens))
    for _, t := range srcTokens {
        recipientIDs = append(recipientIDs, t.RecipientID)
    }

    r.ParseForm()

    // Resolve name
    name := strings.TrimSpace(r.FormValue("name"))
    if name == "" {
        name = src.Name + " (copy)"
    }

    // Resolve asset
    assetID := r.FormValue("asset_id")
    if assetID == "" {
        assetID = src.AssetID
    }

    // Validate asset exists; warn but do not block
    assetMissing := false
    if a, _ := db.GetAsset(h.DB, assetID); a == nil {
        assetMissing = true
        assetID = ""
    }

    // Resolve expires_at override
    newExpiry := src.ExpiresAt
    if raw := r.FormValue("expires_at"); raw != "" {
        if t, terr := time.Parse("2006-01-02T15:04", raw); terr == nil {
            newExpiry = &t
        }
    }

    newCampaign := &model.Campaign{
        ID:          uuid.New().String(),
        AccountID:   accountID,
        AssetID:     assetID,
        Name:        name,
        MaxDownloads: src.MaxDownloads,
        ExpiresAt:   newExpiry,
        VisibleWM:   src.VisibleWM,
        InvisibleWM: src.InvisibleWM,
        State:       "DRAFT",
    }

    skipped, err := db.CloneCampaign(h.DB, newCampaign, recipientIDs)
    if err != nil {
        slog.Error("clone campaign", "src", id, "error", err)
        http.Error(w, "Internal error", 500)
        return
    }

    db.InsertAuditLog(h.DB, accountID, "campaign_cloned", "campaign", newCampaign.ID,
        newCampaign.Name, r.RemoteAddr)

    flashMsg := "Campaign cloned successfully."
    if assetMissing {
        flashMsg = "Campaign cloned, but the original asset no longer exists — please select a new asset before publishing."
    } else if skipped > 0 {
        flashMsg = fmt.Sprintf("Campaign cloned. %d recipient(s) were skipped because they no longer exist.", skipped)
    }
    setFlash(w, flashMsg)
    http.Redirect(w, r, "/campaigns/"+newCampaign.ID, http.StatusSeeOther)
}
```

### Route Registration

In `internal/handler/routes.go`, inside the authenticated group, after the existing campaign routes:

```go
r.Post("/campaigns/{id}/clone", h.CampaignClone)
```

### UI: Clone Button and Confirmation Modal

On `templates/campaign_detail.html`, add a "Clone campaign" button in the page header `<div>` alongside the existing Publish button. The button is shown for all campaign states (a clone of an EXPIRED campaign is a valid workflow).

```html
<!-- In the page-header div, after the existing state badge / Publish button -->
<form method="POST" action="/campaigns/{{.Data.Campaign.ID}}/clone" style="display:inline"
      onsubmit="return confirm('Clone \'{{.Data.Campaign.Name}}\'?\n\nThis will create a new draft campaign with the same recipients and settings.\nYou can change the asset or recipients before publishing.')">
  {{.CSRFField}}
  <button type="submit" class="btn btn-secondary">Clone Campaign</button>
</form>
```

The `confirm()` dialog is sufficient for this low-stakes action. No separate modal page is needed. The confirm text reads:

> Clone '[Campaign Name]'?
>
> This will create a new draft campaign with the same recipients and settings. You can change the asset or recipients before publishing.

If a richer modal is desired in a later iteration, a `<dialog>` element with a hidden `<form>` pointing to the same endpoint can replace the `onsubmit` confirm without any backend changes.

### Implementation Milestones

**M1 — Backend (estimated 1 day)**
- Add `CloneCampaign` DB function to `internal/db/queries_campaigns.go`
- Add `CampaignClone` handler to `internal/handler/campaigns.go`
- Register `POST /campaigns/{id}/clone` in `internal/handler/routes.go`
- Add `campaign_cloned` event type to audit log (no schema change needed; `InsertAuditLog` is generic)

**M2 — UI (estimated 0.5 day)**
- Add Clone button + `confirm()` dialog to `templates/campaign_detail.html`
- Manual smoke test: clone DRAFT, clone READY, verify asset-missing warning, verify skipped-recipient flash

---

## Feature B: Export / Copy All Download Links

### Problem

When SMTP is not configured — or when users prefer mail-merge or spreadsheet workflows — there is no bulk export path for download URLs. With 50 recipients, the only option today is to click each token's "Copy" button individually.

### User Story

"I just published a campaign for 50 festival programmers. SMTP is not configured. I want to paste their individual, personalized download links into a mail merge tool or share a CSV with my assistant."

### Export Formats

| Format | Description |
|---|---|
| Plain text (`.txt`) | One line per recipient: `Name <email> → https://host/d/<token>` |
| CSV (`.csv`) | Columns: `name`, `email`, `org`, `download_url`, `token_state`, `download_count` |
| Clipboard (browser) | JS copies the plain-text format without triggering a file download |

### HTTP Endpoint

```
GET /campaigns/{id}/export-links?format=csv
GET /campaigns/{id}/export-links?format=txt
```

**Authentication:** Requires session cookie or Bearer API key (same `RequireAuth` middleware as all campaign routes). Returns `404` if the campaign does not belong to the authenticated account.

**Response headers for file downloads:**

- CSV: `Content-Type: text/csv; charset=utf-8` + `Content-Disposition: attachment; filename="<campaign-name>-links.csv"`
- TXT: `Content-Type: text/plain; charset=utf-8` + `Content-Disposition: attachment; filename="<campaign-name>-links.txt"`

Filenames are sanitized: spaces replaced with hyphens, non-alphanumeric characters (except hyphens) stripped.

**`format` parameter defaults to `txt` if absent or unrecognised.**

### Availability

Export is available when the campaign state is `PROCESSING`, `READY`, or `EXPIRED`. It is NOT available for `DRAFT` campaigns (tokens have no meaningful URLs to share yet — they exist but the campaign is unpublished).

In `PROCESSING` state, some tokens may still be in `PENDING` state (watermarking in progress). Those tokens still receive a URL in the export — the download page will show the "preparing" message to recipients until the watermarked file is ready. This is correct behaviour: the link is live and functional.

Tokens in `EXPIRED` or `CONSUMED` state are included in the export with their state noted — administrators may need a full record.

### CSV Column Specification

```
name,email,org,download_url,token_state,download_count
"Alice Smith","alice@studio.com","Studio A","https://example.com/d/abc123","ACTIVE","2"
"Bob Jones","bob@fest.org","Festival B","https://example.com/d/def456","PENDING","0"
```

All values are quoted. `download_url` always uses `h.Cfg.BaseURL + "/d/" + token.ID` regardless of token state. `org` may be empty string.

### Plain Text Format

```
Alice Smith <alice@studio.com> → https://example.com/d/abc123
Bob Jones <bob@fest.org> → https://example.com/d/def456
```

One line per recipient, sorted by recipient name (same order as the token table). The arrow character (`→`) is U+2192 and renders correctly in all modern terminals and email clients. If ASCII-only output is preferred in a future revision, `->` is the fallback.

### Handler: `CampaignExportLinks`

Add to `internal/handler/campaigns.go`:

```go
func (h *Handler) CampaignExportLinks(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    accountID := auth.AccountFromContext(r.Context())

    campaign, err := db.GetCampaign(h.DB, id)
    if err != nil || campaign == nil || (campaign.AccountID != accountID && !auth.IsAdmin(r.Context())) {
        http.NotFound(w, r)
        return
    }

    // Only available post-publish
    switch campaign.State {
    case "PROCESSING", "READY", "EXPIRED":
        // allowed
    default:
        http.Error(w, "Export is only available after a campaign has been published.", http.StatusBadRequest)
        return
    }

    tokens, err := db.ListTokensByCampaign(h.DB, id)
    if err != nil {
        slog.Error("export-links: list tokens", "error", err)
        http.Error(w, "Internal error", 500)
        return
    }

    format := r.URL.Query().Get("format")
    safeName := sanitizeFilename(campaign.Name)

    switch format {
    case "csv":
        w.Header().Set("Content-Type", "text/csv; charset=utf-8")
        w.Header().Set("Content-Disposition",
            fmt.Sprintf(`attachment; filename="%s-links.csv"`, safeName))
        wr := csv.NewWriter(w)
        wr.Write([]string{"name", "email", "org", "download_url", "token_state", "download_count"})
        for _, t := range tokens {
            wr.Write([]string{
                t.RecipientName,
                t.RecipientEmail,
                t.RecipientOrg,
                h.Cfg.BaseURL + "/d/" + t.ID,
                t.State,
                strconv.Itoa(t.DownloadCount),
            })
        }
        wr.Flush()

    default: // "txt" and fallback
        w.Header().Set("Content-Type", "text/plain; charset=utf-8")
        w.Header().Set("Content-Disposition",
            fmt.Sprintf(`attachment; filename="%s-links.txt"`, safeName))
        for _, t := range tokens {
            fmt.Fprintf(w, "%s <%s> \u2192 %s\n",
                t.RecipientName,
                t.RecipientEmail,
                h.Cfg.BaseURL+"/d/"+t.ID,
            )
        }
    }
}

// sanitizeFilename replaces spaces with hyphens and strips characters
// that are unsafe in Content-Disposition filenames.
func sanitizeFilename(name string) string {
    var b strings.Builder
    for _, r := range strings.ReplaceAll(name, " ", "-") {
        if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
            (r >= '0' && r <= '9') || r == '-' || r == '_' {
            b.WriteRune(r)
        }
    }
    s := b.String()
    if s == "" {
        return "campaign"
    }
    return s
}
```

Note: `encoding/csv` and `strconv` are already used elsewhere in the handler package (`analytics.go`). No new imports at the package level are strictly new.

### Route Registration

In `internal/handler/routes.go`, inside the authenticated group:

```go
r.Get("/campaigns/{id}/export-links", h.CampaignExportLinks)
```

### UI: Export Links Dropdown

On `templates/campaign_detail.html`, add an export control above the "Download Tokens" heading. The control is conditionally rendered only for published states.

```html
{{if or (eq .Data.Campaign.State "READY") (eq .Data.Campaign.State "PROCESSING") (eq .Data.Campaign.State "EXPIRED")}}
<div class="export-bar">
  <span class="export-label">Export links:</span>
  <button class="btn btn-sm btn-secondary" onclick="copyLinksToClipboard()">Copy to clipboard</button>
  <a href="/campaigns/{{.Data.Campaign.ID}}/export-links?format=csv"
     class="btn btn-sm btn-secondary">Download CSV</a>
  <a href="/campaigns/{{.Data.Campaign.ID}}/export-links?format=txt"
     class="btn btn-sm btn-secondary">Download plain text</a>
</div>
{{end}}

<h2>Download Tokens</h2>
```

The "Copy to clipboard" button uses a JS function that fetches the plain-text export and writes it to the clipboard:

```html
<script>
async function copyLinksToClipboard() {
  try {
    const resp = await fetch('/campaigns/{{.Data.Campaign.ID}}/export-links?format=txt');
    if (!resp.ok) throw new Error('Export failed');
    const text = await resp.text();
    await navigator.clipboard.writeText(text);
    // Reuse existing copyLink pattern for transient feedback
    const btn = document.querySelector('[onclick="copyLinksToClipboard()"]');
    const orig = btn.textContent;
    btn.textContent = 'Copied!';
    setTimeout(() => { btn.textContent = orig; }, 2000);
  } catch (err) {
    alert('Copy failed: ' + err.message);
  }
}
</script>
```

This approach reuses the existing `GET /campaigns/{id}/export-links?format=txt` endpoint rather than embedding the link data in the HTML, keeping the template simple and the data consistent.

`navigator.clipboard.writeText` requires a secure context (HTTPS or localhost). Production deployments behind the provided Caddy reverse proxy satisfy this requirement. A fallback `alert` is shown on failure.

### Security Considerations

- The export endpoint is protected by `RequireAuth` and account-ownership checks — the same guards as `CampaignDetail`.
- Exported download URLs are live, active tokens. The export file should be treated with the same confidentiality as an email containing those links. No additional in-application controls are needed (the operator is responsible for handling the exported file appropriately).
- The `Content-Disposition: attachment` header prevents browsers from rendering the file inline, reducing accidental exposure in shared screens.
- No rate limiting beyond the session is applied. If abuse is a concern, the existing `RateLimiter` middleware could be applied to this route, but this is considered out of scope for this iteration.

### Implementation Milestones

**M1 — Backend (estimated 0.5 day)**
- Add `sanitizeFilename` helper to `internal/handler/campaigns.go`
- Add `CampaignExportLinks` handler to `internal/handler/campaigns.go`
- Register `GET /campaigns/{id}/export-links` in `internal/handler/routes.go`

**M2 — UI (estimated 0.5 day)**
- Add export bar HTML block and `copyLinksToClipboard` JS to `templates/campaign_detail.html`
- Manual smoke test: CSV download, TXT download, clipboard copy (HTTPS context), DRAFT campaign returns 400

---

## Combined Notes

### No Schema Changes

Both features work entirely with the existing database schema. No new migration files are needed.

### Shared Imports

`CampaignClone` requires `fmt` (already imported in `campaigns.go` via the existing `fmt.Sprintf` usage in `CampaignCreate`'s inline error path — confirm at implementation time). `CampaignExportLinks` requires `encoding/csv` and `strconv`, which are already used in `analytics.go` in the same package.

`CloneCampaign` in `queries_campaigns.go` requires `github.com/google/uuid` — add this import to the `db` package file. Alternatively, generate the UUID in the handler (as is done in `CampaignCreate`) and pass the pre-generated ID into `CloneCampaign`. The latter is consistent with existing patterns and is preferred.

### Audit Log Events

| Action | `action` value | `resource_type` | Notes |
|---|---|---|---|
| Clone campaign | `campaign_cloned` | `campaign` | `resource_id` = new campaign ID |
| Export links | (not logged) | — | Read-only; not a state change |

Export is a read-only operation and does not warrant an audit log entry. If compliance requirements change, add `campaign_links_exported` with the format and recipient count.

### Estimated Total Effort

| Feature | Backend | UI | Total |
|---|---|---|---|
| Campaign clone | 1 day | 0.5 day | 1.5 days |
| Export links | 0.5 day | 0.5 day | 1 day |
| **Combined** | | | **2.5 days** |

Both features can be developed in parallel by separate contributors with no conflicts, as they touch different handler methods and different sections of `campaign_detail.html`.
