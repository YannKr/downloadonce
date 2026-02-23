package db

import (
	"database/sql"

	"github.com/ypk/downloadonce/internal/model"
)

func CreateAccount(database *sql.DB, a *model.Account) error {
	_, err := database.Exec(
		`INSERT INTO accounts (id, email, name, password_hash) VALUES (?, ?, ?, ?)`,
		a.ID, a.Email, a.Name, a.PasswordHash,
	)
	return err
}

func GetAccountByEmail(database *sql.DB, email string) (*model.Account, error) {
	a := &model.Account{}
	var createdAt SQLiteTime
	err := database.QueryRow(
		`SELECT id, email, name, password_hash, created_at FROM accounts WHERE email = ?`, email,
	).Scan(&a.ID, &a.Email, &a.Name, &a.PasswordHash, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	a.CreatedAt = createdAt.Time
	return a, err
}

func GetAccountByID(database *sql.DB, id string) (*model.Account, error) {
	a := &model.Account{}
	var createdAt SQLiteTime
	err := database.QueryRow(
		`SELECT id, email, name, password_hash, created_at FROM accounts WHERE id = ?`, id,
	).Scan(&a.ID, &a.Email, &a.Name, &a.PasswordHash, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	a.CreatedAt = createdAt.Time
	return a, err
}

func AccountExists(database *sql.DB) (bool, error) {
	var count int
	err := database.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&count)
	return count > 0, err
}
