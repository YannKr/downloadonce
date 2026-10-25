package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
)

func Migrate(database *sql.DB, migrationFS fs.FS) (err error) {
	_, err = database.Exec(`CREATE TABLE IF NOT EXISTS _migrations (
		filename   TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	)`)
	if err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	// Migrations run with foreign keys off. A table rebuild (create the new
	// table, copy the rows, DROP the old one, rename) otherwise loses data:
	// with foreign keys on, DROP TABLE first deletes every row, which fires
	// ON DELETE CASCADE in the child tables. SQLite ignores the pragma inside
	// a transaction, so it is set on one pinned connection outside them, and
	// PRAGMA foreign_key_check runs before each commit instead. Everything
	// below must use conn: with MaxOpenConns(1), a query on database would
	// wait for conn forever.
	ctx := context.Background()
	conn, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("pin connection: %w", err)
	}
	defer func() {
		if fkErr := setForeignKeys(ctx, conn, true); fkErr != nil {
			// Discard the connection rather than return it to the pool
			// without foreign keys. Open turns them on for new connections.
			conn.Raw(func(any) error { return driver.ErrBadConn })
			err = errors.Join(err, fkErr)
		}
		conn.Close()
	}()
	if err := setForeignKeys(ctx, conn, false); err != nil {
		return err
	}

	for _, name := range names {
		var count int
		err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM _migrations WHERE filename = ?", name).Scan(&count)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if count > 0 {
			continue
		}

		content, err := fs.ReadFile(migrationFS, "migrations/"+name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", name, err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("exec migration %s: %w", name, err)
		}

		var table, parent string
		var rowid sql.NullInt64
		var fkid int
		err = tx.QueryRow("PRAGMA foreign_key_check").Scan(&table, &rowid, &parent, &fkid)
		if err == nil {
			tx.Rollback()
			return fmt.Errorf("migration %s leaves foreign key violations: %s row %d has no matching row in %s",
				name, table, rowid.Int64, parent)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			return fmt.Errorf("foreign key check for %s: %w", name, err)
		}

		if _, err := tx.Exec("INSERT INTO _migrations (filename) VALUES (?)", name); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}

		slog.Info("applied migration", "file", name)
	}

	return nil
}

// setForeignKeys turns foreign key enforcement on or off for conn, then reads
// the pragma back, because SQLite ignores the change without an error inside
// a transaction.
func setForeignKeys(ctx context.Context, conn *sql.Conn, on bool) error {
	want := 0
	if on {
		want = 1
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA foreign_keys=%d", want)); err != nil {
		return fmt.Errorf("set foreign_keys=%d: %w", want, err)
	}
	var got int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&got); err != nil {
		return fmt.Errorf("read foreign_keys: %w", err)
	}
	if got != want {
		return fmt.Errorf("foreign_keys is %d after setting it to %d", got, want)
	}
	return nil
}
