package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"time"
)

func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func CreatePasswordReset(database *sql.DB, id, accountID, tokenHash string, expiresAt time.Time) error {
	_, err := database.Exec(
		`INSERT INTO password_resets (id, account_id, token_hash, expires_at) VALUES (?, ?, ?, ?)`,
		id, accountID, tokenHash, expiresAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

type PasswordReset struct {
	ID        string
	AccountID string
	ExpiresAt time.Time
	Used      bool
}

func GetPasswordResetByTokenHash(database *sql.DB, tokenHash string) (*PasswordReset, error) {
	var pr PasswordReset
	var expiresAt SQLiteTime
	var used int
	err := database.QueryRow(
		`SELECT id, account_id, expires_at, used FROM password_resets WHERE token_hash = ?`, tokenHash,
	).Scan(&pr.ID, &pr.AccountID, &expiresAt, &used)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pr.ExpiresAt = expiresAt.Time
	pr.Used = used != 0
	return &pr, nil
}

func MarkPasswordResetUsed(database *sql.DB, id string) error {
	_, err := database.Exec(`UPDATE password_resets SET used = 1 WHERE id = ?`, id)
	return err
}

func UpdateAccountPassword(database *sql.DB, accountID, passwordHash string) error {
	_, err := database.Exec(`UPDATE accounts SET password_hash = ? WHERE id = ?`, passwordHash, accountID)
	return err
}

func CleanExpiredResets(database *sql.DB) error {
	_, err := database.Exec(
		`DELETE FROM password_resets WHERE used = 1 OR expires_at < ?`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}
