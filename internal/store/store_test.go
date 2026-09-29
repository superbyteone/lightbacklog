package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "data", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenAppliesSchemaAndSeeds(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	var n int
	if err := db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM statuses").Scan(&n); err != nil || n != 4 {
		t.Fatalf("statuses = %d, err %v; want 4", n, err)
	}
	if err := db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM priorities").Scan(&n); err != nil || n != 3 {
		t.Fatalf("priorities = %d, err %v; want 3", n, err)
	}
	if err := db.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestReopenIsIdempotentAndSnapshotsBeforeUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	ctx := context.Background()
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(ctx, path) // nothing pending: no pre-migration snapshot
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "pre-migration")); !os.IsNotExist(err) {
		t.Fatalf("unexpected pre-migration dir: %v", err)
	}
}

func TestFTSFollowsTaskChanges(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.W.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO users (id, username, password_hash, created_at) VALUES ('u1','a','x',1)`)
	exec(`INSERT INTO projects (id, key, name, created_by, created_at, updated_at) VALUES ('p1','WEB','Web','u1',1,1)`)
	exec(`INSERT INTO tasks (id, project_id, number, title, description_md, created_by, created_at, updated_at)
	      VALUES ('t1','p1',1,'Fix login redirect','users get stuck','u1',1,1)`)
	match := func(q string) int {
		var n int
		if err := db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_fts WHERE task_fts MATCH ?`, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if match("redir*") != 1 || match("stuck") != 1 {
		t.Fatal("inserted task not searchable")
	}
	exec(`UPDATE tasks SET title = 'Fix signup flow' WHERE id = 't1'`)
	if match("redirect") != 0 || match("signup") != 1 {
		t.Fatal("index not updated on title change")
	}
	exec(`DELETE FROM tasks WHERE id = 't1'`)
	if match("signup") != 0 {
		t.Fatal("index not cleaned on delete")
	}
}

func TestBackupProducesOwnerOnlyReadableCopy(t *testing.T) {
	db := openTest(t)
	dst := filepath.Join(t.TempDir(), "snap.db")
	if err := db.Backup(context.Background(), dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dst)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v mode %v", err, info)
	}
	if err := db.Backup(context.Background(), dst); err == nil {
		t.Fatal("expected error when target exists")
	}
	copyDB, err := Open(context.Background(), dst)
	if err != nil {
		t.Fatal(err)
	}
	copyDB.Close()
}
