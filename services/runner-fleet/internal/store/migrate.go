package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Migrate applies any not-yet-applied *.up.sql files under dir in name order,
// tracking versions in fleet.schema_migrations. Boot-time migration keeps the
// compose stack self-provisioning (schema fleet is exclusive-write owned).
func (s *PGStore) Migrate(ctx context.Context, dir string) error {
	if _, err := s.pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS fleet`); err != nil {
		return fmt.Errorf("pg store: ensure schema: %w", err)
	}
	if _, err := s.pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS fleet.schema_migrations (version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`,
	); err != nil {
		return fmt.Errorf("pg store: ensure migrations table: %w", err)
	}
	migrations, err := readMigrations(dir)
	if err != nil {
		return err
	}
	for _, m := range migrations {
		var applied bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM fleet.schema_migrations WHERE version=$1)`, m.version,
		).Scan(&applied); err != nil {
			return fmt.Errorf("pg store: migration lookup %d: %w", m.version, err)
		}
		if applied {
			continue
		}
		if err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.sql); err != nil {
				return fmt.Errorf("pg store: apply %s: %w", m.file, err)
			}
			_, err := tx.Exec(ctx, `INSERT INTO fleet.schema_migrations (version) VALUES ($1)`, m.version)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

type migration struct {
	version int
	file    string
	sql     string
}

func readMigrations(dir string) ([]migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("pg store: read migrations dir %s: %w", dir, err)
	}
	var ups []migration
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		base := strings.TrimSuffix(name, ".up.sql")
		versionStr := base
		if i := strings.Index(base, "_"); i >= 0 {
			versionStr = base[:i]
		}
		version, err := strconv.Atoi(versionStr)
		if err != nil {
			return nil, fmt.Errorf("pg store: bad migration name %s: %w", name, err)
		}
		sqlBytes, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("pg store: read %s: %w", name, err)
		}
		ups = append(ups, migration{version: version, file: name, sql: string(sqlBytes)})
	}
	sort.Slice(ups, func(i, j int) bool { return ups[i].version < ups[j].version })
	return ups, nil
}

// MigrationsDir resolves the migration folder relative to the service root.
func MigrationsDir() string {
	for _, dir := range []string{"migrations", "services/runner-fleet/migrations"} {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return "migrations"
}
