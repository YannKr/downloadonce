package db

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type AuditLog struct {
	ID         string
	AccountID  string
	Action     string
	TargetType string
	TargetID   string
	Detail     string
	IPAddress  string
	CreatedAt  time.Time
}

func InsertAuditLog(database *sql.DB, accountID, action, targetType, targetID, detail, ipAddress string) {
	go func() {
		_, _ = database.Exec(
			`INSERT INTO audit_logs (id, account_id, action, target_type, target_id, detail, ip_address) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			uuid.New().String(), accountID, action, targetType, targetID, detail, ipAddress,
		)
	}()
}

func ListAuditLogs(database *sql.DB, limit, offset int, filterAction string) ([]AuditLog, error) {
	var rows *sql.Rows
	var err error

	if filterAction != "" {
		rows, err = database.Query(
			`SELECT id, account_id, action, target_type, target_id, detail, ip_address, created_at
			 FROM audit_logs WHERE action = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
			filterAction, limit, offset,
		)
	} else {
		rows, err = database.Query(
			`SELECT id, account_id, action, target_type, target_id, detail, ip_address, created_at
			 FROM audit_logs ORDER BY created_at DESC LIMIT ? OFFSET ?`,
			limit, offset,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []AuditLog
	for rows.Next() {
		var l AuditLog
		var createdAt SQLiteTime
		if err := rows.Scan(&l.ID, &l.AccountID, &l.Action, &l.TargetType, &l.TargetID, &l.Detail, &l.IPAddress, &createdAt); err != nil {
			return nil, err
		}
		l.CreatedAt = createdAt.Time
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

func CountAuditLogs(database *sql.DB, filterAction string) (int, error) {
	var count int
	var err error
	if filterAction != "" {
		err = database.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = ?`, filterAction).Scan(&count)
	} else {
		err = database.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&count)
	}
	return count, err
}
