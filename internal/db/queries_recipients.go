package db

import (
	"database/sql"

	"github.com/ypk/downloadonce/internal/model"
)

func CreateRecipient(database *sql.DB, r *model.Recipient) error {
	_, err := database.Exec(
		`INSERT INTO recipients (id, account_id, name, email, org) VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.AccountID, r.Name, r.Email, r.Org,
	)
	return err
}

func ListRecipients(database *sql.DB) ([]model.Recipient, error) {
	rows, err := database.Query(
		`SELECT id, account_id, name, email, org, created_at
		 FROM recipients ORDER BY name ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var recipients []model.Recipient
	for rows.Next() {
		var r model.Recipient
		var createdAt SQLiteTime
		if err := rows.Scan(&r.ID, &r.AccountID, &r.Name, &r.Email, &r.Org, &createdAt); err != nil {
			return nil, err
		}
		r.CreatedAt = createdAt.Time
		recipients = append(recipients, r)
	}
	return recipients, rows.Err()
}

func GetRecipient(database *sql.DB, id string) (*model.Recipient, error) {
	r := &model.Recipient{}
	var createdAt SQLiteTime
	err := database.QueryRow(
		`SELECT id, account_id, name, email, org, created_at FROM recipients WHERE id = ?`, id,
	).Scan(&r.ID, &r.AccountID, &r.Name, &r.Email, &r.Org, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	r.CreatedAt = createdAt.Time
	return r, err
}

func GetOrCreateRecipientByEmail(database *sql.DB, accountID, name, email, org string) (*model.Recipient, error) {
	r := &model.Recipient{}
	var createdAt SQLiteTime
	err := database.QueryRow(
		`SELECT id, account_id, name, email, org, created_at FROM recipients WHERE email = ?`,
		email,
	).Scan(&r.ID, &r.AccountID, &r.Name, &r.Email, &r.Org, &createdAt)
	if err == nil {
		r.CreatedAt = createdAt.Time
		return r, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	r = &model.Recipient{
		AccountID: accountID,
		Name:      name,
		Email:     email,
		Org:       org,
	}
	return r, nil // caller must set ID and call CreateRecipient
}

func DeleteRecipient(database *sql.DB, id string) error {
	_, err := database.Exec(`DELETE FROM recipients WHERE id = ?`, id)
	return err
}
