package service

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/superbyteone/lightbacklog/internal/files"
)

// Attachment describes an uploaded file. URL is same-origin and access-controlled.
type Attachment struct {
	ID        string    `json:"id"`
	TaskID    *string   `json:"task_id"`
	ProjectID string    `json:"project_id"`
	Filename  string    `json:"filename"`
	MIME      string    `json:"mime"`
	Size      int64     `json:"size"`
	Inline    bool      `json:"inline"`
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

const attachmentCols = `a.id, a.task_id, a.project_id, a.filename, a.mime, a.size, a.created_at`

func scanAttachment(sc interface{ Scan(...any) error }) (*Attachment, error) {
	var a Attachment
	var tid sql.NullString
	var created int64
	if err := sc.Scan(&a.ID, &tid, &a.ProjectID, &a.Filename, &a.MIME, &a.Size, &created); err != nil {
		return nil, err
	}
	if tid.Valid {
		a.TaskID = &tid.String
	}
	a.CreatedAt = msTime(created)
	a.URL = "/files/" + a.ID
	a.Inline = strings.HasPrefix(a.MIME, "image/")
	return &a, nil
}

// cleanFilename keeps only the base name and strips control characters.
func cleanFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if len([]rune(name)) > 200 {
		name = string([]rune(name)[:200])
	}
	if name == "" || name == "." || name == ".." {
		name = "file"
	}
	return name
}

// Upload stores a file for a project, optionally attached to a task (id or ref) in that project.
// Pass taskRef == "" for uploads made before a task exists (e.g. images pasted in the create dialog).
func (s *Service) Upload(ctx context.Context, p Principal, projectRef, taskRef, filename string, r io.Reader) (*Attachment, error) {
	if err := p.requireWrite(); err != nil {
		return nil, err
	}
	var projectID, taskID string
	if taskRef != "" {
		t, archived, err := findTask(ctx, s.db.R, p, taskRef, RoleEditor)
		if err != nil {
			return nil, err
		}
		if archived {
			return nil, errArchived()
		}
		projectID, taskID = t.Project.ID, t.ID
	} else {
		pr, err := resolveProject(ctx, s.db.R, p, projectRef, RoleEditor)
		if err != nil {
			return nil, err
		}
		if pr.Archived {
			return nil, errArchived()
		}
		projectID = pr.ID
	}
	staged, err := s.files.Stage(r, s.cfg.MaxUploadBytes)
	if err != nil {
		if errors.Is(err, files.ErrTooLarge) {
			return nil, &Error{Status: 413, Code: "file_too_large", Message: "file exceeds the upload size limit"}
		}
		return nil, err
	}
	if staged.Size == 0 {
		staged.Discard()
		return nil, invalid("file", "is empty")
	}
	kind, err := files.Detect(staged.Head, filename)
	if err != nil {
		staged.Discard()
		return nil, &Error{Status: 415, Code: "unsupported_file_type", Message: err.Error()}
	}
	if err := staged.Commit(); err != nil {
		return nil, err
	}
	a := &Attachment{ID: newID(), ProjectID: projectID, Filename: cleanFilename(filename), MIME: kind.MIME, Size: staged.Size, Inline: kind.Inline}
	now := s.nowMS()
	a.CreatedAt, a.URL = msTime(now), "/files/"+a.ID
	var tid any
	if taskID != "" {
		tid = taskID
		a.TaskID = &taskID
	}
	if _, err := s.db.W.ExecContext(ctx, `INSERT INTO attachments (id, task_id, project_id, uploader_id, filename, mime, size, sha256, created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		a.ID, tid, projectID, p.UserID, a.Filename, a.MIME, a.Size, staged.SHA256, now); err != nil {
		return nil, err
	}
	return a, nil
}

// ListAttachments returns a task's attachments.
func (s *Service) ListAttachments(ctx context.Context, p Principal, taskRef string) ([]Attachment, error) {
	t, _, err := findTask(ctx, s.db.R, p, taskRef, RoleViewer)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+attachmentCols+` FROM attachments a WHERE a.task_id = ? ORDER BY a.created_at, a.id`, t.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attachment{}
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// OpenAttachment authorizes access to a file and opens its content.
func (s *Service) OpenAttachment(ctx context.Context, p Principal, id string) (*Attachment, *os.File, error) {
	var sha string
	row := s.db.R.QueryRowContext(ctx, `SELECT `+attachmentCols+`, a.sha256 FROM attachments a WHERE a.id = ?`, strings.ToLower(id))
	var a Attachment
	var tid sql.NullString
	var created int64
	err := row.Scan(&a.ID, &tid, &a.ProjectID, &a.Filename, &a.MIME, &a.Size, &created, &sha)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, errNotFound("file")
	}
	if err != nil {
		return nil, nil, err
	}
	if _, err := roleIn(ctx, s.db.R, p, a.ProjectID); err != nil {
		return nil, nil, errNotFound("file")
	}
	if tid.Valid {
		a.TaskID = &tid.String
	}
	a.CreatedAt, a.URL, a.Inline = msTime(created), "/files/"+a.ID, strings.HasPrefix(a.MIME, "image/")
	f, err := s.files.Open(sha)
	if err != nil {
		return nil, nil, errNotFound("file")
	}
	return &a, f, nil
}

// DeleteAttachment removes an attachment (editor+).
func (s *Service) DeleteAttachment(ctx context.Context, p Principal, id string) error {
	if err := p.requireWrite(); err != nil {
		return err
	}
	var sha string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var projectID string
		err := tx.QueryRowContext(ctx, `SELECT project_id, sha256 FROM attachments WHERE id = ?`, strings.ToLower(id)).Scan(&projectID, &sha)
		if errors.Is(err, sql.ErrNoRows) {
			return errNotFound("file")
		}
		if err != nil {
			return err
		}
		if _, err := requireRole(ctx, tx, p, projectID, RoleEditor); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM attachments WHERE id = ?`, strings.ToLower(id))
		return err
	})
	if err != nil {
		return err
	}
	s.gcBlobs(ctx, []string{sha})
	return nil
}

// gcBlobs deletes blob files that no attachment row references any more (best effort).
func (s *Service) gcBlobs(ctx context.Context, shas []string) {
	for _, sha := range shas {
		var n int
		if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE sha256 = ?`, sha).Scan(&n); err == nil && n == 0 {
			_ = s.files.Remove(sha)
		}
	}
}

// purgeOrphanUploads removes uploads never attached to a task within the grace period.
func (s *Service) purgeOrphanUploads(ctx context.Context, olderThanMS int64) error {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, sha256 FROM attachments WHERE task_id IS NULL AND created_at < ?`, olderThanMS)
	if err != nil {
		return err
	}
	var ids, shas []string
	for rows.Next() {
		var id, sha string
		if err := rows.Scan(&id, &sha); err != nil {
			rows.Close()
			return err
		}
		ids, shas = append(ids, id), append(shas, sha)
	}
	rows.Close()
	for _, id := range ids {
		if _, err := s.db.W.ExecContext(ctx, `DELETE FROM attachments WHERE id = ?`, id); err != nil {
			return err
		}
	}
	s.gcBlobs(ctx, shas)
	return nil
}
