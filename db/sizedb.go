package db

import (
	"database/sql"
	"time"
)

// GetRemoteSize returns the cached remote size for filePath.
// Returns ok=false if not in DB. size may be nil (meaning confirmed 404).
func (d *DB) GetRemoteSize(filePath string) (size *int64, ok bool, err error) {
	var s sql.NullInt64
	err = d.sql.QueryRow(
		"SELECT size FROM remote_sizes WHERE file_path=?", filePath,
	).Scan(&s)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if s.Valid {
		return &s.Int64, true, nil
	}
	return nil, true, nil
}

// SetRemoteSize stores a remote size (nil = 404).
func (d *DB) SetRemoteSize(filePath string, size *int64) error {
	now := time.Now().Unix()
	var s sql.NullInt64
	if size != nil {
		s = sql.NullInt64{Int64: *size, Valid: true}
	}
	_, err := d.sql.Exec(
		`INSERT INTO remote_sizes(file_path, size, recorded_at) VALUES(?,?,?)
		 ON CONFLICT(file_path) DO UPDATE SET size=excluded.size, recorded_at=excluded.recorded_at`,
		filePath, s, now,
	)
	return err
}

// GetLocalSize returns the cached local file size.
func (d *DB) GetLocalSize(filePath string) (size int64, ok bool, err error) {
	err = d.sql.QueryRow(
		"SELECT size FROM local_sizes WHERE file_path=?", filePath,
	).Scan(&size)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return size, true, nil
}

// SetLocalSize stores a local file size.
func (d *DB) SetLocalSize(filePath string, size int64) error {
	_, err := d.sql.Exec(
		`INSERT INTO local_sizes(file_path, size) VALUES(?,?)
		 ON CONFLICT(file_path) DO UPDATE SET size=excluded.size`,
		filePath, size,
	)
	return err
}

// DeleteLocalSize removes a local size entry.
func (d *DB) DeleteLocalSize(filePath string) error {
	_, err := d.sql.Exec("DELETE FROM local_sizes WHERE file_path=?", filePath)
	return err
}

// CleanRemoteSizes deletes NULL size entries older than ttl.
func (d *DB) CleanRemoteSizes(ttl time.Duration) error {
	cutoff := time.Now().Add(-ttl).Unix()
	_, err := d.sql.Exec(
		"DELETE FROM remote_sizes WHERE size IS NULL AND recorded_at < ?", cutoff,
	)
	return err
}

// CleanLocalSizes deletes entries whose path is not in knownPaths.
func (d *DB) CleanLocalSizes(knownPaths map[string]struct{}) error {
	rows, err := d.sql.Query("SELECT file_path FROM local_sizes")
	if err != nil {
		return err
	}
	defer rows.Close()

	var toDelete []string
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			return err
		}
		if _, exists := knownPaths[fp]; !exists {
			toDelete = append(toDelete, fp)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, fp := range toDelete {
		if _, err := d.sql.Exec("DELETE FROM local_sizes WHERE file_path=?", fp); err != nil {
			return err
		}
	}
	return nil
}
