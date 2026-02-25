package db

import (
	"database/sql"

	"github.com/YannKr/downloadonce/internal/model"
)

func CreateAccount(database *sql.DB, a *model.Account) error {
	enabled := 0
	if a.Enabled {
		enabled = 1
	}
	_, err := database.Exec(
		`INSERT INTO accounts (id, email, name, password_hash, role, enabled) VALUES (?, ?, ?, ?, ?, ?)`,
		a.ID, a.Email, a.Name, a.PasswordHash, a.Role, enabled,
	)
	return err
}

func GetAccountByEmail(database *sql.DB, email string) (*model.Account, error) {
	a := &model.Account{}
	var createdAt SQLiteTime
	var enabled int
	var notifyOnDl int
	err := database.QueryRow(
		`SELECT id, email, name, password_hash, role, enabled, notify_on_download, created_at FROM accounts WHERE email = ?`, email,
	).Scan(&a.ID, &a.Email, &a.Name, &a.PasswordHash, &a.Role, &enabled, &notifyOnDl, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	a.CreatedAt = createdAt.Time
	a.Enabled = enabled != 0
	a.NotifyOnDownload = notifyOnDl != 0
	return a, err
}

func GetAccountByID(database *sql.DB, id string) (*model.Account, error) {
	a := &model.Account{}
	var createdAt SQLiteTime
	var enabled int
	var notifyOnDl int
	err := database.QueryRow(
		`SELECT id, email, name, password_hash, role, enabled, notify_on_download, created_at FROM accounts WHERE id = ?`, id,
	).Scan(&a.ID, &a.Email, &a.Name, &a.PasswordHash, &a.Role, &enabled, &notifyOnDl, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	a.CreatedAt = createdAt.Time
	a.Enabled = enabled != 0
	a.NotifyOnDownload = notifyOnDl != 0
	return a, err
}

func AccountExists(database *sql.DB) (bool, error) {
	var count int
	err := database.QueryRow(`SELECT COUNT(*) FROM accounts`).Scan(&count)
	return count > 0, err
}

func ListAccounts(database *sql.DB) ([]model.Account, error) {
	rows, err := database.Query(
		`SELECT id, email, name, password_hash, role, enabled, notify_on_download, created_at FROM accounts ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []model.Account
	for rows.Next() {
		var a model.Account
		var createdAt SQLiteTime
		var enabled int
		var notifyOnDl int
		if err := rows.Scan(&a.ID, &a.Email, &a.Name, &a.PasswordHash, &a.Role, &enabled, &notifyOnDl, &createdAt); err != nil {
			return nil, err
		}
		a.CreatedAt = createdAt.Time
		a.Enabled = enabled != 0
		a.NotifyOnDownload = notifyOnDl != 0
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func UpdateAccountRole(database *sql.DB, id, role string) error {
	_, err := database.Exec(`UPDATE accounts SET role = ? WHERE id = ?`, role, id)
	return err
}

func UpdateAccountEnabled(database *sql.DB, id string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := database.Exec(`UPDATE accounts SET enabled = ? WHERE id = ?`, v, id)
	return err
}

func UpdateAccountNotifyOnDownload(database *sql.DB, id string, notify bool) error {
	v := 0
	if notify {
		v = 1
	}
	_, err := database.Exec(`UPDATE accounts SET notify_on_download = ? WHERE id = ?`, v, id)
	return err
}

func DeleteAccount(database *sql.DB, id string) error {
	_, err := database.Exec(`DELETE FROM accounts WHERE id = ?`, id)
	return err
}

func DeleteSessionsByAccount(database *sql.DB, accountID string) error {
	_, err := database.Exec(`DELETE FROM sessions WHERE account_id = ?`, accountID)
	return err
}
