package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
)

// createMigrationBackup uses SQLite to checkpoint a consistent copy before schema changes.
func (s *Store) createMigrationBackup(ctx context.Context, databasePath string) (string, error) {
	dir, err := os.MkdirTemp(filepath.Dir(databasePath), ".election-bot-pre-v2-")
	if err != nil {
		return "", err
	}
	if err = os.Chmod(dir, 0700); err != nil {
		return "", err
	}
	backup := filepath.Join(dir, "elections.db")
	if _, err = s.db.ExecContext(ctx, `VACUUM INTO ?`, backup); err != nil {
		return "", err
	}
	if err = os.Chmod(backup, 0600); err != nil {
		return "", err
	}
	check, err := sql.Open("sqlite", backup)
	if err != nil {
		return "", err
	}
	defer check.Close()
	var integrity string
	if err = check.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return "", err
	}
	if integrity != "ok" {
		return "", errors.New("migration backup integrity check failed")
	}
	rows, err := check.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if rows.Next() {
		return "", errors.New("migration backup foreign key check failed")
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	return backup, nil
}
