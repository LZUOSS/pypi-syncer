package db

import (
	"database/sql"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

type DB struct {
	sql *sql.DB
}

// Open connects to the MySQL server using dsn and initialises the schema.
func Open(dsn string) (*DB, error) {
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping db: %w", err)
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
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS votes (
			file_path  VARCHAR(512)  NOT NULL,
			ip_prefix  VARCHAR(64)   NOT NULL,
			voted_at   BIGINT        NOT NULL,
			PRIMARY KEY (file_path, ip_prefix, voted_at)
		) DEFAULT CHARSET=utf8mb4`,
		`CREATE INDEX IF NOT EXISTS idx_votes_file ON votes(file_path)`,
		`CREATE INDEX IF NOT EXISTS idx_votes_time ON votes(voted_at)`,
		`CREATE TABLE IF NOT EXISTS remote_sizes (
			file_path    VARCHAR(512) NOT NULL PRIMARY KEY,
			size         BIGINT,
			recorded_at  BIGINT       NOT NULL
		) DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS local_sizes (
			file_path VARCHAR(512) NOT NULL PRIMARY KEY,
			size      BIGINT       NOT NULL,
			tier      INT          NOT NULL DEFAULT 0
		) DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS serials (
			package_name VARCHAR(256) NOT NULL PRIMARY KEY,
			serial       BIGINT       NOT NULL
		) DEFAULT CHARSET=utf8mb4`,
	}

	for _, stmt := range stmts {
		if _, err := d.sql.Exec(stmt); err != nil {
			return fmt.Errorf("exec schema: %w", err)
		}
	}

	// Migrate: add tier column to local_sizes if it does not exist yet.
	var count int
	err := d.sql.QueryRow(`
		SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME   = 'local_sizes'
		  AND COLUMN_NAME  = 'tier'`,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check tier column: %w", err)
	}
	if count == 0 {
		if _, err := d.sql.Exec(
			"ALTER TABLE local_sizes ADD COLUMN tier INT NOT NULL DEFAULT 0",
		); err != nil {
			return fmt.Errorf("add tier column: %w", err)
		}
	}

	return nil
}
