package db

import (
	"bytes"
	"database/sql"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// migrationFiles returns the repository's migrations whose filename sorts
// before stop, so a test can build a database the way an older release left
// it. An empty stop keeps them all.
func migrationFiles(t *testing.T, stop string) fstest.MapFS {
	t.Helper()
	root := os.DirFS("../..")
	entries, err := fs.ReadDir(root, "migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	files := fstest.MapFS{}
	for _, e := range entries {
		if stop != "" && e.Name() >= stop {
			continue
		}
		data, err := fs.ReadFile(root, "migrations/"+e.Name())
		if err != nil {
			t.Fatalf("read migration %s: %v", e.Name(), err)
		}
		files["migrations/"+e.Name()] = &fstest.MapFile{Data: data}
	}
	return files
}

// seedCampaign writes a published campaign with two tokens, a download event,
// a watermark trace row and a job. It uses only columns that exist from
// migration 001, so it works on a database at any version.
func seedCampaign(t *testing.T, database *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		`INSERT INTO accounts (id, email, name, password_hash) VALUES ('acct', 'acct@example.test', 'acct', 'x')`,
		`INSERT INTO assets (id, account_id, title, asset_type, original_path, file_size_bytes, sha256_original, mime_type)
			VALUES ('asset', 'acct', 'asset', 'video', 'originals/asset/source.mp4', 1, 'sha', 'video/mp4')`,
		`INSERT INTO recipients (id, account_id, name, email)
			VALUES ('rec-1', 'acct', 'rec-1', 'rec-1@example.test'), ('rec-2', 'acct', 'rec-2', 'rec-2@example.test')`,
		`INSERT INTO campaigns (id, account_id, asset_id, name, state) VALUES ('camp', 'acct', 'asset', 'camp', 'READY')`,
		`INSERT INTO download_tokens (id, campaign_id, recipient_id, state)
			VALUES ('tok-1', 'camp', 'rec-1', 'ACTIVE'), ('tok-2', 'camp', 'rec-2', 'CONSUMED')`,
		`INSERT INTO download_events (id, token_id, campaign_id, recipient_id, asset_id, ip_address)
			VALUES ('event', 'tok-2', 'camp', 'rec-2', 'asset', '192.0.2.1')`,
		`INSERT INTO watermark_index (payload_hex, token_id, campaign_id, recipient_id) VALUES ('00ff', 'tok-1', 'camp', 'rec-1')`,
		`INSERT INTO jobs (id, job_type, campaign_id, token_id, state) VALUES ('job', 'watermark_video', 'camp', 'tok-1', 'COMPLETED')`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("seed: %v\n%s", err, stmt)
		}
	}
}

// rowCounts returns the number of rows in each table seedCampaign writes to,
// below the account level.
func rowCounts(t *testing.T, database *sql.DB) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, table := range []string{"campaigns", "download_tokens", "download_events", "watermark_index", "jobs"} {
		var n int
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = n
	}
	return counts
}

// assertForeignKeysOn checks that the next connection the pool hands out
// enforces foreign keys, both by the pragma and by a token insert that names
// no real campaign.
func assertForeignKeysOn(t *testing.T, database *sql.DB, label string) {
	t.Helper()
	var on int
	if err := database.QueryRow("PRAGMA foreign_keys").Scan(&on); err != nil {
		t.Fatalf("%s: read PRAGMA foreign_keys: %v", label, err)
	}
	if on != 1 {
		t.Errorf("%s: PRAGMA foreign_keys = %d, want 1", label, on)
	}
	_, err := database.Exec(`INSERT INTO download_tokens (id, campaign_id, recipient_id) VALUES (?, ?, 'no-such-recipient')`,
		"orphan "+label, "no such campaign "+label)
	if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("%s: insert of a token with no campaign = %v, want a FOREIGN KEY error", label, err)
	}
}

// TestMigrateUpgradeKeepsRows upgrades a database that holds a published
// campaign across the campaigns table rebuilds in migrations 010 and 011.
// With foreign keys on, DROP TABLE campaigns first deletes every campaign
// row, and ON DELETE CASCADE then removes the tokens, their download events
// and their watermark trace rows, without an error.
func TestMigrateUpgradeKeepsRows(t *testing.T) {
	for _, tc := range []struct{ name, stop string }{
		{"from 009 to latest", "010"},
		{"from 010 to latest", "011"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, err := Open(t.TempDir())
			if err != nil {
				t.Fatalf("open db: %v", err)
			}
			t.Cleanup(func() { database.Close() })
			if err := Migrate(database, migrationFiles(t, tc.stop)); err != nil {
				t.Fatalf("migrate to before %s: %v", tc.stop, err)
			}
			seedCampaign(t, database)

			if err := Migrate(database, os.DirFS("../..")); err != nil {
				t.Fatalf("upgrade: %v", err)
			}

			want := map[string]int{"campaigns": 1, "download_tokens": 2, "download_events": 1, "watermark_index": 1, "jobs": 1}
			if got := rowCounts(t, database); !maps.Equal(got, want) {
				t.Errorf("rows after upgrade = %v, want %v", got, want)
			}
		})
	}
}

// TestMigrateLeavesForeignKeysOn checks that the connection Migrate pins and
// any connection the pool opens later both enforce foreign keys.
func TestMigrateLeavesForeignKeysOn(t *testing.T) {
	database := newTestDB(t)
	assertForeignKeysOn(t, database, "connection used by Migrate")

	// With no idle connections kept, each query opens a new connection.
	database.SetMaxIdleConns(0)
	assertForeignKeysOn(t, database, "new connection")
}

// TestMigrateRejectsForeignKeyViolation runs a migration that rebuilds
// campaigns without copying its rows, which orphans every token. Migrate must
// refuse it and leave the database as it was before that migration.
func TestMigrateRejectsForeignKeyViolation(t *testing.T) {
	database := newTestDB(t)
	seedCampaign(t, database)
	before := rowCounts(t, database)

	const bad = "999_rebuild_campaigns_without_rows.sql"
	files := migrationFiles(t, "")
	files["migrations/"+bad] = &fstest.MapFile{Data: []byte(`
CREATE TABLE campaigns_new (id TEXT PRIMARY KEY);
DROP TABLE campaigns;
ALTER TABLE campaigns_new RENAME TO campaigns;
`)}

	err := Migrate(database, files)
	if err == nil || !strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), "foreign key") {
		t.Fatalf("Migrate = %v, want a foreign key error naming %s", err, bad)
	}

	var recorded int
	if err := database.QueryRow(`SELECT COUNT(*) FROM _migrations WHERE filename = ?`, bad).Scan(&recorded); err != nil {
		t.Fatalf("read _migrations: %v", err)
	}
	if recorded != 0 {
		t.Errorf("%s recorded as applied after it was rejected", bad)
	}
	if got := rowCounts(t, database); !maps.Equal(got, before) {
		t.Errorf("rows after rejected migration = %v, want %v", got, before)
	}
	assertForeignKeysOn(t, database, "after rejected migration")
}

// TestMigrateToleratesExistingViolations upgrades a database at 009 that
// already holds an orphan token, as the sqlite3 shell leaves after a manual
// DELETE (it has foreign keys off by default). The upgrade must still run
// and keep every row, and a migration that adds a violation of the same kind
// must still fail.
func TestMigrateToleratesExistingViolations(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := Migrate(database, migrationFiles(t, "010")); err != nil {
		t.Fatalf("migrate to 009: %v", err)
	}
	seedCampaign(t, database)
	for _, stmt := range []string{
		`PRAGMA foreign_keys=OFF`,
		`INSERT INTO campaigns (id, account_id, asset_id, name) VALUES ('gone', 'acct', 'asset', 'gone')`,
		`INSERT INTO download_tokens (id, campaign_id, recipient_id) VALUES ('orphan', 'gone', 'rec-1')`,
		`DELETE FROM campaigns WHERE id = 'gone'`,
		`PRAGMA foreign_keys=ON`,
	} {
		if _, err := database.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	// TestMain discards the logs. Capture them here to see the warning.
	var logs bytes.Buffer
	defaultLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(defaultLogger) })

	if err := Migrate(database, os.DirFS("../..")); err != nil {
		t.Fatalf("upgrade with an existing violation: %v", err)
	}
	want := map[string]int{"campaigns": 1, "download_tokens": 3, "download_events": 1, "watermark_index": 1, "jobs": 1}
	if got := rowCounts(t, database); !maps.Equal(got, want) {
		t.Errorf("rows after upgrade = %v, want %v", got, want)
	}
	if !strings.Contains(logs.String(), "already holds foreign key violations") ||
		!strings.Contains(logs.String(), "download_tokens -> campaigns") {
		t.Errorf("no warning for the existing download_tokens -> campaigns violation, logs:\n%s", logs.String())
	}

	const bad = "999_rebuild_campaigns_without_rows.sql"
	files := migrationFiles(t, "")
	files["migrations/"+bad] = &fstest.MapFile{Data: []byte(`
CREATE TABLE campaigns_new (id TEXT PRIMARY KEY);
DROP TABLE campaigns;
ALTER TABLE campaigns_new RENAME TO campaigns;
`)}
	if err := Migrate(database, files); err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("Migrate = %v, want a foreign key error naming %s", err, bad)
	}
	assertForeignKeysOn(t, database, "after rejected migration")
}
