package db

import (
	"database/sql"
	"time"

	"github.com/YannKr/downloadonce/internal/model"
)

func CreateSession(database *sql.DB, s *model.Session) error {
	_, err := database.Exec(
		`INSERT INTO sessions (id, account_id, expires_at) VALUES (?, ?, ?)`,
		s.ID, s.AccountID, s.ExpiresAt.UTC().Format(time.RFC3339),
	)
	return err
}

func GetSession(database *sql.DB, id string) (*model.Session, error) {
	s := &model.Session{}
	var createdAt, expiresAt SQLiteTime
	err := database.QueryRow(
		`SELECT id, account_id, created_at, expires_at FROM sessions WHERE id = ?`, id,
	).Scan(&s.ID, &s.AccountID, &createdAt, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	s.CreatedAt = createdAt.Time
	s.ExpiresAt = expiresAt.Time
	return s, err
}

func DeleteSession(database *sql.DB, id string) error {
	_, err := database.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func CleanExpiredSessions(database *sql.DB) error {
	_, err := database.Exec(
		`DELETE FROM sessions WHERE expires_at < ?`,
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}
