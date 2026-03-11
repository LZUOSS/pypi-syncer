package db

import "time"

type PopularFile struct {
	FilePath  string
	VoteCount int
}

// RecordVote records a vote for filePath from ipPrefix, deduplicating within dedupWindow.
// Only inserts if no vote exists for (file_path, ip_prefix) within the last dedupWindow.
func (d *DB) RecordVote(filePath, ipPrefix string, dedupWindow time.Duration) error {
	now := time.Now().Unix()
	cutoff := time.Now().Add(-dedupWindow).Unix()

	var count int
	err := d.sql.QueryRow(
		"SELECT COUNT(*) FROM votes WHERE file_path=? AND ip_prefix=? AND voted_at > ?",
		filePath, ipPrefix, cutoff,
	).Scan(&count)
	if err != nil {
		return err
	}

	if count == 0 {
		_, err = d.sql.Exec(
			"INSERT INTO votes(file_path, ip_prefix, voted_at) VALUES(?,?,?)",
			filePath, ipPrefix, now,
		)
		return err
	}
	return nil
}

// QueryPopular returns files with vote_count >= minVotes since the given time.
func (d *DB) QueryPopular(since time.Time, minVotes int) ([]PopularFile, error) {
	rows, err := d.sql.Query(
		`SELECT file_path, COUNT(*) as vote_count
		 FROM votes
		 WHERE voted_at >= ?
		 GROUP BY file_path
		 HAVING vote_count >= ?
		 ORDER BY vote_count DESC`,
		since.Unix(), minVotes,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PopularFile
	for rows.Next() {
		var pf PopularFile
		if err := rows.Scan(&pf.FilePath, &pf.VoteCount); err != nil {
			return nil, err
		}
		results = append(results, pf)
	}
	return results, rows.Err()
}

// DeleteOldVotes deletes votes older than before.
func (d *DB) DeleteOldVotes(before time.Time) error {
	_, err := d.sql.Exec("DELETE FROM votes WHERE voted_at < ?", before.Unix())
	return err
}
