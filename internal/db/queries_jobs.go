package db

import (
	"database/sql"

	"github.com/YannKr/downloadonce/internal/model"
)

func EnqueueJob(database *sql.DB, j *model.Job) error {
	_, err := database.Exec(
		`INSERT INTO jobs (id, job_type, campaign_id, token_id, state) VALUES (?, ?, ?, ?, 'PENDING')`,
		j.ID, j.JobType, j.CampaignID, j.TokenID,
	)
	return err
}

func EnqueueDetectJob(database *sql.DB, id, accountID, inputPath, jobType string) error {
	_, err := database.Exec(
		`INSERT INTO jobs (id, job_type, campaign_id, token_id, state, input_path)
		 VALUES (?, ?, ?, ?, 'PENDING', ?)`,
		id, jobType, accountID, "", inputPath,
	)
	return err
}

func ClaimNextJob(database *sql.DB, jobTypes []string) (*model.Job, error) {
	if len(jobTypes) == 0 {
		return nil, nil
	}

	// Build placeholder string for IN clause
	query := `
		UPDATE jobs
		SET state = 'RUNNING', started_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		WHERE id = (
			SELECT id FROM jobs
			WHERE state = 'PENDING' AND job_type IN (`

	args := make([]interface{}, len(jobTypes))
	for i, jt := range jobTypes {
		if i > 0 {
			query += ","
		}
		query += "?"
		args[i] = jt
	}
	query += `) ORDER BY created_at ASC LIMIT 1
		)
		RETURNING id, job_type, campaign_id, token_id, state, progress,
		          COALESCE(input_path, ''), COALESCE(result_data, ''),
		          created_at, started_at`

	j := &model.Job{}
	var createdAt, startedAt SQLiteTime
	err := database.QueryRow(query, args...).Scan(
		&j.ID, &j.JobType, &j.CampaignID, &j.TokenID,
		&j.State, &j.Progress, &j.InputPath, &j.ResultData,
		&createdAt, &startedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.CreatedAt = createdAt.Time
	j.StartedAt = &startedAt.Time
	return j, nil
}

func CompleteJob(database *sql.DB, id string) error {
	_, err := database.Exec(
		`UPDATE jobs SET state = 'COMPLETED', progress = 100, completed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE id = ?`, id,
	)
	return err
}

func FailJob(database *sql.DB, id, errorMsg string) error {
	_, err := database.Exec(
		`UPDATE jobs SET state = 'FAILED', error_message = ?, completed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE id = ?`, errorMsg, id,
	)
	return err
}

func UpdateJobProgress(database *sql.DB, id string, progress int) error {
	_, err := database.Exec(`UPDATE jobs SET progress = ? WHERE id = ?`, progress, id)
	return err
}

func SetJobResult(database *sql.DB, id, resultJSON string) error {
	_, err := database.Exec(`UPDATE jobs SET result_data = ? WHERE id = ?`, resultJSON, id)
	return err
}

func GetJob(database *sql.DB, id string) (*model.Job, error) {
	j := &model.Job{}
	var createdAt SQLiteTime
	var startedAt, completedAt sql.NullString
	err := database.QueryRow(`
		SELECT id, job_type, campaign_id, token_id, state, progress,
		       COALESCE(error_message, ''), COALESCE(input_path, ''), COALESCE(result_data, ''),
		       created_at, started_at, completed_at
		FROM jobs WHERE id = ?`, id,
	).Scan(
		&j.ID, &j.JobType, &j.CampaignID, &j.TokenID,
		&j.State, &j.Progress, &j.ErrorMessage,
		&j.InputPath, &j.ResultData,
		&createdAt, &startedAt, &completedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.CreatedAt = createdAt.Time
	if startedAt.Valid {
		var st SQLiteTime
		st.Scan(startedAt.String)
		j.StartedAt = &st.Time
	}
	if completedAt.Valid {
		var ct SQLiteTime
		ct.Scan(completedAt.String)
		j.CompletedAt = &ct.Time
	}
	return j, nil
}

func CountJobsByCampaign(database *sql.DB, campaignID string) (total, completed, failed int, err error) {
	err = database.QueryRow(`
		SELECT
		  COUNT(*),
		  SUM(CASE WHEN state = 'COMPLETED' THEN 1 ELSE 0 END),
		  SUM(CASE WHEN state = 'FAILED' THEN 1 ELSE 0 END)
		FROM jobs WHERE campaign_id = ?`, campaignID,
	).Scan(&total, &completed, &failed)
	return
}

func ListJobsByCampaign(database *sql.DB, campaignID string) ([]model.Job, error) {
	rows, err := database.Query(`
		SELECT id, job_type, campaign_id, token_id, state, progress,
		       COALESCE(error_message, ''), created_at
		FROM jobs WHERE campaign_id = ?
		ORDER BY created_at ASC`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []model.Job
	for rows.Next() {
		var j model.Job
		var createdAt SQLiteTime
		if err := rows.Scan(&j.ID, &j.JobType, &j.CampaignID, &j.TokenID,
			&j.State, &j.Progress, &j.ErrorMessage, &createdAt); err != nil {
			return nil, err
		}
		j.CreatedAt = createdAt.Time
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// EnqueueJobIfNotExists creates a watermark job for the given token only if
// no PENDING or RUNNING job already exists for that token. Returns true if
// a job already existed (no new row inserted).
func EnqueueJobIfNotExists(database *sql.DB, j *model.Job) (alreadyExists bool, err error) {
	res, err := database.Exec(
		`INSERT INTO jobs (id, job_type, campaign_id, token_id, state)
		 SELECT ?, ?, ?, ?, 'PENDING'
		 WHERE NOT EXISTS (
		   SELECT 1 FROM jobs WHERE token_id = ? AND state IN ('PENDING', 'RUNNING')
		 )`,
		j.ID, j.JobType, j.CampaignID, j.TokenID, j.TokenID,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 0, nil
}

// GetJobByToken returns the latest job for a given token ID.
func GetJobByToken(database *sql.DB, tokenID string) (*model.Job, error) {
	j := &model.Job{}
	var createdAt SQLiteTime
	err := database.QueryRow(`
		SELECT id, job_type, campaign_id, token_id, state, progress,
		       COALESCE(error_message, ''), created_at
		FROM jobs WHERE token_id = ?
		ORDER BY created_at DESC LIMIT 1`, tokenID,
	).Scan(&j.ID, &j.JobType, &j.CampaignID, &j.TokenID,
		&j.State, &j.Progress, &j.ErrorMessage, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	j.CreatedAt = createdAt.Time
	return j, nil
}

func InsertWatermarkIndex(database *sql.DB, payloadHex, tokenID, campaignID, recipientID string) error {
	_, err := database.Exec(
		`INSERT OR IGNORE INTO watermark_index (payload_hex, token_id, campaign_id, recipient_id) VALUES (?, ?, ?, ?)`,
		payloadHex, tokenID, campaignID, recipientID,
	)
	return err
}

// LookupWatermarkIndex finds a watermark_index row by matching the token_id_hex
// portion of the payload (bytes 2-9 of the 16-byte payload = chars 4-19 of hex).
func LookupWatermarkIndex(database *sql.DB, tokenIDHex string) (tokenID, campaignID, recipientID string, err error) {
	err = database.QueryRow(`
		SELECT token_id, campaign_id, recipient_id
		FROM watermark_index
		WHERE SUBSTR(payload_hex, 5, 16) = ?
		LIMIT 1`, tokenIDHex,
	).Scan(&tokenID, &campaignID, &recipientID)
	if err == sql.ErrNoRows {
		return "", "", "", nil
	}
	return
}

// LookupWatermarkIndexFuzzy finds the best-matching watermark_index row by
// comparing the token_id_hex portion of all stored payloads. Returns the match
// with the smallest hex-character difference count, provided it's within
// maxDiffChars (hex character differences).
func LookupWatermarkIndexFuzzy(database *sql.DB, tokenIDHex string, maxDiffChars int) (tokenID, campaignID, recipientID string, diffCount int, err error) {
	rows, err := database.Query(`
		SELECT payload_hex, token_id, campaign_id, recipient_id
		FROM watermark_index`)
	if err != nil {
		return "", "", "", 0, err
	}
	defer rows.Close()

	bestDiff := maxDiffChars + 1
	for rows.Next() {
		var payloadHex, tID, cID, rID string
		if err := rows.Scan(&payloadHex, &tID, &cID, &rID); err != nil {
			continue
		}
		// Extract token_id_hex portion (chars 4-19, 0-indexed)
		if len(payloadHex) < 20 {
			continue
		}
		storedTokenHex := payloadHex[4:20]
		diff := hexCharDiff(storedTokenHex, tokenIDHex)
		if diff < bestDiff {
			bestDiff = diff
			tokenID = tID
			campaignID = cID
			recipientID = rID
			diffCount = diff
		}
	}
	if bestDiff > maxDiffChars {
		return "", "", "", 0, nil
	}
	return tokenID, campaignID, recipientID, diffCount, nil
}

// hexCharDiff counts the number of differing hex characters between two
// equal-length hex strings. Returns len(a)+1 if lengths differ.
func hexCharDiff(a, b string) int {
	if len(a) != len(b) {
		return len(a) + 1
	}
	diff := 0
	for i := range a {
		if a[i] != b[i] {
			diff++
		}
	}
	return diff
}
