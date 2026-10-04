package service

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Project is the API view of a project with the caller's role and task counts.
type Project struct {
	ID          string     `json:"id"`
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Color       string     `json:"color"`
	Archived    bool       `json:"archived"`
	ArchivedAt  *time.Time `json:"archived_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Role        Role       `json:"role"`
	Favorite    bool       `json:"favorite"`
	OpenTasks   int        `json:"open_tasks"`
	TotalTasks  int        `json:"total_tasks"`
}

var (
	keyRe   = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,7}$`)
	colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

var palette = []string{"#2563eb", "#7c3aed", "#db2777", "#dc2626", "#ea580c", "#ca8a04", "#16a34a", "#0d9488", "#0891b2", "#4b5563"}

const projectSelect = `SELECT p.id, p.key, p.name, p.description, p.color, p.archived_at, p.created_at, p.updated_at, m.role, m.favorite,
	COALESCE(c.open_tasks, 0), COALESCE(c.total_tasks, 0)
	FROM projects p
	JOIN project_members m ON m.project_id = p.id AND m.user_id = ?
	LEFT JOIN (SELECT t.project_id, COUNT(*) AS total_tasks,
	                  SUM(CASE WHEN s.is_done = 0 THEN 1 ELSE 0 END) AS open_tasks
	           FROM tasks t JOIN statuses s ON s.slug = t.status GROUP BY t.project_id) c ON c.project_id = p.id `

func scanProject(sc interface{ Scan(...any) error }) (*Project, error) {
	var pr Project
	var archived sql.NullInt64
	var created, updated int64
	var role string
	var favorite int
	if err := sc.Scan(&pr.ID, &pr.Key, &pr.Name, &pr.Description, &pr.Color, &archived, &created, &updated, &role, &favorite, &pr.OpenTasks, &pr.TotalTasks); err != nil {
		return nil, err
	}
	pr.ArchivedAt = msTimePtr(archived)
	pr.Archived = archived.Valid
	pr.CreatedAt, pr.UpdatedAt, pr.Role = msTime(created), msTime(updated), Role(role)
	pr.Favorite = favorite != 0
	return &pr, nil
}

// resolveProject finds a project by id or key and enforces the minimum role.
func resolveProject(ctx context.Context, q querier, p Principal, ref string, min Role) (*Project, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, invalid("project", "is required")
	}
	var id string
	err := q.QueryRowContext(ctx, `SELECT id FROM projects WHERE id = ? OR key = ?`, ref, ref).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound("project")
	}
	if err != nil {
		return nil, err
	}
	if _, err := requireRole(ctx, q, p, id, min); err != nil {
		return nil, err
	}
	pr, err := scanProject(q.QueryRowContext(ctx, projectSelect+`WHERE p.id = ?`, p.UserID, id))
	if err != nil {
		return nil, err
	}
	return pr, nil
}

// ProjectFilter selects projects in a listing.
type ProjectFilter struct {
	Q        string
	Archived string // "active" (default), "archived" or "all"
}

// ListProjects returns the projects the caller can access, alphabetically.
func (s *Service) ListProjects(ctx context.Context, p Principal, f ProjectFilter) ([]Project, error) {
	where := []string{}
	args := []any{p.UserID}
	switch f.Archived {
	case "", "active":
		where = append(where, "p.archived_at IS NULL")
	case "archived":
		where = append(where, "p.archived_at IS NOT NULL")
	case "all":
	default:
		return nil, invalid("archived", "must be active, archived or all")
	}
	if p.Token != nil && len(p.Token.ProjectIDs) > 0 {
		where = append(where, "p.id IN ("+placeholders(len(p.Token.ProjectIDs))+")")
		for _, id := range p.Token.ProjectIDs {
			args = append(args, id)
		}
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		like := "%" + escapeLike(strings.ToLower(q)) + "%"
		where = append(where, `(LOWER(p.name) LIKE ? ESCAPE '\' OR LOWER(p.key) LIKE ? ESCAPE '\' OR LOWER(p.description) LIKE ? ESCAPE '\')`)
		args = append(args, like, like, like)
	}
	query := projectSelect
	if len(where) > 0 {
		query += "WHERE " + strings.Join(where, " AND ") + " "
	}
	query += "ORDER BY p.name COLLATE NOCASE, p.id"
	rows, err := s.db.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		pr, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *pr)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// GetProject returns one project by id or key.
func (s *Service) GetProject(ctx context.Context, p Principal, ref string) (*Project, error) {
	return resolveProject(ctx, s.db.R, p, ref, RoleViewer)
}

// CreateProjectInput describes a new project. Key and Color are derived when omitted.
type CreateProjectInput struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
}

func validateName(field, v string, max int) (string, *Error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", invalid(field, "is required")
	}
	if utf8.RuneCountInString(v) > max {
		return "", invalid(field, "must be at most %d characters", max)
	}
	return v, nil
}

// deriveKey builds a short uppercase key from a project name (initials, or leading letters).
func deriveKey(name string) string {
	var words []string
	cur := []rune{}
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = cur[:0]
		}
	}
	for _, r := range name {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			cur = append(cur, unicode.ToUpper(r))
		} else {
			flush()
		}
	}
	flush()
	key := ""
	if len(words) >= 2 {
		for _, w := range words {
			key += string([]rune(w)[0])
			if len(key) == 4 {
				break
			}
		}
	} else if len(words) == 1 {
		key = words[0]
		if len(key) > 4 {
			key = key[:4]
		}
	}
	if len(key) < 2 || key[0] < 'A' || key[0] > 'Z' {
		key = "PRJ"
	}
	return key
}

// CreateProject creates a project and makes the caller its owner.
func (s *Service) CreateProject(ctx context.Context, p Principal, in CreateProjectInput) (*Project, error) {
	if err := p.requireWrite(); err != nil {
		return nil, err
	}
	if p.Token != nil && len(p.Token.ProjectIDs) > 0 {
		return nil, errForbidden("this API token is restricted to specific projects and cannot create new ones")
	}
	name, e := validateName("name", in.Name, 100)
	if e != nil {
		return nil, e
	}
	if utf8.RuneCountInString(in.Description) > 5000 {
		return nil, invalid("description", "must be at most 5000 characters")
	}
	color := strings.TrimSpace(in.Color)
	if color != "" && !colorRe.MatchString(color) {
		return nil, invalid("color", "must be a #rrggbb hex color")
	}
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	explicitKey := key != ""
	if !explicitKey {
		key = deriveKey(name)
	} else if !keyRe.MatchString(key) {
		return nil, invalid("key", "must be 2-8 letters/digits starting with a letter")
	}
	now := s.nowMS()
	var id string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		final := key
		for i := 2; ; i++ {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE key = ?`, final).Scan(&exists); err != nil {
				return err
			}
			if exists == 0 {
				break
			}
			if explicitKey {
				return errConflict("key_taken", "a project with key "+key+" already exists")
			}
			suffix := itoa(i)
			base := key
			if len(base)+len(suffix) > 8 {
				base = base[:8-len(suffix)]
			}
			final = base + suffix
		}
		if color == "" {
			color = palette[hashIndex(final, len(palette))]
		}
		id = newID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO projects (id, key, name, description, color, created_by, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
			id, final, name, in.Description, color, p.UserID, now, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO project_members (project_id, user_id, role) VALUES (?,?, 'owner')`, id, p.UserID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return resolveProject(ctx, s.db.R, p, id, RoleViewer)
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	b := []byte{}
	for i > 0 {
		b = append([]byte{digits[i%10]}, b...)
		i /= 10
	}
	return string(b)
}

func hashIndex(s string, n int) int {
	h := 0
	for _, r := range s {
		h = (h*31 + int(r)) & 0x7fffffff
	}
	return h % n
}

// UpdateProjectInput carries optional changes; nil fields are left untouched.
type UpdateProjectInput struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Color       *string `json:"color"`
}

// UpdateProject edits project metadata (owner only). The key is immutable so task refs stay valid.
func (s *Service) UpdateProject(ctx context.Context, p Principal, ref string, in UpdateProjectInput) (*Project, error) {
	if err := p.requireWrite(); err != nil {
		return nil, err
	}
	var id string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		pr, err := resolveProject(ctx, tx, p, ref, RoleOwner)
		if err != nil {
			return err
		}
		id = pr.ID
		sets, args := []string{}, []any{}
		if in.Name != nil {
			name, e := validateName("name", *in.Name, 100)
			if e != nil {
				return e
			}
			sets, args = append(sets, "name = ?"), append(args, name)
		}
		if in.Description != nil {
			if utf8.RuneCountInString(*in.Description) > 5000 {
				return invalid("description", "must be at most 5000 characters")
			}
			sets, args = append(sets, "description = ?"), append(args, *in.Description)
		}
		if in.Color != nil {
			if !colorRe.MatchString(*in.Color) {
				return invalid("color", "must be a #rrggbb hex color")
			}
			sets, args = append(sets, "color = ?"), append(args, *in.Color)
		}
		if len(sets) == 0 {
			return nil
		}
		sets, args = append(sets, "updated_at = ?"), append(args, s.nowMS())
		args = append(args, pr.ID)
		_, err = tx.ExecContext(ctx, `UPDATE projects SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish(ctx, id, Event{Type: "project.changed"})
	return resolveProject(ctx, s.db.R, p, id, RoleViewer)
}

// SetProjectFavorite stars or unstars a project for the caller. It is a personal preference, not a
// project-wide setting, so any member may set it for themselves regardless of role.
func (s *Service) SetProjectFavorite(ctx context.Context, p Principal, ref string, favorite bool) (*Project, error) {
	if err := p.requireWrite(); err != nil {
		return nil, err
	}
	var id string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		pr, err := resolveProject(ctx, tx, p, ref, RoleViewer)
		if err != nil {
			return err
		}
		id = pr.ID
		val := 0
		if favorite {
			val = 1
		}
		_, err = tx.ExecContext(ctx, `UPDATE project_members SET favorite = ? WHERE project_id = ? AND user_id = ?`, val, pr.ID, p.UserID)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish(ctx, id, Event{Type: "project.changed"})
	return resolveProject(ctx, s.db.R, p, id, RoleViewer)
}

// SetProjectArchived archives or restores a project (owner only). Archived projects
// keep their tasks, which are hidden from default listings.
func (s *Service) SetProjectArchived(ctx context.Context, p Principal, ref string, archived bool) (*Project, error) {
	if err := p.requireWrite(); err != nil {
		return nil, err
	}
	var id string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		pr, err := resolveProject(ctx, tx, p, ref, RoleOwner)
		if err != nil {
			return err
		}
		id = pr.ID
		var val any
		if archived {
			val = s.nowMS()
		}
		_, err = tx.ExecContext(ctx, `UPDATE projects SET archived_at = ?, updated_at = ? WHERE id = ?`, val, s.nowMS(), pr.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.publish(ctx, id, Event{Type: "project.changed"})
	return resolveProject(ctx, s.db.R, p, id, RoleViewer)
}

// Member is a user's membership in a project.
type Member struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Role     Role   `json:"role"`
}

// ListMembers lists a project's members (any member may see them).
func (s *Service) ListMembers(ctx context.Context, p Principal, ref string) ([]Member, error) {
	pr, err := resolveProject(ctx, s.db.R, p, ref, RoleViewer)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT u.id, u.username, m.role FROM project_members m JOIN users u ON u.id = m.user_id
		WHERE m.project_id = ? ORDER BY u.username`, pr.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		var role string
		if err := rows.Scan(&m.UserID, &m.Username, &role); err != nil {
			return nil, err
		}
		m.Role = Role(role)
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetMember adds a user to a project or changes their role (owner only).
func (s *Service) SetMember(ctx context.Context, p Principal, ref, username string, role Role) error {
	if err := p.requireWrite(); err != nil {
		return err
	}
	if role != RoleViewer && role != RoleEditor && role != RoleOwner {
		return invalid("role", "must be viewer, editor or owner")
	}
	var projectID, memberID string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		pr, err := resolveProject(ctx, tx, p, ref, RoleOwner)
		if err != nil {
			return err
		}
		projectID = pr.ID
		var uid string
		var disabled sql.NullInt64
		err = tx.QueryRowContext(ctx, `SELECT id, disabled_at FROM users WHERE username = ?`, strings.TrimSpace(username)).Scan(&uid, &disabled)
		if errors.Is(err, sql.ErrNoRows) || disabled.Valid {
			return invalid("username", "no such active user")
		}
		if err != nil {
			return err
		}
		if role != RoleOwner {
			if err := ensureAnotherOwner(ctx, tx, pr.ID, uid); err != nil {
				return err
			}
		}
		memberID = uid
		_, err = tx.ExecContext(ctx, `INSERT INTO project_members (project_id, user_id, role) VALUES (?,?,?)
			ON CONFLICT(project_id, user_id) DO UPDATE SET role = excluded.role`, pr.ID, uid, string(role))
		return err
	})
	if err != nil {
		return err
	}
	s.publish(ctx, projectID, Event{Type: "project.changed"})
	s.publishToUser(ctx, memberID, Event{Type: "resync"})
	return nil
}

// RemoveMember removes a user from a project (owner only, or a user leaving on their own).
func (s *Service) RemoveMember(ctx context.Context, p Principal, ref, userID string) error {
	if err := p.requireWrite(); err != nil {
		return err
	}
	var projectID string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		min := RoleOwner
		if userID == p.UserID {
			min = RoleViewer
		}
		pr, err := resolveProject(ctx, tx, p, ref, min)
		if err != nil {
			return err
		}
		projectID = pr.ID
		if err := ensureAnotherOwner(ctx, tx, pr.ID, userID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, pr.ID, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errNotFound("member")
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.publish(ctx, projectID, Event{Type: "project.changed"})
	s.publishToUser(ctx, userID, Event{Type: "resync"})
	return nil
}

// ensureAnotherOwner guards against leaving a project with no owner when userID currently owns it.
func ensureAnotherOwner(ctx context.Context, q querier, projectID, userID string) error {
	var isOwner int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ? AND role = 'owner'`, projectID, userID).Scan(&isOwner); err != nil {
		return err
	}
	if isOwner == 0 {
		return nil
	}
	var others int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id <> ? AND role = 'owner'`, projectID, userID).Scan(&others); err != nil {
		return err
	}
	if others == 0 {
		return errConflict("last_owner", "a project must keep at least one owner")
	}
	return nil
}

// DeleteProject permanently removes an archived project with all its tasks, attachments and
// memberships. It is deliberately hard to trigger by accident: owner only, browser session only
// (never an API token), the project must already be archived, and confirm must repeat its key.
func (s *Service) DeleteProject(ctx context.Context, p Principal, ref, confirm string) error {
	if err := p.requireSession(); err != nil {
		return err
	}
	var shas, members []string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		pr, err := resolveProject(ctx, tx, p, ref, RoleOwner)
		if err != nil {
			return err
		}
		if !pr.Archived {
			return errConflict("project_not_archived", "archive the project first; only archived projects can be deleted permanently")
		}
		if !strings.EqualFold(strings.TrimSpace(confirm), pr.Key) {
			return invalid("confirm", "type the project key %s to confirm the deletion", pr.Key)
		}
		rows, err := tx.QueryContext(ctx, `SELECT DISTINCT sha256 FROM attachments WHERE project_id = ?`, pr.ID)
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
		mrows, err := tx.QueryContext(ctx, `SELECT user_id FROM project_members WHERE project_id = ?`, pr.ID)
		if err != nil {
			return err
		}
		for mrows.Next() {
			var id string
			if err := mrows.Scan(&id); err != nil {
				mrows.Close()
				return err
			}
			members = append(members, id)
		}
		mrows.Close()
		// A token restricted to projects is "restricted" only while it has rows in api_token_projects;
		// deleting the last one would silently turn it into an all-projects token. Revoke those.
		if _, err := tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = COALESCE(revoked_at, ?)
			WHERE id IN (SELECT token_id FROM api_token_projects WHERE project_id = ?)
			  AND (SELECT COUNT(*) FROM api_token_projects x WHERE x.token_id = api_tokens.id) = 1`, s.nowMS(), pr.ID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, pr.ID) // cascades to tasks, labels links, members, attachments
		return err
	})
	if err != nil {
		return err
	}
	s.gcBlobs(ctx, shas)
	for _, id := range members {
		s.publishToUser(ctx, id, Event{Type: "resync"})
	}
	return nil
}
