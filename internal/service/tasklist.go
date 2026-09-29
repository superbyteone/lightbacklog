package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// TaskFilter selects and orders tasks. Every slice filter is "any of"; different filters combine with AND.
type TaskFilter struct {
	Projects           []string // ids or keys
	Statuses           []string
	Types              []string // improvement, feature, bug, or "none" for tasks without a type
	Priorities         []string
	Labels             []string // names or ids
	LabelMatch         string   // "any" (default) or "all"
	Q                  string   // full-text search, or a task ref such as WEB-42
	DueBefore          string   // YYYY-MM-DD, inclusive
	DueAfter           string   // YYYY-MM-DD, inclusive
	HasDue             *bool
	IncludeArchived    bool // tasks of archived projects (implied when Projects is set)
	Sort               string
	Cursor             string
	Limit              int
	IncludeDescription bool
	IncludeTotal       bool
}

// TaskPage is one page of results.
type TaskPage struct {
	Tasks      []*Task `json:"data"`
	NextCursor string  `json:"next_cursor,omitempty"`
	Total      *int    `json:"total,omitempty"`
}

type sortSpec struct {
	expr string
	kind byte // 'i' int, 'f' float, 's' string
}

var sorts = map[string]sortSpec{
	"updated_at": {"t.updated_at", 'i'},
	"created_at": {"t.created_at", 'i'},
	// tasks that are not done count as never completed, so newest-first lists the recently completed ones first
	"completed_at": {"COALESCE(t.completed_at, 0)", 'i'},
	"title":        {"t.title COLLATE NOCASE", 's'},
	"due_date":     {"COALESCE(t.due_date, '9999-99-99')", 's'},
	"priority":     {"pr.position", 'i'},
	"status":       {"st.position", 'i'},
	"project":      {"p.name COLLATE NOCASE", 's'},
	"position":     {"t.position", 'f'},
	"type":         {"COALESCE(tt.position, 99)", 'i'}, // Improvement, Feature, Bug, then untyped
	// tasks without a sequence sort after every sequenced one when ascending
	"sequence": {"COALESCE(t.sequence, 2147483647)", 'i'},
}

type cursorPayload struct {
	Sort string          `json:"s"`
	V    json.RawMessage `json:"v"`
	ID   string          `json:"id"`
}

var ftsTokenRe = regexp.MustCompile(`[\p{L}\p{N}_]+`)

// ftsQuery turns free text into a safe FTS5 expression: every word is a quoted prefix term.
func ftsQuery(q string) string {
	toks := ftsTokenRe.FindAllString(q, 12)
	for i, t := range toks {
		toks[i] = `"` + t + `"*`
	}
	return strings.Join(toks, " ")
}

// ListTasks returns a page of tasks visible to the caller, with keyset pagination.
func (s *Service) ListTasks(ctx context.Context, p Principal, f TaskFilter) (*TaskPage, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	sortName, desc := strings.TrimPrefix(f.Sort, "-"), strings.HasPrefix(f.Sort, "-")
	if f.Sort == "" {
		sortName, desc = "updated_at", true
	}
	relevance := sortName == "relevance"
	spec, ok := sorts[sortName]
	if !ok && !relevance {
		return nil, invalid("sort", "must be one of updated_at, created_at, completed_at, title, due_date, priority, status, project, position, sequence, type, relevance (optionally prefixed with '-')")
	}
	dirStr := "ASC"
	if desc {
		dirStr = "DESC"
	}

	where, args := []string{}, []any{}
	access, aargs := projectAccessSQL(p, "t.project_id")
	where, args = append(where, access), append(args, aargs...)

	if len(f.Projects) > 0 {
		ids := make([]any, 0, len(f.Projects)*2)
		for _, r := range f.Projects {
			ids = append(ids, r, r)
		}
		where = append(where, "t.project_id IN (SELECT id FROM projects WHERE "+strings.TrimSuffix(strings.Repeat("id = ? OR key = ? OR ", len(f.Projects)), " OR ")+")")
		args = append(args, ids...)
	} else if !f.IncludeArchived {
		where = append(where, "p.archived_at IS NULL")
	}
	if len(f.Statuses) > 0 {
		vals := make([]any, 0, len(f.Statuses))
		for _, v := range f.Statuses {
			sl, e := normalizeSlug(ctx, s.db.R, "statuses", "status", v)
			if e != nil {
				return nil, e
			}
			vals = append(vals, sl)
		}
		where, args = append(where, "t.status IN ("+placeholders(len(vals))+")"), append(args, vals...)
	}
	if len(f.Types) > 0 {
		vals := make([]any, 0, len(f.Types))
		none := false
		for _, v := range f.Types {
			if strings.EqualFold(strings.TrimSpace(v), "none") {
				none = true
				continue
			}
			sl, e := normalizeSlug(ctx, s.db.R, "task_types", "type", v)
			if e != nil {
				return nil, e
			}
			vals = append(vals, sl)
		}
		cond := "t.type IN (" + placeholders(len(vals)) + ")"
		if len(vals) == 0 {
			cond = "1 = 0"
		}
		if none {
			cond = "(t.type IS NULL OR " + cond + ")"
		}
		where, args = append(where, cond), append(args, vals...)
	}
	if len(f.Priorities) > 0 {
		vals := make([]any, 0, len(f.Priorities))
		for _, v := range f.Priorities {
			sl, e := normalizeSlug(ctx, s.db.R, "priorities", "priority", v)
			if e != nil {
				return nil, e
			}
			vals = append(vals, sl)
		}
		where, args = append(where, "t.priority IN ("+placeholders(len(vals))+")"), append(args, vals...)
	}
	if len(f.Labels) > 0 {
		if f.LabelMatch == "all" {
			for _, lb := range f.Labels {
				where = append(where, `EXISTS (SELECT 1 FROM task_labels tl JOIN labels l ON l.id = tl.label_id WHERE tl.task_id = t.id AND (l.name = ? OR l.id = ?))`)
				args = append(args, lb, lb)
			}
		} else {
			conds := make([]string, 0, len(f.Labels))
			for _, lb := range f.Labels {
				conds = append(conds, "l.name = ? OR l.id = ?")
				args = append(args, lb, lb)
			}
			where = append(where, `EXISTS (SELECT 1 FROM task_labels tl JOIN labels l ON l.id = tl.label_id WHERE tl.task_id = t.id AND (`+strings.Join(conds, " OR ")+`))`)
		}
	}
	if f.DueBefore != "" {
		if e := validateDue(f.DueBefore); e != nil {
			return nil, invalid("due_before", "must be a date in YYYY-MM-DD format")
		}
		where, args = append(where, "t.due_date IS NOT NULL AND t.due_date <= ?"), append(args, f.DueBefore)
	}
	if f.DueAfter != "" {
		if e := validateDue(f.DueAfter); e != nil {
			return nil, invalid("due_after", "must be a date in YYYY-MM-DD format")
		}
		where, args = append(where, "t.due_date IS NOT NULL AND t.due_date >= ?"), append(args, f.DueAfter)
	}
	if f.HasDue != nil {
		if *f.HasDue {
			where = append(where, "t.due_date IS NOT NULL")
		} else {
			where = append(where, "t.due_date IS NULL")
		}
	}

	from := taskFrom
	var ftsArg any
	if q := strings.TrimSpace(f.Q); q != "" {
		if m := refRe.FindStringSubmatch(q); m != nil {
			n, _ := strconv.Atoi(m[2])
			where, args = append(where, "(p.key = ? AND t.number = ?)"), append(args, m[1], n)
			relevance = false
			if sortName == "relevance" {
				spec, sortName = sorts["updated_at"], "updated_at"
			}
		} else if fq := ftsQuery(q); fq != "" {
			from += ` JOIN task_fts f ON f.rowid = t.rowid `
			where = append(where, "task_fts MATCH ?")
			ftsArg = fq
			args = append(args, ftsArg)
		} else {
			return &TaskPage{Tasks: []*Task{}}, nil
		}
	} else if relevance {
		return nil, invalid("sort", "relevance requires a search query (q)")
	}

	base := from + " WHERE " + strings.Join(where, " AND ")
	page := &TaskPage{}
	if f.IncludeTotal {
		var n int
		if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) `+base, args...).Scan(&n); err != nil {
			return nil, err
		}
		page.Total = &n
	}

	qargs := append([]any{}, args...)
	sel := taskSelect
	orderBy := ""
	cmp := ">"
	if desc {
		cmp = "<"
	}
	if relevance {
		sel += ", 0 "
		orderBy = " ORDER BY f.rank, t.id"
		limit = min(limit, 50)
	} else {
		sel += ", " + spec.expr + " "
		if f.Cursor != "" {
			cp, err := decodeCursor(f.Cursor)
			if err != nil || cp.Sort != sortName+dirSuffix(desc) {
				return nil, invalid("cursor", "is invalid for this sort order; restart pagination without a cursor")
			}
			var v any
			switch spec.kind {
			case 'i':
				var n int64
				err = json.Unmarshal(cp.V, &n)
				v = n
			case 'f':
				var n float64
				err = json.Unmarshal(cp.V, &n)
				v = n
			default:
				var str string
				err = json.Unmarshal(cp.V, &str)
				v = str
			}
			if err != nil {
				return nil, invalid("cursor", "is invalid")
			}
			base += " AND (" + spec.expr + ", t.id) " + cmp + " (?, ?)"
			qargs = append(qargs, v, cp.ID)
		}
		orderBy = " ORDER BY " + spec.expr + " " + dirStr + ", t.id " + dirStr
	}
	rows, err := s.db.R.QueryContext(ctx, sel+base+orderBy+" LIMIT ?", append(qargs, limit+1)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []*Task{}
	var lastKey any
	keys := []any{}
	for rows.Next() {
		var key any
		t, _, err := scanTask(rows, f.IncludeDescription, &key)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if len(tasks) > limit {
		tasks = tasks[:limit]
		if !relevance {
			lastKey = keys[limit-1]
			raw, _ := json.Marshal(lastKey)
			if b, ok := lastKey.([]byte); ok {
				raw, _ = json.Marshal(string(b))
			}
			page.NextCursor = encodeCursor(cursorPayload{Sort: sortName + dirSuffix(desc), V: raw, ID: tasks[limit-1].ID})
		}
	}
	if err := attachLabels(ctx, s.db.R, tasks); err != nil {
		return nil, err
	}
	page.Tasks = tasks
	return page, nil
}

func dirSuffix(desc bool) string {
	if desc {
		return ":desc"
	}
	return ":asc"
}

func encodeCursor(c cursorPayload) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursorPayload, error) {
	var c cursorPayload
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}
