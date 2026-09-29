package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Role is a user's permission level inside one project.
type Role string

const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
	RoleOwner  Role = "owner"
)

func (r Role) rank() int {
	switch r {
	case RoleOwner:
		return 3
	case RoleEditor:
		return 2
	case RoleViewer:
		return 1
	}
	return 0
}

// AtLeast reports whether r grants at least the permissions of min.
func (r Role) AtLeast(min Role) bool { return r.rank() >= min.rank() }

// Principal is the authenticated caller: a user via browser session, or a user acting
// through an API token (which can only narrow, never widen, the user's own access).
type Principal struct {
	UserID   string
	Username string
	IsAdmin  bool
	Token    *TokenGrant // nil for browser sessions
}

// TokenGrant is the restriction set carried by an API token.
type TokenGrant struct {
	ID         string
	Scope      string   // "read" or "write"
	ProjectIDs []string // empty means every project the user can access
}

// CanWrite reports whether the principal may perform mutations at all.
func (p Principal) CanWrite() bool { return p.Token == nil || p.Token.Scope == "write" }

// IsSession reports whether the principal authenticated with a browser session.
func (p Principal) IsSession() bool { return p.Token == nil }

func (p Principal) requireWrite() error {
	if !p.CanWrite() {
		return errScope()
	}
	return nil
}

func (p Principal) requireSession() error {
	if p.Token != nil {
		return errForbidden("this action is not available to API tokens")
	}
	return nil
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// projectAccessSQL returns a SQL predicate (and args) restricting <col> to projects the
// principal may read: membership, further narrowed by the token's project allow-list.
// Listings must use this in SQL rather than filtering results afterwards.
func projectAccessSQL(p Principal, col string) (string, []any) {
	sqlText := col + ` IN (SELECT project_id FROM project_members WHERE user_id = ?)`
	args := []any{p.UserID}
	if p.Token != nil && len(p.Token.ProjectIDs) > 0 {
		sqlText += ` AND ` + col + ` IN (` + placeholders(len(p.Token.ProjectIDs)) + `)`
		for _, id := range p.Token.ProjectIDs {
			args = append(args, id)
		}
	}
	return "(" + sqlText + ")", args
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// roleIn returns the principal's role in a project, or errNotFound when the project does
// not exist or the principal has no access (deliberately indistinguishable).
func roleIn(ctx context.Context, q querier, p Principal, projectID string) (Role, error) {
	if p.Token != nil && len(p.Token.ProjectIDs) > 0 {
		allowed := false
		for _, id := range p.Token.ProjectIDs {
			if id == projectID {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", errNotFound("project")
		}
	}
	var role string
	err := q.QueryRowContext(ctx, `SELECT role FROM project_members WHERE project_id = ? AND user_id = ?`,
		projectID, p.UserID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotFound("project")
	}
	if err != nil {
		return "", err
	}
	return Role(role), nil
}

// requireRole loads the principal's role in a project and enforces a minimum.
func requireRole(ctx context.Context, q querier, p Principal, projectID string, min Role) (Role, error) {
	role, err := roleIn(ctx, q, p, projectID)
	if err != nil {
		return "", err
	}
	if !role.AtLeast(min) {
		return "", errForbidden("you need " + string(min) + " access to this project")
	}
	return role, nil
}
