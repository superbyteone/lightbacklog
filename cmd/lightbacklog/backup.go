package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/superbyteone/lightbacklog/internal/backup"
	"github.com/superbyteone/lightbacklog/internal/store"
)

// backupCmd writes a full backup (database snapshot + uploads) into the backups directory and
// prunes archives older than LB_BACKUP_KEEP_DAYS (default 14).
func backupCmd(args []string) error {
	dir := env("LB_BACKUP_DIR", "./backups")
	if len(args) > 0 {
		dir = args[0]
	}
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(dataDir(), "app.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	path, err := backup.Create(ctx, db, dataDir(), dir, int(envInt("LB_BACKUP_KEEP_DAYS", 14)))
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// restoreCmd extracts a backup archive into a new, empty directory and verifies it. It never
// modifies the live data directory; see the README for how to put a restored copy into service.
func restoreCmd(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: lightbacklog restore <archive.tar.gz> <new-data-dir>")
	}
	res, err := backup.Restore(context.Background(), args[0], args[1])
	if err != nil {
		return err
	}
	fmt.Printf("restored to %s: %d projects, %d tasks, %d attachments\n", args[1], res.Projects, res.Tasks, res.Attachments)
	if !res.OK {
		if res.Detail != "" {
			fmt.Println("PROBLEM:", res.Detail)
		}
		for _, sha := range res.MissingBlob {
			fmt.Println("PROBLEM: missing uploaded file", sha)
		}
		return fmt.Errorf("restored data failed verification")
	}
	fmt.Println("verification passed (integrity_check, foreign keys, all uploaded files present)")
	return nil
}
