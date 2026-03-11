// Package db provides MySQL-backed storage for pypi-mirror.
// Multiple processes (serve + sync) can connect simultaneously; MySQL handles
// concurrent writes natively without the file-level locking issues of SQLite.
package db
