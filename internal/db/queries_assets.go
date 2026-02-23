package db

import (
	"database/sql"

	"github.com/ypk/downloadonce/internal/model"
)

func CreateAsset(database *sql.DB, a *model.Asset) error {
	_, err := database.Exec(
		`INSERT INTO assets (id, account_id, title, asset_type, original_path,
		  file_size_bytes, sha256_original, mime_type, duration_secs, resolution_w, resolution_h)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.AccountID, a.OriginalName, a.AssetType, a.OriginalPath,
		a.FileSize, a.SHA256, a.MimeType, a.Duration, a.Width, a.Height,
	)
	return err
}

func ListAssets(database *sql.DB, accountID string) ([]model.Asset, error) {
	rows, err := database.Query(
		`SELECT id, account_id, title, asset_type, original_path,
		  file_size_bytes, sha256_original, mime_type, duration_secs, resolution_w, resolution_h, created_at
		 FROM assets WHERE account_id = ? ORDER BY created_at DESC`, accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var assets []model.Asset
	for rows.Next() {
		var a model.Asset
		var createdAt SQLiteTime
		err := rows.Scan(&a.ID, &a.AccountID, &a.OriginalName, &a.AssetType,
			&a.OriginalPath, &a.FileSize, &a.SHA256, &a.MimeType,
			&a.Duration, &a.Width, &a.Height, &createdAt)
		if err != nil {
			return nil, err
		}
		a.CreatedAt = createdAt.Time
		assets = append(assets, a)
	}
	return assets, rows.Err()
}

func GetAsset(database *sql.DB, id string) (*model.Asset, error) {
	a := &model.Asset{}
	var createdAt SQLiteTime
	err := database.QueryRow(
		`SELECT id, account_id, title, asset_type, original_path,
		  file_size_bytes, sha256_original, mime_type, duration_secs, resolution_w, resolution_h, created_at
		 FROM assets WHERE id = ?`, id,
	).Scan(&a.ID, &a.AccountID, &a.OriginalName, &a.AssetType,
		&a.OriginalPath, &a.FileSize, &a.SHA256, &a.MimeType,
		&a.Duration, &a.Width, &a.Height, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	a.CreatedAt = createdAt.Time
	return a, err
}

func DeleteAsset(database *sql.DB, id string) error {
	_, err := database.Exec(`DELETE FROM assets WHERE id = ?`, id)
	return err
}
