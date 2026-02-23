package db

import (
	"database/sql"
	"time"

	"github.com/ypk/downloadonce/internal/model"
)

func CreateToken(database *sql.DB, t *model.DownloadToken) error {
	var expiresAt *string
	if t.ExpiresAt != nil {
		s := t.ExpiresAt.UTC().Format(time.RFC3339)
		expiresAt = &s
	}
	_, err := database.Exec(
		`INSERT INTO download_tokens (id, campaign_id, recipient_id, max_downloads, state, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, t.CampaignID, t.RecipientID, t.MaxDownloads, t.State, expiresAt,
	)
	return err
}

func GetToken(database *sql.DB, id string) (*model.DownloadToken, error) {
	t := &model.DownloadToken{}
	var expiresAt *string
	var createdAt SQLiteTime
	err := database.QueryRow(
		`SELECT id, campaign_id, recipient_id, max_downloads, download_count, state,
		  watermarked_path, watermark_payload, sha256_output, output_size_bytes, expires_at, created_at
		 FROM download_tokens WHERE id = ?`, id,
	).Scan(&t.ID, &t.CampaignID, &t.RecipientID, &t.MaxDownloads, &t.DownloadCount,
		&t.State, &t.WatermarkedPath, &t.WatermarkPayload, &t.SHA256Output,
		&t.OutputSizeBytes, &expiresAt, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt = createdAt.Time
	if expiresAt != nil {
		pt, _ := time.Parse(time.RFC3339, *expiresAt)
		t.ExpiresAt = &pt
	}
	return t, nil
}

func ListTokensByCampaign(database *sql.DB, campaignID string) ([]model.TokenWithRecipient, error) {
	rows, err := database.Query(`
		SELECT t.id, t.campaign_id, t.recipient_id, t.max_downloads, t.download_count,
		  t.state, t.watermarked_path, t.sha256_output, t.output_size_bytes, t.expires_at, t.created_at,
		  r.name, r.email, r.org,
		  (SELECT MAX(de.downloaded_at) FROM download_events de WHERE de.token_id = t.id) AS last_download
		FROM download_tokens t
		JOIN recipients r ON r.id = t.recipient_id
		WHERE t.campaign_id = ?
		ORDER BY r.name ASC`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []model.TokenWithRecipient
	for rows.Next() {
		var tw model.TokenWithRecipient
		var expiresAt, lastDL *string
		var createdAt SQLiteTime
		err := rows.Scan(
			&tw.ID, &tw.CampaignID, &tw.RecipientID, &tw.MaxDownloads, &tw.DownloadCount,
			&tw.State, &tw.WatermarkedPath, &tw.SHA256Output, &tw.OutputSizeBytes,
			&expiresAt, &createdAt,
			&tw.RecipientName, &tw.RecipientEmail, &tw.RecipientOrg,
			&lastDL,
		)
		if err != nil {
			return nil, err
		}
		tw.CreatedAt = createdAt.Time
		if expiresAt != nil {
			t, _ := time.Parse(time.RFC3339, *expiresAt)
			tw.ExpiresAt = &t
		}
		if lastDL != nil {
			t, _ := time.Parse(time.RFC3339, *lastDL)
			tw.LastDownloadAt = &t
		}
		tokens = append(tokens, tw)
	}
	return tokens, rows.Err()
}

func ActivateToken(database *sql.DB, id, watermarkedPath, sha256 string, sizeBytes int64) error {
	_, err := database.Exec(
		`UPDATE download_tokens SET state = 'ACTIVE', watermarked_path = ?, sha256_output = ?, output_size_bytes = ?
		 WHERE id = ?`,
		watermarkedPath, sha256, sizeBytes, id,
	)
	return err
}

func IncrementDownloadCount(database *sql.DB, tokenID string) (newCount int, consumed bool, err error) {
	err = database.QueryRow(`
		UPDATE download_tokens
		SET download_count = download_count + 1,
		    state = CASE
		        WHEN max_downloads IS NOT NULL AND download_count + 1 >= max_downloads THEN 'CONSUMED'
		        ELSE state
		    END
		WHERE id = ? AND state = 'ACTIVE'
		RETURNING download_count, (max_downloads IS NOT NULL AND download_count >= max_downloads)`,
		tokenID,
	).Scan(&newCount, &consumed)
	return
}

func ExpireToken(database *sql.DB, id string) error {
	_, err := database.Exec(`UPDATE download_tokens SET state = 'EXPIRED' WHERE id = ?`, id)
	return err
}
