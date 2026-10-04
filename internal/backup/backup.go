// Package backup creates and restores full application backups: a consistent SQLite snapshot
// plus every uploaded file, in a single owner-only .tar.gz archive.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/superbyteone/lightbacklog/internal/store"
)

const (
	archivePrefix = "lightbacklog-"
	archiveSuffix = ".tar.gz"
	dbEntry       = "app.db"
)

var blobEntry = regexp.MustCompile(`^uploads/[0-9a-f]{2}/[0-9a-f]{64}$`)

// Create writes <destDir>/lightbacklog-<UTC timestamp>.tar.gz and prunes archives older than
// keepDays (0 disables pruning). The archive is written under a temporary name and verified by
// reading it back before it is published, so a partial archive is never left in place.
func Create(ctx context.Context, db *store.DB, dataDir, destDir string, keepDays int) (string, error) {
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	snapshot := filepath.Join(destDir, ".snapshot-"+stamp+".db")
	if err := db.Backup(ctx, snapshot); err != nil {
		return "", err
	}
	defer os.Remove(snapshot)

	final := filepath.Join(destDir, archivePrefix+stamp+archiveSuffix)
	tmp := final + ".tmp"
	if err := writeArchive(tmp, snapshot, filepath.Join(dataDir, "uploads")); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if _, err := listArchive(tmp); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("verifying new archive: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return "", err
	}
	if keepDays > 0 {
		prune(destDir, time.Now().AddDate(0, 0, -keepDays), final)
	}
	return final, nil
}

func writeArchive(path, snapshot, uploadsDir string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := addFile(tw, snapshot, dbEntry); err != nil {
		return err
	}
	err = filepath.WalkDir(uploadsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && p == uploadsDir {
				return nil
			}
			return err
		}
		rel, _ := filepath.Rel(uploadsDir, p)
		if d.IsDir() {
			if rel == "tmp" { // in-flight uploads are not part of the data set
				return filepath.SkipDir
			}
			return nil
		}
		name := "uploads/" + filepath.ToSlash(rel)
		if !d.Type().IsRegular() || !blobEntry.MatchString(name) {
			return nil
		}
		return addFile(tw, p, name)
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Sync()
}

func addFile(tw *tar.Writer, path, name string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: st.Size(), ModTime: st.ModTime(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.Copy(tw, in)
	return err
}

// listArchive reads the whole archive and returns its entry names.
func listArchive(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	var names []string
	hasDB := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(io.Discard, tr); err != nil {
			return nil, err
		}
		names = append(names, h.Name)
		hasDB = hasDB || h.Name == dbEntry
	}
	if !hasDB {
		return nil, fmt.Errorf("archive contains no %s", dbEntry)
	}
	return names, nil
}

func prune(dir string, cutoff time.Time, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, archivePrefix) || !strings.HasSuffix(name, archiveSuffix) {
			continue
		}
		full := filepath.Join(dir, name)
		if full == keep {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			os.Remove(full)
		}
	}
}

// Restore extracts an archive into targetDir (which must not exist or be empty) as a ready-to-use
// data directory, then verifies it. It never touches any other directory, so it can be run
// safely while the live application is up.
func Restore(ctx context.Context, archive, targetDir string) (*store.Integrity, error) {
	if entries, err := os.ReadDir(targetDir); err == nil && len(entries) > 0 {
		return nil, fmt.Errorf("refusing to restore into non-empty directory %s", targetDir)
	}
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg || (h.Name != dbEntry && !blobEntry.MatchString(h.Name)) {
			return nil, fmt.Errorf("unexpected entry %q in archive", h.Name)
		}
		dst := filepath.Join(targetDir, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return nil, err
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return nil, err
		}
		if err := out.Close(); err != nil {
			return nil, err
		}
	}
	return store.CheckIntegrity(ctx, filepath.Join(targetDir, dbEntry), filepath.Join(targetDir, "uploads"))
}
