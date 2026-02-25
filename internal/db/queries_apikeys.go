package db

import (
	"database/sql"

	"github.com/YannKr/downloadonce/internal/model"
)

func CreateAPIKey(database *sql.DB, k *model.APIKey) error {
	_, err := database.Exec(
		`INSERT INTO api_keys (id, account_id, name, key_prefix, key_hash) VALUES (?, ?, ?, ?, ?)`,
		k.ID, k.AccountID, k.Name, k.KeyPrefix, k.KeyHash,
	)
	return err
}

func ListAPIKeys(database *sql.DB, accountID string) ([]model.APIKey, error) {
	rows, err := database.Query(
		`SELECT id, account_id, name, key_prefix, created_at, last_used_at
		 FROM api_keys WHERE account_id = ? ORDER BY created_at DESC`, accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []model.APIKey
	for rows.Next() {
		var k model.APIKey
		var createdAt SQLiteTime
		var lastUsed sql.NullString
		if err := rows.Scan(&k.ID, &k.AccountID, &k.Name, &k.KeyPrefix, &createdAt, &lastUsed); err != nil {
			return nil, err
		}
		k.CreatedAt = createdAt.Time
		if lastUsed.Valid {
			var lu SQLiteTime
			lu.Scan(lastUsed.String)
			k.LastUsedAt = &lu.Time
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func DeleteAPIKey(database *sql.DB, id, accountID string) error {
	_, err := database.Exec(`DELETE FROM api_keys WHERE id = ? AND account_id = ?`, id, accountID)
	return err
}

func GetAPIKeyByPrefix(database *sql.DB, prefix string) (*model.APIKey, error) {
	k := &model.APIKey{}
	var createdAt SQLiteTime
	err := database.QueryRow(
		`SELECT id, account_id, name, key_prefix, key_hash, created_at
		 FROM api_keys WHERE key_prefix = ?`, prefix,
	).Scan(&k.ID, &k.AccountID, &k.Name, &k.KeyPrefix, &k.KeyHash, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	k.CreatedAt = createdAt.Time
	return k, nil
}

func TouchAPIKeyUsed(database *sql.DB, id string) error {
	_, err := database.Exec(
		`UPDATE api_keys SET last_used_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ?`, id,
	)
	return err
}
