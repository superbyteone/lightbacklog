package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/superbyteone/lightbacklog/internal/files"
	"github.com/superbyteone/lightbacklog/internal/service"
	"github.com/superbyteone/lightbacklog/internal/store"
)

func TestBackupRestoreRoundTripWithUploads(t *testing.T) {
	ctx := context.Background()
	data := filepath.Join(t.TempDir(), "data")
	db, err := store.Open(ctx, filepath.Join(data, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fs, _ := files.NewStore(filepath.Join(data, "uploads"))
	svc := service.New(db, fs, service.Config{})
	u, _ := svc.CreateUser(ctx, service.CreateUserInput{Username: "alice", Password: "correct horse battery", IsAdmin: true})
	p := service.Principal{UserID: u.ID, Username: "alice", IsAdmin: true}
	proj, _ := svc.CreateProject(ctx, p, service.CreateProjectInput{Name: "Backed Up"})
	tk, _, _ := svc.CreateTask(ctx, p, service.CreateTaskInput{Project: proj.ID, Title: "keep me", Description: "important", Labels: []string{"x"}})
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 500)...)
	att, err := svc.Upload(ctx, p, "", tk.ID, "shot.png", bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	// An in-flight upload in tmp/ must not be archived.
	os.WriteFile(filepath.Join(data, "uploads", "tmp", "up-partial"), []byte("partial"), 0o600)

	dest := filepath.Join(t.TempDir(), "backups")
	archive, err := Create(ctx, db, data, dest, 14)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(archive); info.Mode().Perm() != 0o600 {
		t.Fatalf("archive mode %v, want 0600", info.Mode().Perm())
	}
	if info, _ := os.Stat(dest); info.Mode().Perm() != 0o700 {
		t.Fatalf("backup dir mode %v, want 0700", info.Mode().Perm())
	}
	names, err := listArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if strings.Contains(n, "tmp") || strings.Contains(n, "partial") {
			t.Fatalf("archive contains in-flight upload: %v", names)
		}
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dest, ".snapshot-*")); len(leftovers) > 0 {
		t.Fatalf("snapshot not cleaned up: %v", leftovers)
	}

	// Changes after the backup must not leak into it.
	svc.CreateTask(ctx, p, service.CreateTaskInput{Project: proj.ID, Title: "created after backup"})

	restored := filepath.Join(t.TempDir(), "restored")
	res, err := Restore(ctx, archive, restored)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Projects != 1 || res.Tasks != 1 || res.Attachments != 1 || len(res.MissingBlob) != 0 {
		t.Fatalf("restore verification: %+v", res)
	}
	// The restored directory is a working data dir: open it and read the task and its file back.
	db2, err := store.Open(ctx, filepath.Join(restored, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	fs2, _ := files.NewStore(filepath.Join(restored, "uploads"))
	svc2 := service.New(db2, fs2, service.Config{})
	got, err := svc2.GetTask(ctx, p, tk.ID)
	if err != nil || got.Title != "keep me" || *got.Description != "important" {
		t.Fatalf("restored task: %v %+v", err, got)
	}
	_, f, err := svc2.OpenAttachment(ctx, p, att.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(f)
	if !bytes.Equal(buf.Bytes(), png) {
		t.Fatal("restored upload differs from the original")
	}

	// Restore refuses to write into a directory that already has content.
	if _, err := Restore(ctx, archive, restored); err == nil {
		t.Fatal("restore into a non-empty directory must fail")
	}
}

func TestRestoreRejectsHostileArchivesAndDetectsMissingBlobs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	// A file that is not a gzip archive.
	bad := filepath.Join(dir, "bad.tar.gz")
	os.WriteFile(bad, []byte("not an archive"), 0o600)
	if _, err := Restore(ctx, bad, filepath.Join(dir, "t1")); err == nil {
		t.Fatal("corrupt archive accepted")
	}

	// Missing blobs are reported.
	data := filepath.Join(dir, "data")
	db, _ := store.Open(ctx, filepath.Join(data, "app.db"))
	defer db.Close()
	fs, _ := files.NewStore(filepath.Join(data, "uploads"))
	svc := service.New(db, fs, service.Config{})
	u, err := svc.CreateUser(ctx, service.CreateUserInput{Username: "ab", Password: "correct horse battery"})
	if err != nil {
		t.Fatal(err)
	}
	p := service.Principal{UserID: u.ID}
	proj, err := svc.CreateProject(ctx, p, service.CreateProjectInput{Name: "Pr"})
	if err != nil {
		t.Fatal(err)
	}
	tk, _, err := svc.CreateTask(ctx, p, service.CreateTaskInput{Project: proj.ID, Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Upload(ctx, p, "", tk.ID, "a.txt", strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	blobs, _ := filepath.Glob(filepath.Join(data, "uploads", "*", "*"))
	for _, b := range blobs {
		os.Remove(b) // simulate a lost upload
	}
	archive, err := Create(ctx, db, data, filepath.Join(dir, "b"), 0)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Restore(ctx, archive, filepath.Join(dir, "t2"))
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || len(res.MissingBlob) != 1 {
		t.Fatalf("missing blob not detected: %+v", res)
	}
}

func TestPruneRemovesOnlyOldArchives(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "lightbacklog-20200101T000000Z.tar.gz")
	recent := filepath.Join(dir, "lightbacklog-20990101T000000Z.tar.gz")
	other := filepath.Join(dir, "notes.txt")
	for _, f := range []string{old, recent, other} {
		os.WriteFile(f, []byte("x"), 0o600)
	}
	os.Chtimes(old, time.Now().AddDate(0, 0, -40), time.Now().AddDate(0, 0, -40))
	os.Chtimes(other, time.Now().AddDate(0, 0, -40), time.Now().AddDate(0, 0, -40))
	prune(dir, time.Now().AddDate(0, 0, -14), "")
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old archive should be pruned")
	}
	for _, f := range []string{recent, other} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s must be kept: %v", f, err)
		}
	}
}
