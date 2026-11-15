// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

func (a *App) migrate(ctx context.Context) error {
	return Migrate(ctx, a.DB)
}

// Migrate applies the embedded schema for both the HTTP application and workers.
// The advisory lock serializes startup of separate processes.
func Migrate(ctx context.Context, db *pgxpool.Pool) error {
	migrations, err := readMigrations(assets)
	if err != nil {
		return err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(771903)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return err
	}
	for _, migration := range migrations {
		var applied bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)", migration.version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sql, err := assets.ReadFile("migrations/" + migration.name)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", migration.name, err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES($1)", migration.version); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type migrationFile struct {
	name    string
	version int
}

// Validate the complete file set before connecting to the database or checking
// applied versions, so duplicate versions fail on both fresh and existing DBs.
func readMigrations(source fs.FS) ([]migrationFile, error) {
	entries, err := fs.ReadDir(source, "migrations")
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	seen := make(map[int]string)
	var migrations []migrationFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("invalid migration filename %q", entry.Name())
		}
		if previous, exists := seen[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d: %q and %q", version, previous, entry.Name())
		}
		seen[version] = entry.Name()
		migrations = append(migrations, migrationFile{name: entry.Name(), version: version})
	}
	return migrations, nil
}
