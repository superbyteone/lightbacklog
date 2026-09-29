// Package store owns the SQLite database: connections, migrations and backups.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (registers "sqlite")
)

// DB wraps one serialized writer connection and a small pool of read-only connections.
// SQLite in WAL mode allows readers to proceed while the single writer commits.
type DB struct {
	W    *sql.DB
	R    *sql.DB
	Path string
}

func dsn(path string, readOnly bool) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	if readOnly {
		q.Add("_pragma", "query_only(1)")
	}
	return "file:" + path + "?" + q.Encode()
}

// Open opens (creating if needed) the database at path and applies pending migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	w, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if err := w.PingContext(ctx); err != nil {
		w.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	r, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)
	db := &DB{W: w, R: r, Path: path}
	if err := db.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	// The database may hold private data; keep it owner-only.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(path+suffix, 0o600)
	}
	return db, nil
}

func (d *DB) Close() error {
	err := d.R.Close()
	if e := d.W.Close(); err == nil {
		err = e
	}
	return err
}

// Ping verifies that the database answers queries.
func (d *DB) Ping(ctx context.Context) error {
	var one int
	return d.R.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// Backup writes a consistent snapshot of the database to dst using VACUUM INTO.
// dst must not exist; the resulting file is owner-only.
func (d *DB) Backup(ctx context.Context, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("backup target %s already exists", dst)
	}
	quoted := "'" + strings.ReplaceAll(dst, "'", "''") + "'"
	if _, err := d.W.ExecContext(ctx, "VACUUM INTO "+quoted); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	return os.Chmod(dst, 0o600)
}

// Integrity summarizes a database health check.
type Integrity struct {
	OK          bool
	Detail      string
	Projects    int
	Tasks       int
	Attachments int
	MissingBlob []string // attachment blobs referenced by the database but absent on disk
}

// CheckIntegrity opens a database file read-only and verifies it: SQLite's own integrity and
// foreign-key checks, plus that every attachment row has its blob under uploadsDir (if given).
func CheckIntegrity(ctx context.Context, path, uploadsDir string) (*Integrity, error) {
	db, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	res := &Integrity{OK: true}
	var check string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil {
		return nil, err
	}
	if check != "ok" {
		res.OK, res.Detail = false, "integrity_check: "+check
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return nil, err
	}
	if rows.Next() {
		res.OK, res.Detail = false, strings.TrimSpace(res.Detail+" foreign_key_check reported violations")
	}
	rows.Close()
	for q, dst := range map[string]*int{"SELECT COUNT(*) FROM projects": &res.Projects, "SELECT COUNT(*) FROM tasks": &res.Tasks, "SELECT COUNT(*) FROM attachments": &res.Attachments} {
		if err := db.QueryRowContext(ctx, q).Scan(dst); err != nil {
			return nil, err
		}
	}
	if uploadsDir != "" {
		brows, err := db.QueryContext(ctx, "SELECT DISTINCT sha256 FROM attachments")
		if err != nil {
			return nil, err
		}
		defer brows.Close()
		for brows.Next() {
			var sha string
			if err := brows.Scan(&sha); err != nil {
				return nil, err
			}
			if _, err := os.Stat(filepath.Join(uploadsDir, sha[:2], sha)); err != nil {
				res.MissingBlob = append(res.MissingBlob, sha)
			}
		}
		if len(res.MissingBlob) > 0 {
			res.OK = false
		}
	}
	return res, nil
}
