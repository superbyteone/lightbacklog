package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Opt distinguishes "field absent" from "explicitly null" in partial updates.
type Opt[T any] struct {
	Set   bool
	Value *T
}

func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// TaskProject is the compact project reference embedded in tasks.
type TaskProject struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// Task is the API view of a task. Description is omitted from list responses unless requested.
type Task struct {
	ID          string      `json:"id"`
	Ref         string      `json:"ref"`
	Number      int         `json:"number"`
	Project     TaskProject `json:"project"`
	Title       string      `json:"title"`
	Description *string     `json:"description,omitempty"`
	Status      string      `json:"status"`
	Priority    string      `json:"priority"`
	DueDate     *string     `json:"due_date"`
	Labels      []Label     `json:"labels"`
	ExternalRef *string     `json:"external_ref"`
	Version     int64       `json:"version"`
	Position    float64     `json:"position"`
	// Sequence is an optional whole number giving the order in which tasks should be tackled (1 first).
	Sequence *int `json:"sequence"`
	// Type is the optional kind of task ("improvement", "feature" or "bug"); null when not classified.
	Type      *string   `json:"type"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// CompletedAt is when the task last arrived in a done status; null while it is not done.
	CompletedAt *time.Time `json:"completed_at"`
}

const (
	maxTitleRunes = 500
	maxDescRunes  = 200_000
	maxLabels     = 20
)

var refRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]{1,7})-(\d{1,9})$`)

const taskSelect = `SELECT t.id, t.number, t.title, t.description_md, t.status, t.priority, t.due_date, t.external_ref,
	t.version, t.position, t.sequence, t.type, t.created_at, t.updated_at, t.completed_at, p.id, p.key, p.name, p.color, p.archived_at `

const taskFrom = ` FROM tasks t JOIN projects p ON p.id = t.project_id
	JOIN statuses st ON st.slug = t.status JOIN priorities pr ON pr.slug = t.priority
	LEFT JOIN task_types tt ON tt.slug = t.type `

// statusIsDone reports whether the status with this slug counts as done.
func statusIsDone(ctx context.Context, q querier, slug string) (bool, error) {
	var done bool
	err := q.QueryRowContext(ctx, `SELECT is_done FROM statuses WHERE slug = ?`, slug).Scan(&done)
	return done, err
}

type taskScanner interface{ Scan(...any) error }

func scanTask(sc taskScanner, withDescription bool, extra ...any) (*Task, bool, error) {
	var t Task
	var desc string
	var due, ext sql.NullString
	var seq sql.NullInt64
	var typ sql.NullString
	var created, updated int64
	var completed sql.NullInt64
	var archived sql.NullInt64
	dest := []any{&t.ID, &t.Number, &t.Title, &desc, &t.Status, &t.Priority, &due, &ext, &t.Version, &t.Position, &seq, &typ, &created, &updated, &completed,
		&t.Project.ID, &t.Project.Key, &t.Project.Name, &t.Project.Color, &archived}
	dest = append(dest, extra...)
	if err := sc.Scan(dest...); err != nil {
		return nil, false, err
	}
	if withDescription {
		t.Description = &desc
	}
	if due.Valid {
		t.DueDate = &due.String
	}
	if ext.Valid {
		t.ExternalRef = &ext.String
	}
	if seq.Valid {
		n := int(seq.Int64)
		t.Sequence = &n
	}
	if typ.Valid {
		t.Type = &typ.String
	}
	t.Ref = t.Project.Key + "-" + strconv.Itoa(t.Number)
	t.CreatedAt, t.UpdatedAt = msTime(created), msTime(updated)
	t.CompletedAt = msTimePtr(completed)
	t.Labels = []Label{}
	return &t, archived.Valid, nil
}

// attachLabels fills Labels for a batch of tasks with a single query.
func attachLabels(ctx context.Context, q querier, tasks []*Task) error {
	if len(tasks) == 0 {
		return nil
	}
	idx := make(map[string]*Task, len(tasks))
	args := make([]any, 0, len(tasks))
	for _, t := range tasks {
		idx[t.ID] = t
		args = append(args, t.ID)
	}
	rows, err := q.QueryContext(ctx, `SELECT tl.task_id, l.id, l.name, l.color FROM task_labels tl JOIN labels l ON l.id = tl.label_id
		WHERE tl.task_id IN (`+placeholders(len(args))+`) ORDER BY l.name COLLATE NOCASE`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tid string
		var l Label
		if err := rows.Scan(&tid, &l.ID, &l.Name, &l.Color); err != nil {
			return err
		}
		idx[tid].Labels = append(idx[tid].Labels, l)
	}
	return rows.Err()
}

// findTask loads a task by UUID or "KEY-12" ref and enforces the caller's minimum role in its project.
// The task is returned with its description and labels; archived reports the project state.
func findTask(ctx context.Context, q querier, p Principal, ref string, min Role) (*Task, bool, error) {
	ref = strings.TrimSpace(ref)
	var row *sql.Row
	if m := refRe.FindStringSubmatch(ref); m != nil {
		n, _ := strconv.Atoi(m[2])
		row = q.QueryRowContext(ctx, taskSelect+taskFrom+`WHERE p.key = ? AND t.number = ?`, m[1], n)
	} else {
		row = q.QueryRowContext(ctx, taskSelect+taskFrom+`WHERE t.id = ?`, ref)
	}
	t, archived, err := scanTask(row, true)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, errNotFound("task")
	}
	if err != nil {
		return nil, false, err
	}
	if _, err := requireRole(ctx, q, p, t.Project.ID, min); err != nil {
		return nil, false, err
	}
	if err := attachLabels(ctx, q, []*Task{t}); err != nil {
		return nil, false, err
	}
	return t, archived, nil
}

// GetTask returns one task by id or ref, including its description.
func (s *Service) GetTask(ctx context.Context, p Principal, ref string) (*Task, error) {
	t, _, err := findTask(ctx, s.db.R, p, ref, RoleViewer)
	return t, err
}

func errArchived() *Error {
	return errConflict("project_archived", "the project is archived; restore it before changing its tasks")
}

// normalizeSlug maps user input ("In Progress", "in-progress", "HIGH") to a canonical slug.
func normalizeSlug(ctx context.Context, q querier, table, field, val string) (string, *Error) {
	val = strings.TrimSpace(val)
	cand := strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(val))
	var slug string
	err := q.QueryRowContext(ctx, `SELECT slug FROM `+table+` WHERE slug = ? OR LOWER(name) = LOWER(?)`, cand, val).Scan(&slug)
	if errors.Is(err, sql.ErrNoRows) {
		valid := []string{}
		if rows, e := q.QueryContext(ctx, `SELECT slug FROM `+table+` ORDER BY position`); e == nil {
			for rows.Next() {
				var sl string
				if rows.Scan(&sl) == nil {
					valid = append(valid, sl)
				}
			}
			rows.Close()
		}
		return "", invalid(field, "unknown value %q; valid values: %s", val, strings.Join(valid, ", "))
	}
	if err != nil {
		return "", &Error{Status: 500, Code: "internal", Message: err.Error()}
	}
	return slug, nil
}

func validateDue(v string) *Error {
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return invalid("due_date", "must be a date in YYYY-MM-DD format")
	}
	return nil
}

func validateTitle(v string) (string, *Error) {
	v = strings.Join(strings.Fields(v), " ")
	if v == "" {
		return "", invalid("title", "is required")
	}
	if utf8.RuneCountInString(v) > maxTitleRunes {
		return "", invalid("title", "must be at most %d characters", maxTitleRunes)
	}
	return v, nil
}

func validateDescription(v string) *Error {
	if utf8.RuneCountInString(v) > maxDescRunes {
		return invalid("description", "must be at most %d characters", maxDescRunes)
	}
	return nil
}

const maxSequence = 1_000_000

func validateSequence(v int) *Error {
	if v < 0 || v > maxSequence {
		return invalid("sequence", "must be a whole number from 0 to %d (1 is worked on first), or empty for none", maxSequence)
	}
	return nil
}

func validateExternalRef(v string) *Error {
	if v == "" || utf8.RuneCountInString(v) > 200 {
		return invalid("external_ref", "must be 1-200 characters")
	}
	return nil
}

// resolveLabels turns label names into label rows, creating missing ones when allowed.
func resolveLabels(ctx context.Context, q querier, names []string, create bool) ([]Label, *Error) {
	if len(names) > maxLabels {
		return nil, invalid("labels", "at most %d labels per task", maxLabels)
	}
	out := make([]Label, 0, len(names))
	seen := map[string]bool{}
	for _, raw := range names {
		name, e := normalizeLabel(raw)
		if e != nil {
			return nil, e
		}
		if seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		var l Label
		err := q.QueryRowContext(ctx, `SELECT id, name, color FROM labels WHERE name = ?`, name).Scan(&l.ID, &l.Name, &l.Color)
		if errors.Is(err, sql.ErrNoRows) {
			if !create {
				return nil, invalid("labels", "unknown label %q (create it first or set create_missing_labels)", name)
			}
			lp, err := ensureLabel(ctx, q, name, "")
			if err != nil {
				return nil, &Error{Status: 500, Code: "internal", Message: err.Error()}
			}
			l = *lp
		} else if err != nil {
			return nil, &Error{Status: 500, Code: "internal", Message: err.Error()}
		}
		out = append(out, l)
	}
	return out, nil
}

var fileRefRe = regexp.MustCompile(`/files/([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`)

// linkReferencedUploads attaches uploads embedded in a description (e.g. pasted screenshots
// uploaded before the task existed) to the task, provided they belong to the same project.
func linkReferencedUploads(ctx context.Context, q querier, projectID, taskID, description string) error {
	matches := fileRefRe.FindAllStringSubmatch(description, 50)
	for _, m := range matches {
		if _, err := q.ExecContext(ctx, `UPDATE attachments SET task_id = ? WHERE id = ? AND project_id = ? AND task_id IS NULL`,
			taskID, strings.ToLower(m[1]), projectID); err != nil {
			return err
		}
	}
	return nil
}

func nextPosition(ctx context.Context, q querier, projectID, status string) (float64, error) {
	var max sql.NullFloat64
	if err := q.QueryRowContext(ctx, `SELECT MAX(position) FROM tasks WHERE project_id = ? AND status = ?`, projectID, status).Scan(&max); err != nil {
		return 0, err
	}
	return max.Float64 + 1000, nil
}

// CreateTaskInput describes a new task. Project (id or key) and Title are required.
type CreateTaskInput struct {
	Project             string   `json:"project"`
	Title               string   `json:"title"`
	Description         string   `json:"description"`
	Status              string   `json:"status"`
	Priority            string   `json:"priority"`
	DueDate             *string  `json:"due_date"`
	Labels              []string `json:"labels"`
	CreateMissingLabels *bool    `json:"create_missing_labels"`
	ExternalRef         *string  `json:"external_ref"`
	Position            *float64 `json:"position"`
	Sequence            *int     `json:"sequence"`
	Type                *string  `json:"type"` // optional: improvement, feature or bug
}

// CreateTask creates a task. If ExternalRef is set and a task with that reference already
// exists in the project, that task is returned unchanged (created=false): retries and
// re-runs by agents never produce duplicates.
func (s *Service) CreateTask(ctx context.Context, p Principal, in CreateTaskInput) (*Task, bool, error) {
	if err := p.requireWrite(); err != nil {
		return nil, false, err
	}
	title, e := validateTitle(in.Title)
	if e != nil {
		return nil, false, e
	}
	if e := validateDescription(in.Description); e != nil {
		return nil, false, e
	}
	if in.DueDate != nil {
		if e := validateDue(*in.DueDate); e != nil {
			return nil, false, e
		}
	}
	if in.ExternalRef != nil {
		if e := validateExternalRef(*in.ExternalRef); e != nil {
			return nil, false, e
		}
	}
	if in.Sequence != nil {
		if e := validateSequence(*in.Sequence); e != nil {
			return nil, false, e
		}
	}
	createLabels := in.CreateMissingLabels == nil || *in.CreateMissingLabels
	var out *Task
	created := false
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		proj, err := resolveProject(ctx, tx, p, in.Project, RoleEditor)
		if err != nil {
			return err
		}
		if proj.Archived {
			return errArchived()
		}
		if in.ExternalRef != nil {
			var existing string
			err := tx.QueryRowContext(ctx, `SELECT id FROM tasks WHERE project_id = ? AND external_ref = ?`, proj.ID, *in.ExternalRef).Scan(&existing)
			if err == nil {
				out, _, err = findTask(ctx, tx, p, existing, RoleViewer)
				return err
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		status, priority := in.Status, in.Priority
		if status == "" {
			status = "todo"
		}
		if priority == "" {
			priority = "low"
		}
		if status, e = normalizeSlug(ctx, tx, "statuses", "status", status); e != nil {
			return e
		}
		if priority, e = normalizeSlug(ctx, tx, "priorities", "priority", priority); e != nil {
			return e
		}
		var typ any
		if in.Type != nil && strings.TrimSpace(*in.Type) != "" {
			tp, e := normalizeSlug(ctx, tx, "task_types", "type", *in.Type)
			if e != nil {
				return e
			}
			typ = tp
		}
		labels, e := resolveLabels(ctx, tx, in.Labels, createLabels)
		if e != nil {
			return e
		}
		var number int
		if err := tx.QueryRowContext(ctx, `UPDATE projects SET next_number = next_number + 1 WHERE id = ? RETURNING next_number - 1`, proj.ID).Scan(&number); err != nil {
			return err
		}
		pos := 0.0
		if in.Position != nil {
			pos = *in.Position
		} else if pos, err = nextPosition(ctx, tx, proj.ID, status); err != nil {
			return err
		}
		id, now := newID(), s.nowMS()
		var due, ext, seq, completed any
		if isDone, e := statusIsDone(ctx, tx, status); e != nil {
			return e
		} else if isDone {
			completed = now
		}
		if in.DueDate != nil {
			due = *in.DueDate
		}
		if in.ExternalRef != nil {
			ext = *in.ExternalRef
		}
		if in.Sequence != nil {
			seq = *in.Sequence
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tasks (id, project_id, number, title, description_md, status, priority, due_date, external_ref, version, position, sequence, type, created_by, created_at, updated_at, completed_at)
			VALUES (?,?,?,?,?,?,?,?,?,1,?,?,?,?,?,?,?)`, id, proj.ID, number, title, in.Description, status, priority, due, ext, pos, seq, typ, p.UserID, now, now, completed); err != nil {
			return err
		}
		for _, l := range labels {
			if _, err := tx.ExecContext(ctx, `INSERT INTO task_labels (task_id, label_id) VALUES (?,?)`, id, l.ID); err != nil {
				return err
			}
		}
		if err := linkReferencedUploads(ctx, tx, proj.ID, id, in.Description); err != nil {
			return err
		}
		created = true
		out, _, err = findTask(ctx, tx, p, id, RoleViewer)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if created {
		s.publish(ctx, out.Project.ID, Event{Type: "task.created", TaskID: out.ID, Ref: out.Ref})
	}
	return out, created, nil
}

// UpdateTaskInput is a partial update; absent fields are left unchanged.
type UpdateTaskInput struct {
	Title               *string     `json:"title"`
	Description         *string     `json:"description"`
	Status              *string     `json:"status"`
	Priority            *string     `json:"priority"`
	DueDate             Opt[string] `json:"due_date"`
	Labels              *[]string   `json:"labels"`
	AddLabels           []string    `json:"add_labels"`
	RemoveLabels        []string    `json:"remove_labels"`
	CreateMissingLabels *bool       `json:"create_missing_labels"`
	ExternalRef         Opt[string] `json:"external_ref"`
	Position            *float64    `json:"position"`
	Sequence            Opt[int]    `json:"sequence"` // null clears it
	Type                Opt[string] `json:"type"`     // null (or empty) clears it
	Project             *string     `json:"project"`  // moves the task to another project
	Version             *int64      `json:"version"`  // optimistic concurrency check
}

// UpdateTask applies a partial update. If Version is set and does not match the stored
// version, it fails with version_conflict and returns the current task in the error.
func (s *Service) UpdateTask(ctx context.Context, p Principal, ref string, in UpdateTaskInput) (*Task, error) {
	if err := p.requireWrite(); err != nil {
		return nil, err
	}
	createLabels := in.CreateMissingLabels == nil || *in.CreateMissingLabels
	var out *Task
	var prevVersion int64
	var prevProject string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		cur, archived, err := findTask(ctx, tx, p, ref, RoleEditor)
		if err != nil {
			return err
		}
		if archived {
			return errArchived()
		}
		prevVersion, prevProject = cur.Version, cur.Project.ID
		if in.Version != nil && *in.Version != cur.Version {
			ce := errConflict("version_conflict", "the task was modified by someone else; re-read it and retry")
			ce.Current = cur
			return ce
		}
		sets, args := []string{}, []any{}
		set := func(col string, v any) { sets, args = append(sets, col+" = ?"), append(args, v) }
		changed := false
		projectID := cur.Project.ID

		if in.Project != nil {
			dest, err := resolveProject(ctx, tx, p, *in.Project, RoleEditor)
			if err != nil {
				return err
			}
			if dest.ID != cur.Project.ID {
				if dest.Archived {
					return errArchived()
				}
				extRef := cur.ExternalRef
				if in.ExternalRef.Set {
					extRef = in.ExternalRef.Value
				}
				if extRef != nil {
					var n int
					if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE project_id = ? AND external_ref = ?`, dest.ID, *extRef).Scan(&n); err != nil {
						return err
					}
					if n > 0 {
						return invalid("external_ref", "already used by another task in the target project")
					}
				}
				var number int
				if err := tx.QueryRowContext(ctx, `UPDATE projects SET next_number = next_number + 1 WHERE id = ? RETURNING next_number - 1`, dest.ID).Scan(&number); err != nil {
					return err
				}
				set("project_id", dest.ID)
				set("number", number)
				if _, err := tx.ExecContext(ctx, `UPDATE attachments SET project_id = ? WHERE task_id = ?`, dest.ID, cur.ID); err != nil {
					return err
				}
				projectID, changed = dest.ID, true
			}
		}
		if in.Title != nil {
			title, e := validateTitle(*in.Title)
			if e != nil {
				return e
			}
			if title != cur.Title {
				set("title", title)
				changed = true
			}
		}
		if in.Description != nil {
			if e := validateDescription(*in.Description); e != nil {
				return e
			}
			if *in.Description != *cur.Description {
				set("description_md", *in.Description)
				changed = true
			}
		}
		newStatus := cur.Status
		if in.Status != nil {
			st, e := normalizeSlug(ctx, tx, "statuses", "status", *in.Status)
			if e != nil {
				return e
			}
			if st != cur.Status {
				set("status", st)
				newStatus, changed = st, true
				wasDone, e := statusIsDone(ctx, tx, cur.Status)
				if e != nil {
					return e
				}
				nowDone, e := statusIsDone(ctx, tx, st)
				if e != nil {
					return e
				}
				// completed_at is the latest arrival in a done status; leaving done clears it.
				switch {
				case nowDone && !wasDone:
					set("completed_at", s.nowMS())
				case !nowDone && wasDone:
					set("completed_at", nil)
				}
			}
		}
		if in.Priority != nil {
			pr, e := normalizeSlug(ctx, tx, "priorities", "priority", *in.Priority)
			if e != nil {
				return e
			}
			if pr != cur.Priority {
				set("priority", pr)
				changed = true
			}
		}
		if in.DueDate.Set {
			var v any
			var nv *string
			if in.DueDate.Value != nil {
				if e := validateDue(*in.DueDate.Value); e != nil {
					return e
				}
				v, nv = *in.DueDate.Value, in.DueDate.Value
			}
			if !equalStrPtr(nv, cur.DueDate) {
				set("due_date", v)
				changed = true
			}
		}
		if in.ExternalRef.Set {
			var v any
			if in.ExternalRef.Value != nil {
				if e := validateExternalRef(*in.ExternalRef.Value); e != nil {
					return e
				}
				v = *in.ExternalRef.Value
			}
			if !equalStrPtr(in.ExternalRef.Value, cur.ExternalRef) {
				set("external_ref", v)
				changed = true
			}
		}
		if in.Type.Set {
			var v any
			var nv *string
			if in.Type.Value != nil && strings.TrimSpace(*in.Type.Value) != "" {
				tp, e := normalizeSlug(ctx, tx, "task_types", "type", *in.Type.Value)
				if e != nil {
					return e
				}
				v, nv = tp, &tp
			}
			if !equalStrPtr(nv, cur.Type) {
				set("type", v)
				changed = true
			}
		}
		if in.Sequence.Set {
			var v any
			if in.Sequence.Value != nil {
				if e := validateSequence(*in.Sequence.Value); e != nil {
					return e
				}
				v = *in.Sequence.Value
			}
			if (in.Sequence.Value == nil) != (cur.Sequence == nil) || (in.Sequence.Value != nil && *in.Sequence.Value != *cur.Sequence) {
				set("sequence", v)
				changed = true
			}
		}
		if in.Position != nil && *in.Position != cur.Position {
			set("position", *in.Position)
			changed = true
		} else if in.Position == nil && (newStatus != cur.Status || projectID != cur.Project.ID) {
			pos, err := nextPosition(ctx, tx, projectID, newStatus)
			if err != nil {
				return err
			}
			set("position", pos)
		}

		labelsChanged, err := s.applyLabelChanges(ctx, tx, cur, in, createLabels)
		if err != nil {
			return err
		}
		if changed || labelsChanged {
			set("version", cur.Version+1)
			set("updated_at", s.nowMS())
			args = append(args, cur.ID)
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
				if isUnique(err) {
					return invalid("external_ref", "already used by another task in this project")
				}
				return err
			}
		}
		if in.Description != nil {
			if err := linkReferencedUploads(ctx, tx, projectID, cur.ID, *in.Description); err != nil {
				return err
			}
		}
		out, _, err = findTask(ctx, tx, p, cur.ID, RoleViewer)
		return err
	})
	if err != nil {
		return nil, err
	}
	if out.Version != prevVersion {
		s.publish(ctx, out.Project.ID, Event{Type: "task.updated", TaskID: out.ID, Ref: out.Ref})
		if prevProject != out.Project.ID { // moved: members of the old project must drop it too
			s.publish(ctx, prevProject, Event{Type: "task.deleted", TaskID: out.ID})
		}
	}
	return out, nil
}

func equalStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// applyLabelChanges handles labels (replace), add_labels and remove_labels; it reports whether anything changed.
func (s *Service) applyLabelChanges(ctx context.Context, tx *sql.Tx, cur *Task, in UpdateTaskInput, create bool) (bool, error) {
	if in.Labels == nil && len(in.AddLabels) == 0 && len(in.RemoveLabels) == 0 {
		return false, nil
	}
	have := map[string]Label{}
	for _, l := range cur.Labels {
		have[l.ID] = l
	}
	want := map[string]Label{}
	for id, l := range have {
		want[id] = l
	}
	if in.Labels != nil {
		ls, e := resolveLabels(ctx, tx, *in.Labels, create)
		if e != nil {
			return false, e
		}
		want = map[string]Label{}
		for _, l := range ls {
			want[l.ID] = l
		}
	}
	if len(in.AddLabels) > 0 {
		ls, e := resolveLabels(ctx, tx, in.AddLabels, create)
		if e != nil {
			return false, e
		}
		for _, l := range ls {
			want[l.ID] = l
		}
	}
	if len(in.RemoveLabels) > 0 {
		for _, raw := range in.RemoveLabels {
			name, e := normalizeLabel(raw)
			if e != nil {
				return false, e
			}
			for id, l := range want {
				if strings.EqualFold(l.Name, name) {
					delete(want, id)
				}
			}
		}
	}
	if len(want) > maxLabels {
		return false, invalid("labels", "at most %d labels per task", maxLabels)
	}
	changed := false
	for id := range have {
		if _, ok := want[id]; !ok {
			if _, err := tx.ExecContext(ctx, `DELETE FROM task_labels WHERE task_id = ? AND label_id = ?`, cur.ID, id); err != nil {
				return false, err
			}
			changed = true
		}
	}
	for id := range want {
		if _, ok := have[id]; !ok {
			if _, err := tx.ExecContext(ctx, `INSERT INTO task_labels (task_id, label_id) VALUES (?,?)`, cur.ID, id); err != nil {
				return false, err
			}
			changed = true
		}
	}
	return changed, nil
}

// DeleteTask permanently removes a task and its attachments. version, if non-nil, must match.
func (s *Service) DeleteTask(ctx context.Context, p Principal, ref string, version *int64) error {
	if err := p.requireWrite(); err != nil {
		return err
	}
	var shas []string
	var deleted *Task
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		cur, archived, err := findTask(ctx, tx, p, ref, RoleEditor)
		if err != nil {
			return err
		}
		if archived {
			return errArchived()
		}
		deleted = cur
		if version != nil && *version != cur.Version {
			ce := errConflict("version_conflict", "the task was modified by someone else; re-read it and retry")
			ce.Current = cur
			return ce
		}
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT sha256 FROM attachments WHERE task_id = ?`, cur.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var sha string
			if err := rows.Scan(&sha); err != nil {
				rows.Close()
				return err
			}
			shas = append(shas, sha)
		}
		rows.Close()
		_, err = tx.ExecContext(ctx, `DELETE FROM tasks WHERE id = ?`, cur.ID)
		return err
	})
	if err != nil {
		return err
	}
	s.gcBlobs(ctx, shas)
	s.publish(ctx, deleted.Project.ID, Event{Type: "task.deleted", TaskID: deleted.ID, Ref: deleted.Ref})
	return nil
}
