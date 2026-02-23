package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

func Open(dataDir string) (*sql.DB, error) {
	dbDir := filepath.Join(dataDir, "db")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	dbPath := filepath.Join(dbDir, "downloadonce.db")

	database, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA cache_size=-20000",
	}
	for _, p := range pragmas {
		if _, err := database.Exec(p); err != nil {
			database.Close()
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}

	database.SetMaxOpenConns(1)

	return database, nil
}

// SQLiteTime handles scanning time values from SQLite columns.
// SQLite stores timestamps as TEXT and different drivers may return
// string, time.Time, or int64 – this wrapper normalises them all.
type SQLiteTime struct {
	Time time.Time
}

func (st *SQLiteTime) Scan(src interface{}) error {
	switch v := src.(type) {
	case nil:
		st.Time = time.Time{}
	case string:
		formats := []string{
			"2006-01-02T15:04:05.000Z",
			time.RFC3339,
			"2006-01-02 15:04:05",
		}
		var err error
		for _, f := range formats {
			st.Time, err = time.Parse(f, v)
			if err == nil {
				return nil
			}
		}
		return fmt.Errorf("SQLiteTime: cannot parse %q", v)
	case time.Time:
		st.Time = v
	case int64:
		st.Time = time.Unix(v, 0)
	default:
		return fmt.Errorf("SQLiteTime: unsupported type %T", src)
	}
	return nil
}
