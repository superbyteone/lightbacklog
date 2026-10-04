package service

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Status is a workflow state tasks can be in.
type Status struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	Color    string `json:"color"`
	IsDone   bool   `json:"is_done"`
}

// Priority ranks the urgency of a task.
type Priority struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	Color    string `json:"color"`
}

// Label is an instance-wide tag reusable across projects.
type Label struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// TaskType classifies a task (Improvement, Feature, Bug). Optional on every task.
type TaskType struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	Color    string `json:"color"`
}

// Meta is the vocabulary agents and the UI need to build valid requests.
type Meta struct {
	Statuses   []Status   `json:"statuses"`
	Priorities []Priority `json:"priorities"`
	Types      []TaskType `json:"types"`
}

// GetMeta returns statuses and priorities in display order.
func (s *Service) GetMeta(ctx context.Context) (*Meta, error) {
	m := &Meta{Statuses: []Status{}, Priorities: []Priority{}, Types: []TaskType{}}
	rows, err := s.db.R.QueryContext(ctx, `SELECT slug, name, position, color, is_done FROM statuses ORDER BY position`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var st Status
		var done int
		if err := rows.Scan(&st.Slug, &st.Name, &st.Position, &st.Color, &done); err != nil {
			rows.Close()
			return nil, err
		}
		st.IsDone = done == 1
		m.Statuses = append(m.Statuses, st)
	}
	rows.Close()
	prows, err := s.db.R.QueryContext(ctx, `SELECT slug, name, position, color FROM priorities ORDER BY position`)
	if err != nil {
		return nil, err
	}
	for prows.Next() {
		var pr Priority
		if err := prows.Scan(&pr.Slug, &pr.Name, &pr.Position, &pr.Color); err != nil {
			prows.Close()
			return nil, err
		}
		m.Priorities = append(m.Priorities, pr)
	}
	prows.Close()
	trows, err := s.db.R.QueryContext(ctx, `SELECT slug, name, position, color FROM task_types ORDER BY position`)
	if err != nil {
		return nil, err
	}
	defer trows.Close()
	for trows.Next() {
		var tt TaskType
		if err := trows.Scan(&tt.Slug, &tt.Name, &tt.Position, &tt.Color); err != nil {
			return nil, err
		}
		m.Types = append(m.Types, tt)
	}
	return m, trows.Err()
}

// ListLabels returns every label alphabetically.
func (s *Service) ListLabels(ctx context.Context, p Principal) ([]Label, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, name, color FROM labels ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Label{}
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l.ID, &l.Name, &l.Color); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

var labelNameRe = regexp.MustCompile(`^[^\s,#@!/][^,]*$`)

func normalizeLabel(name string) (string, *Error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || utf8.RuneCountInString(name) > 40 || !labelNameRe.MatchString(name) {
		return "", invalid("labels", "label names must be 1-40 characters, without commas, and not start with # @ ! /")
	}
	return name, nil
}

// CreateLabel creates a label, returning the existing one if the name (case-insensitive) is taken.
func (s *Service) CreateLabel(ctx context.Context, p Principal, name, color string) (*Label, error) {
	if err := p.requireWrite(); err != nil {
		return nil, err
	}
	name, e := normalizeLabel(name)
	if e != nil {
		return nil, e
	}
	if color != "" && !colorRe.MatchString(color) {
		return nil, invalid("color", "must be a #rrggbb hex color")
	}
	var out *Label
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		l, err := ensureLabel(ctx, tx, name, color)
		out = l
		return err
	})
	return out, err
}

func ensureLabel(ctx context.Context, q querier, name, color string) (*Label, error) {
	var l Label
	err := q.QueryRowContext(ctx, `SELECT id, name, color FROM labels WHERE name = ?`, name).Scan(&l.ID, &l.Name, &l.Color)
	if err == nil {
		return &l, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	if color == "" {
		color = palette[hashIndex(strings.ToLower(name), len(palette))]
	}
	l = Label{ID: newID(), Name: name, Color: color}
	_, err = q.ExecContext(ctx, `INSERT INTO labels (id, name, color) VALUES (?,?,?)`, l.ID, l.Name, l.Color)
	return &l, err
}

// UpdateLabel renames or recolors a label.
func (s *Service) UpdateLabel(ctx context.Context, p Principal, id string, name, color *string) (*Label, error) {
	if err := p.requireWrite(); err != nil {
		return nil, err
	}
	var out Label
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT id, name, color FROM labels WHERE id = ?`, id).Scan(&out.ID, &out.Name, &out.Color); err != nil {
			if err == sql.ErrNoRows {
				return errNotFound("label")
			}
			return err
		}
		if name != nil {
			n, e := normalizeLabel(*name)
			if e != nil {
				return e
			}
			out.Name = n
		}
		if color != nil {
			if !colorRe.MatchString(*color) {
				return invalid("color", "must be a #rrggbb hex color")
			}
			out.Color = *color
		}
		_, err := tx.ExecContext(ctx, `UPDATE labels SET name = ?, color = ? WHERE id = ?`, out.Name, out.Color, id)
		if isUnique(err) {
			return errConflict("label_exists", "a label with that name already exists")
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteLabel removes a label from every task (administrators only, as it affects all users).
func (s *Service) DeleteLabel(ctx context.Context, p Principal, id string) error {
	if !p.IsAdmin || p.Token != nil {
		return errForbidden("administrator access required")
	}
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM labels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound("label")
	}
	return nil
}
