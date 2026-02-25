# Feature Spec 02: Recipient Groups / Contact Lists

**Status:** Draft
**Date:** 2026-02-23
**Author:** Engineering

---

## Table of Contents

1. [Problem Statement & User Story](#1-problem-statement--user-story)
2. [Goals & Non-Goals](#2-goals--non-goals)
3. [Data Model](#3-data-model)
4. [UI/UX Design](#4-uiux-design)
5. [API Routes](#5-api-routes)
6. [Campaign Integration](#6-campaign-integration)
7. [DB Queries](#7-db-queries)
8. [Go Structs](#8-go-structs)
9. [Edge Cases](#9-edge-cases)
10. [Implementation Milestones](#10-implementation-milestones)

---

## 1. Problem Statement & User Story

### The Pain

A filmmaker sends screeners to the same 50 festival programmers every year — Sundance, SXSW, Tribeca, Toronto, Cannes, and so on. Each time a new film is ready for distribution, they open DownloadOnce and manually re-add every programmer by typing or pasting their email address into the campaign form, one by one. There is no concept of a saved list. Even with the CSV bulk import, they must locate the CSV file each time, re-upload it, and verify the entries were not already in the system.

A food photographer distributes RAW files to the same roster of 12 editorial clients: magazine art directors and agency buyers. Every new shoot means re-selecting those same 12 people from the global recipient list.

This is the most common workflow in the product — same sender, same audience, new content — and it has no shortcut.

### User Stories

**Primary persona: Maya, independent filmmaker**
> "I send screeners to the same 70 programmers at the start of every festival cycle. I want to save that list once, name it 'Festival Circuit 2026', and just pick it when I create a new campaign. I should never have to type those emails again."

**Secondary persona: Daniel, commercial photographer**
> "I have three client groups: Agency Buyers (8 people), Magazine ADs (12 people), and Press (5 people). When I deliver a shoot, I pick one or more groups and I'm done. I don't want to scroll through my full 40-person contact list every time."

**Tertiary persona: Studio distributor managing multiple filmmakers**
> "I need to see which groups exist, who's in them, and be able to add a new programmer to 'Festival Circuit' without touching the campaign that already went out."

---

## 2. Goals & Non-Goals

### Goals

- Allow users to create named recipient groups (contact lists) associated with their account.
- Allow adding and removing individual recipients from a group.
- Allow importing recipients from a CSV directly into a group.
- Show group membership on the recipient list page.
- On campaign creation, let the user pick one or more groups to bulk-add all their members as recipients, with real-time count preview.
- Deduplicate recipients across multiple selected groups before creating tokens.
- Full CRUD for groups, accessible only to the owning account (or admin).

### Non-Goals

- Groups are not access-control or permission scoping. They are purely a UI convenience for bulk-selection.
- A campaign does not "belong" to a group. Tokens belong to individual recipients. Once a campaign is created, there is no ongoing link between the campaign and the group that was used to populate it.
- No nesting of groups (a group of groups).
- No sharing of groups between accounts (recipients are already global; groups are per-account).
- No group-level analytics or reporting.
- No API endpoint for group management in this milestone (API extension is a future spec).

---

## 3. Data Model

### 3.1 New Tables

#### `recipient_groups`

Stores named lists owned by an account.

```sql
CREATE TABLE recipient_groups (
    id          TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(account_id, name)
);
CREATE INDEX IF NOT EXISTS idx_recipient_groups_account ON recipient_groups(account_id);
```

**Notes:**
- `id`: UUID v4, generated in Go before insert.
- `name`: must be unique per account (not globally). The UNIQUE constraint is `(account_id, name)`.
- `description`: optional free-text label (e.g., "Sundance + SXSW programmers, updated Feb 2026").

#### `recipient_group_members`

Many-to-many join between groups and recipients.

```sql
CREATE TABLE recipient_group_members (
    group_id     TEXT NOT NULL REFERENCES recipient_groups(id) ON DELETE CASCADE,
    recipient_id TEXT NOT NULL REFERENCES recipients(id) ON DELETE CASCADE,
    added_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (group_id, recipient_id)
);
CREATE INDEX IF NOT EXISTS idx_rgm_recipient ON recipient_group_members(recipient_id);
```

**Notes:**
- Composite primary key `(group_id, recipient_id)` enforces uniqueness.
- `ON DELETE CASCADE` on `group_id`: deleting a group removes all its memberships automatically.
- `ON DELETE CASCADE` on `recipient_id`: deleting a recipient removes their membership from all groups automatically. Existing campaign tokens are unaffected.
- The `idx_rgm_recipient` index enables efficient "which groups does this recipient belong to?" lookups.

### 3.2 Migration File

File: `migrations/006_recipient_groups.sql`

```sql
-- Recipient groups: named contact lists per account
CREATE TABLE IF NOT EXISTS recipient_groups (
    id          TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(account_id, name)
);
CREATE INDEX IF NOT EXISTS idx_recipient_groups_account ON recipient_groups(account_id);

-- Many-to-many: which recipients are in which group
CREATE TABLE IF NOT EXISTS recipient_group_members (
    group_id     TEXT NOT NULL REFERENCES recipient_groups(id) ON DELETE CASCADE,
    recipient_id TEXT NOT NULL REFERENCES recipients(id) ON DELETE CASCADE,
    added_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (group_id, recipient_id)
);
CREATE INDEX IF NOT EXISTS idx_rgm_recipient ON recipient_group_members(recipient_id);
```

This file is placed alongside the existing `001_` through `005_` files. `db.Migrate()` applies it automatically on the next server start. Existing migration files must not be modified.

---

## 4. UI/UX Design

### 4.1 Groups Management Page — `GET /recipients/groups`

**Layout:** Standard authenticated page, nav active on "Recipients".

**Content:**
- Page heading: "Recipient Groups"
- Button: "New Group" linking to `/recipients/groups/new`
- Table of existing groups for the current account, columns:
  - **Name** (links to `/recipients/groups/{id}`)
  - **Description** (truncated to ~60 chars with ellipsis if longer)
  - **Members** (integer count, e.g., "47 members")
  - **Created** (formatted date)
  - **Actions**: delete button (POST to `/recipients/groups/{id}/delete`, confirmation dialog)
- Empty state: "No groups yet. Create one to save a list of recipients for reuse across campaigns."
- Tab strip on the Recipients section linking between "All Recipients" and "Groups".

### 4.2 New/Edit Group Form — `GET /recipients/groups/new` + `POST /recipients/groups/new`

**Fields:**
- **Name** (required, text input, max 100 chars). Placeholder: "e.g. Festival Circuit 2026"
- **Description** (optional, textarea, max 500 chars). Placeholder: "Optional notes about this group."
- Submit: "Create Group"
- Cancel: link back to `/recipients/groups`

**Validation:**
- Name is required and must be non-empty after trimming whitespace.
- If a group with the same name already exists for this account, return a form error: "A group named '[name]' already exists."

**After creation:** redirect to `/recipients/groups/{id}` so the user can immediately add members.

### 4.3 Group Detail Page — `GET /recipients/groups/{id}`

**Content:**
- Heading: group name, with description below it in muted text.
- Inline edit form (collapsed by default, toggled by "Edit" button): fields for name and description, submit to `POST /recipients/groups/{id}/edit`.
- **Members section:**
  - Count badge: "N members"
  - Table of current members: Name, Email, Organization, Added date, Remove button
  - Empty state: "No members yet. Add recipients below."
- **Add members section:**
  - Multi-select or searchable checkbox list of all recipients not already in this group.
  - Search/filter input above the list (client-side JavaScript filter on name/email text).
  - "Add Selected" button submits a POST to `/recipients/groups/{id}/add-members` with a list of selected `recipient_ids`.
- **Import to group section:**
  - Textarea for CSV (`Name, Email, Org`), same format as the existing bulk import.
  - Submitting via `POST /recipients/groups/{id}/import` creates any new recipients that don't exist, then adds all of them to this group.
  - Flash: "N added to group, M already members, K new recipients created."

### 4.4 Recipient List Page Enhancements — `GET /recipients`

The existing `recipients.html` table gains a **Groups** column. Each row shows small pill badges for every group this recipient belongs to, linking to the group detail page.

The page also gains a "Manage Groups" link in the page header area.

---

## 5. API Routes

All routes are under the existing `RequireAuth` middleware group.

### New routes to add to `internal/handler/routes.go`

```go
r.Get("/recipients/groups",                                      h.GroupList)
r.Get("/recipients/groups/new",                                  h.GroupNewForm)
r.Post("/recipients/groups/new",                                 h.GroupCreate)
r.Get("/recipients/groups/{id}",                                 h.GroupDetail)
r.Post("/recipients/groups/{id}/edit",                           h.GroupEdit)
r.Post("/recipients/groups/{id}/add-members",                    h.GroupAddMembers)
r.Post("/recipients/groups/{id}/members/{recipientID}/remove",   h.GroupRemoveMember)
r.Post("/recipients/groups/{id}/import",                         h.GroupImport)
r.Post("/recipients/groups/{id}/delete",                         h.GroupDelete)
```

**Route ordering note:** The new group routes must be registered before `r.Post("/recipients/{id}/delete", ...)` to prevent chi from treating `groups` as a `{id}` parameter value. In chi, static path segments take precedence over named parameters.

### Route Summary Table

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| GET | `/recipients/groups` | `GroupList` | List all groups for the current account |
| GET | `/recipients/groups/new` | `GroupNewForm` | Render new group form |
| POST | `/recipients/groups/new` | `GroupCreate` | Create a group, redirect to detail |
| GET | `/recipients/groups/{id}` | `GroupDetail` | Show group members and add-member form |
| POST | `/recipients/groups/{id}/edit` | `GroupEdit` | Update group name/description |
| POST | `/recipients/groups/{id}/add-members` | `GroupAddMembers` | Add one or more recipients to group |
| POST | `/recipients/groups/{id}/members/{recipientID}/remove` | `GroupRemoveMember` | Remove one recipient from group |
| POST | `/recipients/groups/{id}/import` | `GroupImport` | Bulk-import CSV, create recipients, add to group |
| POST | `/recipients/groups/{id}/delete` | `GroupDelete` | Delete group (members unaffected) |

### Authorization

All group routes check that the group's `account_id` matches the authenticated session's account ID, unless the user is an admin. A 404 is returned for mismatches (same pattern as campaigns and recipients).

---

## 6. Campaign Integration

### 6.1 Form Change: "Add from Group"

The `campaign_new.html` template and `campaignNewData` struct gain a Groups section above the individual recipient checkboxes.

The group dropdown is a `<select multiple>` element with `name="group_ids"`. Each `<option>` carries a `data-count` attribute with the member count, and a small inline script updates a preview count as the user selects/deselects groups. Full deduplication happens server-side at submission.

### 6.2 `campaignNewData` Struct Changes

```go
type campaignNewData struct {
    Assets         []model.Asset
    Recipients     []model.Recipient
    Groups         []model.RecipientGroupSummary  // new
    Name           string
    AssetID        string
    MaxDownloads   string
    ExpiresAt      string
    SelectedIDs    map[string]bool
    SelectedGroups map[string]bool                // new
    VisibleWM      bool
    InvisibleWM    bool
}
```

`CampaignNewForm` fetches groups: `db.ListGroupsForAccount(h.DB, accountID)`.

### 6.3 Server-Side Deduplication on Campaign Create

In `CampaignCreate`, after collecting `recipientIDs` and `groupIDs` from the form, the handler expands group IDs to recipient IDs and deduplicates:

```go
seen := make(map[string]struct{})
finalIDs := make([]string, 0)

for _, rid := range recipientIDs {
    if _, ok := seen[rid]; !ok {
        seen[rid] = struct{}{}
        finalIDs = append(finalIDs, rid)
    }
}

for _, gid := range groupIDs {
    // Verify the group belongs to the current account before expanding.
    members, err := db.ListGroupMemberIDs(h.DB, gid, accountID)
    if err != nil {
        continue
    }
    for _, rid := range members {
        if _, ok := seen[rid]; !ok {
            seen[rid] = struct{}{}
            finalIDs = append(finalIDs, rid)
        }
    }
}
```

`db.ListGroupMemberIDs` joins `recipient_group_members` against `recipient_groups` and filters by `account_id` to prevent cross-account group expansion.

### 6.4 Validation

The existing check `len(recipientIDs) == 0` changes to `len(finalIDs) == 0`. The error message becomes: "Select at least one recipient or group."

---

## 7. DB Queries

New file: `internal/db/queries_groups.go`

### List groups for an account (with member count)

```sql
SELECT
    g.id, g.account_id, g.name, g.description, g.created_at,
    COUNT(m.recipient_id) AS member_count
FROM recipient_groups g
LEFT JOIN recipient_group_members m ON m.group_id = g.id
WHERE g.account_id = ?
GROUP BY g.id
ORDER BY g.name ASC;
```

### Get a single group

```sql
SELECT id, account_id, name, description, created_at
FROM recipient_groups
WHERE id = ?;
```

### Create a group

```sql
INSERT INTO recipient_groups (id, account_id, name, description)
VALUES (?, ?, ?, ?);
```

### Update a group

```sql
UPDATE recipient_groups
SET name = ?, description = ?
WHERE id = ? AND account_id = ?;
```

### Delete a group

```sql
DELETE FROM recipient_groups WHERE id = ? AND account_id = ?;
```

Cascade on `recipient_group_members` cleans up memberships automatically.

### List members of a group

```sql
SELECT r.id, r.account_id, r.name, r.email, r.org, r.created_at, m.added_at
FROM recipient_group_members m
JOIN recipients r ON r.id = m.recipient_id
WHERE m.group_id = ?
ORDER BY r.name ASC;
```

### Add a member (idempotent)

```sql
INSERT OR IGNORE INTO recipient_group_members (group_id, recipient_id)
VALUES (?, ?);
```

Called once per recipient ID. `INSERT OR IGNORE` silently skips existing memberships.

### Remove a member

```sql
DELETE FROM recipient_group_members
WHERE group_id = ? AND recipient_id = ?;
```

### List recipients not in a group (for the add-member picker)

```sql
SELECT r.id, r.name, r.email, r.org
FROM recipients r
WHERE r.id NOT IN (
    SELECT recipient_id FROM recipient_group_members WHERE group_id = ?
)
ORDER BY r.name ASC;
```

### Fetch member IDs for campaign expansion (with account guard)

```sql
SELECT m.recipient_id
FROM recipient_group_members m
JOIN recipient_groups g ON g.id = m.group_id
WHERE m.group_id = ? AND g.account_id = ?;
```

### List groups a recipient belongs to (for badge rendering)

```sql
SELECT g.id, g.name
FROM recipient_group_members m
JOIN recipient_groups g ON g.id = m.group_id
WHERE m.recipient_id = ?
ORDER BY g.name ASC;
```

### Recipient list with group badges (aggregate query)

```sql
SELECT
    r.id, r.account_id, r.name, r.email, r.org, r.created_at,
    GROUP_CONCAT(g.id || '|' || g.name, '||') AS groups
FROM recipients r
LEFT JOIN recipient_group_members m ON m.recipient_id = r.id
LEFT JOIN recipient_groups g ON g.id = m.group_id
GROUP BY r.id
ORDER BY r.name ASC;
```

The `groups` column is a `||`-delimited string. The Go scanner splits on `||`, then on `|`, to produce `[]GroupBadge`.

---

## 8. Go Structs

Add to `internal/model/model.go`:

```go
// RecipientGroup is a named contact list owned by an account.
type RecipientGroup struct {
    ID          string
    AccountID   string
    Name        string
    Description string
    CreatedAt   time.Time
}

// RecipientGroupSummary is used in list views where the member count
// is needed but individual members are not fetched.
type RecipientGroupSummary struct {
    RecipientGroup
    MemberCount int
}

// RecipientGroupMember joins a Recipient with the timestamp it was
// added to a specific group.
type RecipientGroupMember struct {
    Recipient
    AddedAt time.Time
}

// GroupBadge is a minimal struct for rendering group membership pills
// on the recipient list page.
type GroupBadge struct {
    ID   string
    Name string
}

// RecipientWithGroups extends Recipient with the groups it belongs to,
// used on the enhanced recipient list page.
type RecipientWithGroups struct {
    Recipient
    Groups []GroupBadge
}
```

New handler page-data structs (in `internal/handler/groups.go`):

```go
type groupListData struct {
    Groups []model.RecipientGroupSummary
}

type groupDetailData struct {
    Group      model.RecipientGroup
    Members    []model.RecipientGroupMember
    NonMembers []model.Recipient
}
```

---

## 9. Edge Cases

**Group deleted after campaign is created from it:** No problem. Tokens reference `recipients.id` directly. The group and its memberships are gone, but the tokens, watermarked files, and audit trail are unaffected.

**Recipient deleted after being added to a group:** `ON DELETE CASCADE` on `recipient_group_members.recipient_id` removes the membership. Existing campaign tokens for that recipient remain.

**Same recipient in two selected groups on campaign create:** Server-side `seen` map ensures only one token is created per unique recipient ID.

**Recipient in a group and also directly checked on the form:** Same deduplication. Direct selections populate `seen` first; group expansion skips already-seen IDs.

**Group with zero members:** Allowed. Selecting an empty group contributes zero recipients. If no other recipients are selected, the existing "at least one recipient" validation error fires.

**Duplicate group name within an account:** Caught by `UNIQUE(account_id, name)`. Handler returns: "A group named 'X' already exists."

**Bulk import creates a recipient that already exists globally:** `GetOrCreateRecipientByEmail` returns the existing recipient. `INSERT OR IGNORE` adds them to the group. No duplicate is created.

**Very large groups (thousands of members):** `ListGroupMemberIDs` fetches all IDs in one query (indexed on `group_id`). Token creation is already a loop in `CampaignCreate`. No architectural change needed.

---

## 10. Implementation Milestones

### M1 — Schema + Backend CRUD

**Goal:** Groups can be created, edited, deleted. Members can be added and removed. Recipient list shows group badges.

**Deliverables:**
1. `migrations/006_recipient_groups.sql` with the two new tables.
2. `internal/db/queries_groups.go` with all queries from Section 7.
3. New model structs in `internal/model/model.go` (Section 8).
4. `internal/handler/groups.go` with handlers: `GroupList`, `GroupNewForm`, `GroupCreate`, `GroupDetail`, `GroupEdit`, `GroupAddMembers`, `GroupRemoveMember`, `GroupImport`, `GroupDelete`.
5. New routes registered in `internal/handler/routes.go` before the existing `recipients/{id}/delete` route.
6. New templates: `templates/recipient_groups.html`, `templates/recipient_group_detail.html`.
7. Updated `templates/recipients.html` with Groups column and "Manage Groups" link.
8. Audit log entries: `group_created`, `group_deleted`, `group_member_added`, `group_member_removed`.

### M2 — Campaign Form Integration

**Goal:** Campaign creation form supports group selection for bulk recipient add.

**Deliverables:**
1. `campaignNewData` updated with `Groups` and `SelectedGroups` fields.
2. `CampaignNewForm` fetches groups via `db.ListGroupsForAccount`.
3. `CampaignCreate` reads `group_ids`, expands via `db.ListGroupMemberIDs`, deduplicates with `seen` map.
4. Validation updated: `len(finalIDs) == 0` triggers error.
5. `templates/campaign_new.html` updated with group multi-select and count preview script.

### M3 — Group Import from CSV

**Goal:** Users can paste a CSV into a group's import form, creating new recipients as needed.

**Deliverables:**
1. `GroupImport` handler: parse CSV, `GetOrCreateRecipientByEmail`, `CreateRecipient` if new, `AddGroupMember` for all.
2. Flash message: "N added to group, M already members, K new recipients created."
3. Import form section added to `templates/recipient_group_detail.html`.
4. Audit log entry: `group_import` with counts in the detail field.

---

## Appendix: File Checklist

| File | Action | Notes |
|------|--------|-------|
| `migrations/006_recipient_groups.sql` | Create | New migration; do not modify after deploy |
| `internal/model/model.go` | Edit | Add 5 new structs |
| `internal/db/queries_groups.go` | Create | All group/member SQL queries |
| `internal/handler/groups.go` | Create | All group HTTP handlers |
| `internal/handler/campaigns.go` | Edit | Expand `groupIDs`, update `campaignNewData` |
| `internal/handler/recipients.go` | Edit | Update `recipientPageData`, `RecipientList` |
| `internal/handler/routes.go` | Edit | Register 9 new routes |
| `templates/recipient_groups.html` | Create | Group list page |
| `templates/recipient_group_detail.html` | Create | Group detail + member management |
| `templates/recipients.html` | Edit | Add Groups column, "Manage Groups" link |
| `templates/campaign_new.html` | Edit | Add group multi-select with count preview |
