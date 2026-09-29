package store

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/superbyteone/lightbacklog/migrations"
)

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		num, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil {
			return nil, fmt.Errorf("migration %q: name must start with <number>_", e.Name())
		}
		body, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i := 1; i < len(out); i++ {
		if out[i].version == out[i-1].version {
			return nil, fmt.Errorf("duplicate migration version %d", out[i].version)
		}
	}
	return out, nil
}

// migrate applies pending migrations, each in its own transaction. When an existing
// database (one that already has applied migrations) is about to change, a snapshot is
// written next to the database first so a bad upgrade can be rolled back.
func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.W.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL) STRICT`); err != nil {
		return err
	}
	var current int
	if err := d.W.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current); err != nil {
		return err
	}
	all, err := loadMigrations()
	if err != nil {
		return err
	}
	var pending []migration
	for _, m := range all {
		if m.version > current {
			pending = append(pending, m)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if current > 0 {
		dst := filepath.Join(filepath.Dir(d.Path), "pre-migration", fmt.Sprintf("app-v%d-%s.db", current, time.Now().UTC().Format("20060102T150405Z")))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := d.Backup(ctx, dst); err != nil {
			return fmt.Errorf("pre-migration backup: %w", err)
		}
	}
	for _, m := range pending {
		tx, err := d.W.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)",
			m.version, m.name, time.Now().UnixMilli()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
