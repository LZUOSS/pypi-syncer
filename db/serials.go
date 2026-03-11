package db

import "database/sql"

// GetSerial returns the local serial for a package.
func (d *DB) GetSerial(packageName string) (serial int64, ok bool, err error) {
	err = d.sql.QueryRow(
		"SELECT serial FROM serials WHERE package_name=?", packageName,
	).Scan(&serial)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return serial, true, nil
}

// SetSerial upserts the serial for a package.
func (d *DB) SetSerial(packageName string, serial int64) error {
	_, err := d.sql.Exec(
		`INSERT INTO serials(package_name, serial) VALUES(?,?)
		 ON CONFLICT(package_name) DO UPDATE SET serial=excluded.serial`,
		packageName, serial,
	)
	return err
}

// GetAllSerials returns all package serials as a map.
func (d *DB) GetAllSerials() (map[string]int64, error) {
	rows, err := d.sql.Query("SELECT package_name, serial FROM serials")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int64)
	for rows.Next() {
		var name string
		var serial int64
		if err := rows.Scan(&name, &serial); err != nil {
			return nil, err
		}
		result[name] = serial
	}
	return result, rows.Err()
}

// DeleteSerial removes a package's serial.
func (d *DB) DeleteSerial(packageName string) error {
	_, err := d.sql.Exec("DELETE FROM serials WHERE package_name=?", packageName)
	return err
}
