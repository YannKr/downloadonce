package db

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/YannKr/downloadonce/internal/model"
)

func CreateUploadSession(database *sql.DB, s *model.UploadSession) error {
	chunks, _ := json.Marshal(s.ReceivedChunks)
	_, err := database.Exec(
		`INSERT INTO upload_sessions
		 (id, account_id, filename, size, mime_type, chunk_size, total_chunks,
		  received_chunks, status, storage_path, created_at, updated_at, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.AccountID, s.Filename, s.Size, s.MimeType, s.ChunkSize, s.TotalChunks,
		string(chunks), s.Status, s.StoragePath,
		s.CreatedAt.UTC().Format(time.RFC3339),
		s.UpdatedAt.UTC().Format(time.RFC3339),
		s.ExpiresAt.UTC().Format(time.RFC3339),
	)
	return err
}

func GetUploadSession(database *sql.DB, id string) (*model.UploadSession, error) {
	s := &model.UploadSession{}
	var createdAt, updatedAt, expiresAt SQLiteTime
	var chunksJSON string
	err := database.QueryRow(
		`SELECT id, account_id, filename, size, mime_type, chunk_size, total_chunks,
		  received_chunks, status, storage_path, created_at, updated_at, expires_at
		 FROM upload_sessions WHERE id = ?`, id,
	).Scan(&s.ID, &s.AccountID, &s.Filename, &s.Size, &s.MimeType, &s.ChunkSize,
		&s.TotalChunks, &chunksJSON, &s.Status, &s.StoragePath,
		&createdAt, &updatedAt, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.CreatedAt = createdAt.Time
	s.UpdatedAt = updatedAt.Time
	s.ExpiresAt = expiresAt.Time
	json.Unmarshal([]byte(chunksJSON), &s.ReceivedChunks)
	return s, nil
}

func UpdateUploadSessionChunks(database *sql.DB, id string, receivedChunks []int) error {
	chunks, _ := json.Marshal(receivedChunks)
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := database.Exec(
		`UPDATE upload_sessions SET received_chunks = ?, updated_at = ? WHERE id = ?`,
		string(chunks), now, id,
	)
	return err
}

func CompleteUploadSession(database *sql.DB, id, storagePath string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := database.Exec(
		`UPDATE upload_sessions SET status = ?, storage_path = ?, updated_at = ? WHERE id = ?`,
		"COMPLETE", storagePath, now, id,
	)
	return err
}

func DeleteUploadSession(database *sql.DB, id string) error {
	_, err := database.Exec(`DELETE FROM upload_sessions WHERE id = ?`, id)
	return err
}

func ListExpiredUploadSessions(database *sql.DB) ([]model.UploadSession, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := database.Query(
		`SELECT id, account_id, filename, size, mime_type, chunk_size, total_chunks,
		  received_chunks, status, storage_path, created_at, updated_at, expires_at
		 FROM upload_sessions WHERE expires_at < ? AND status = ?`,
		now, "PENDING",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []model.UploadSession
	for rows.Next() {
		var s model.UploadSession
		var createdAt, updatedAt, expiresAt SQLiteTime
		var chunksJSON string
		if err := rows.Scan(&s.ID, &s.AccountID, &s.Filename, &s.Size, &s.MimeType, &s.ChunkSize,
			&s.TotalChunks, &chunksJSON, &s.Status, &s.StoragePath,
			&createdAt, &updatedAt, &expiresAt); err == nil {
			s.CreatedAt = createdAt.Time
			s.UpdatedAt = updatedAt.Time
			s.ExpiresAt = expiresAt.Time
			json.Unmarshal([]byte(chunksJSON), &s.ReceivedChunks)
			sessions = append(sessions, s)
		}
	}
	return sessions, rows.Err()
}

func ExpireUploadSession(database *sql.DB, id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := database.Exec(
		`UPDATE upload_sessions SET status = ?, updated_at = ? WHERE id = ?`,
		"EXPIRED", now, id,
	)
	return err
}
