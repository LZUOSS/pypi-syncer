package db

import (
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DB struct {
	sql *sql.DB
}

func Open(repoPath string) (*DB, error) {
	dbPath := filepath.Join(repoPath, "pypi-mirror.db")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set journal_mode: %w", err)
	}
	if _, err := sqlDB.Exec("PRAGMA busy_timeout=15000"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set busy_timeout: %w", err)
	}
	if _, err := sqlDB.Exec("PRAGMA foreign_keys=ON"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set foreign_keys: %w", err)
	}

	d := &DB{sql: sqlDB}
	if err := d.initSchema(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return d, nil
}

func (d *DB) Close() error {
	return d.sql.Close()
}

func (d *DB) DB() *sql.DB {
	return d.sql
}

func (d *DB) initSchema() error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS votes (
			file_path   TEXT NOT NULL,
			ip_prefix   TEXT NOT NULL,
			voted_at    INTEGER NOT NULL,
			PRIMARY KEY (file_path, ip_prefix, voted_at)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_votes_file ON votes(file_path)`,
		`CREATE INDEX IF NOT EXISTS idx_votes_time ON votes(voted_at)`,
		`CREATE TABLE IF NOT EXISTS remote_sizes (
			file_path    TEXT PRIMARY KEY,
			size         INTEGER,
			recorded_at  INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS local_sizes (
			file_path TEXT PRIMARY KEY,
			size      INTEGER NOT NULL,
			tier      INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS serials (
			package_name TEXT PRIMARY KEY,
			serial       INTEGER NOT NULL
		)`,
	}

	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:40], err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// Migrate: add tier column to local_sizes if it does not exist yet.
	// This handles databases created before the multi-tier feature was added.
	rows, err := d.sql.Query("PRAGMA table_info(local_sizes)")
	if err != nil {
		return fmt.Errorf("pragma table_info: %w", err)
	}
	hasTier := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("scan table_info: %w", err)
		}
		if name == "tier" {
			hasTier = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("table_info rows: %w", err)
	}

	if !hasTier {
		if _, err := d.sql.Exec("ALTER TABLE local_sizes ADD COLUMN tier INTEGER NOT NULL DEFAULT 0"); err != nil {
			return fmt.Errorf("add tier column: %w", err)
		}
	}

	return nil
}
