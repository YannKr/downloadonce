package db

import (
	"database/sql"
	"time"

	"github.com/YannKr/downloadonce/internal/model"
)

func CreateCampaign(database *sql.DB, c *model.Campaign) error {
	var expiresAt *string
	if c.ExpiresAt != nil {
		s := c.ExpiresAt.UTC().Format(time.RFC3339)
		expiresAt = &s
	}
	_, err := database.Exec(
		`INSERT INTO campaigns (id, account_id, asset_id, name, max_downloads, expires_at, visible_wm, invisible_wm, state)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.AccountID, c.AssetID, c.Name, c.MaxDownloads, expiresAt,
		boolToInt(c.VisibleWM), boolToInt(c.InvisibleWM), c.State,
	)
	return err
}

func GetCampaign(database *sql.DB, id string) (*model.Campaign, error) {
	c := &model.Campaign{}
	var visibleWM, invisibleWM int
	var expiresAt, publishedAt *string
	var createdAt SQLiteTime
	err := database.QueryRow(
		`SELECT id, account_id, asset_id, name, max_downloads, expires_at,
		  visible_wm, invisible_wm, state, created_at, published_at
		 FROM campaigns WHERE id = ?`, id,
	).Scan(&c.ID, &c.AccountID, &c.AssetID, &c.Name, &c.MaxDownloads, &expiresAt,
		&visibleWM, &invisibleWM, &c.State, &createdAt, &publishedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.CreatedAt = createdAt.Time
	c.VisibleWM = visibleWM != 0
	c.InvisibleWM = invisibleWM != 0
	if expiresAt != nil {
		t, _ := time.Parse(time.RFC3339, *expiresAt)
		c.ExpiresAt = &t
	}
	if publishedAt != nil {
		t, _ := time.Parse(time.RFC3339, *publishedAt)
		c.PublishedAt = &t
	}
	return c, nil
}

func ListCampaigns(database *sql.DB, accountID string, showAll bool) ([]model.CampaignSummary, error) {
	query := `
		SELECT c.id, c.account_id, c.asset_id, c.name, c.max_downloads, c.expires_at,
		  c.visible_wm, c.invisible_wm, c.state, c.created_at, c.published_at,
		  a.title AS asset_name, a.asset_type,
		  (SELECT COUNT(*) FROM download_tokens WHERE campaign_id = c.id) AS recipient_count,
		  (SELECT COUNT(DISTINCT de.token_id) FROM download_events de
		    JOIN download_tokens dt ON dt.id = de.token_id WHERE dt.campaign_id = c.id) AS downloaded_count,
		  (SELECT COUNT(*) FROM jobs WHERE campaign_id = c.id) AS jobs_total,
		  (SELECT COUNT(*) FROM jobs WHERE campaign_id = c.id AND state = 'COMPLETED') AS jobs_completed,
		  (SELECT COUNT(*) FROM jobs WHERE campaign_id = c.id AND state = 'FAILED') AS jobs_failed,
		  acc.name AS creator_name
		FROM campaigns c
		JOIN assets a ON a.id = c.asset_id
		JOIN accounts acc ON acc.id = c.account_id`

	var rows *sql.Rows
	var err error
	if showAll {
		query += ` ORDER BY c.created_at DESC`
		rows, err = database.Query(query)
	} else {
		query += ` WHERE c.account_id = ? ORDER BY c.created_at DESC`
		rows, err = database.Query(query, accountID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var campaigns []model.CampaignSummary
	for rows.Next() {
		var cs model.CampaignSummary
		var visibleWM, invisibleWM int
		var expiresAt, publishedAt *string
		var createdAt SQLiteTime
		err := rows.Scan(
			&cs.ID, &cs.AccountID, &cs.AssetID, &cs.Name, &cs.MaxDownloads, &expiresAt,
			&visibleWM, &invisibleWM, &cs.State, &createdAt, &publishedAt,
			&cs.AssetName, &cs.AssetType,
			&cs.RecipientCount, &cs.DownloadedCount,
			&cs.JobsTotal, &cs.JobsCompleted, &cs.JobsFailed,
			&cs.CreatorName,
		)
		if err != nil {
			return nil, err
		}
		cs.CreatedAt = createdAt.Time
		cs.VisibleWM = visibleWM != 0
		cs.InvisibleWM = invisibleWM != 0
		if expiresAt != nil {
			t, _ := time.Parse(time.RFC3339, *expiresAt)
			cs.ExpiresAt = &t
		}
		if publishedAt != nil {
			t, _ := time.Parse(time.RFC3339, *publishedAt)
			cs.PublishedAt = &t
		}
		campaigns = append(campaigns, cs)
	}
	return campaigns, rows.Err()
}

func UpdateCampaignState(database *sql.DB, id, state string) error {
	_, err := database.Exec(`UPDATE campaigns SET state = ? WHERE id = ?`, state, id)
	return err
}

func SetCampaignPublished(database *sql.DB, id string) error {
	_, err := database.Exec(
		`UPDATE campaigns SET state = 'PROCESSING', published_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		id,
	)
	return err
}

func SetCampaignPublishedReady(database *sql.DB, id string) error {
	_, err := database.Exec(
		`UPDATE campaigns SET state = 'READY', published_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`,
		id,
	)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func ListExpiredCampaigns(database *sql.DB) ([]model.Campaign, error) {
	rows, err := database.Query(`
		SELECT id, account_id, asset_id, name, max_downloads, expires_at,
		  visible_wm, invisible_wm, state, created_at, published_at
		FROM campaigns
		WHERE expires_at IS NOT NULL
		  AND expires_at < strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		  AND state != 'EXPIRED'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var campaigns []model.Campaign
	for rows.Next() {
		var c model.Campaign
		var visibleWM, invisibleWM int
		var expiresAt, publishedAt *string
		var createdAt SQLiteTime
		if err := rows.Scan(&c.ID, &c.AccountID, &c.AssetID, &c.Name, &c.MaxDownloads, &expiresAt,
			&visibleWM, &invisibleWM, &c.State, &createdAt, &publishedAt); err != nil {
			return nil, err
		}
		c.CreatedAt = createdAt.Time
		c.VisibleWM = visibleWM != 0
		c.InvisibleWM = invisibleWM != 0
		if expiresAt != nil {
			t, _ := time.Parse(time.RFC3339, *expiresAt)
			c.ExpiresAt = &t
		}
		if publishedAt != nil {
			t, _ := time.Parse(time.RFC3339, *publishedAt)
			c.PublishedAt = &t
		}
		campaigns = append(campaigns, c)
	}
	return campaigns, rows.Err()
}

func ExpireCampaignAndTokens(database *sql.DB, campaignID string) error {
	_, err := database.Exec(`UPDATE campaigns SET state = 'EXPIRED' WHERE id = ?`, campaignID)
	if err != nil {
		return err
	}
	_, err = database.Exec(`UPDATE download_tokens SET state = 'EXPIRED' WHERE campaign_id = ? AND state IN ('PENDING', 'ACTIVE')`, campaignID)
	return err
}
