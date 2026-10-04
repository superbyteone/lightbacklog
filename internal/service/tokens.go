package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// APIToken is the metadata of an agent credential; the secret is never stored or listed.
type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scope      string     `json:"scope"`
	ProjectIDs []string   `json:"project_ids"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// CreateTokenInput describes a new token. Projects (ids or keys) optionally restricts it.
// ExpiresInDays is optional; 0 (the default) means the token never expires.
type CreateTokenInput struct {
	Name          string   `json:"name"`
	Scope         string   `json:"scope"`
	Projects      []string `json:"projects"`
	ExpiresInDays int      `json:"expires_in_days,omitempty"`
}

const tokenPrefix = "lb_"

// CreateToken issues a token for the calling user. The secret is returned exactly once.
func (s *Service) CreateToken(ctx context.Context, p Principal, in CreateTokenInput) (*APIToken, string, error) {
	if err := p.requireSession(); err != nil {
		return nil, "", err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		return nil, "", invalid("name", "must be 1-80 characters")
	}
	if in.Scope == "" {
		in.Scope = "write"
	}
	if in.Scope != "read" && in.Scope != "write" {
		return nil, "", invalid("scope", "must be 'read' or 'write'")
	}
	if in.ExpiresInDays < 0 {
		return nil, "", invalid("expires_in_days", "must be 0 (never expires) or a positive number of days")
	}
	secret := tokenPrefix + randomToken(32)
	tok := &APIToken{ID: newID(), Name: in.Name, Prefix: secret[:len(tokenPrefix)+6], Scope: in.Scope, ProjectIDs: []string{}}
	now := s.nowMS()
	tok.CreatedAt = msTime(now)
	var expiresAt sql.NullInt64
	if in.ExpiresInDays > 0 {
		expiresAt = sql.NullInt64{Valid: true, Int64: now + int64(in.ExpiresInDays)*86_400_000}
		t := msTime(expiresAt.Int64)
		tok.ExpiresAt = &t
	}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		for _, ref := range in.Projects {
			proj, err := resolveProject(ctx, tx, p, ref, RoleViewer)
			if err != nil {
				return err
			}
			tok.ProjectIDs = append(tok.ProjectIDs, proj.ID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO api_tokens (id, user_id, name, prefix, token_hash, scope, created_at, expires_at) VALUES (?,?,?,?,?,?,?,?)`,
			tok.ID, p.UserID, tok.Name, tok.Prefix, hashToken(secret), tok.Scope, now, expiresAt); err != nil {
			return err
		}
		for _, id := range tok.ProjectIDs {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO api_token_projects (token_id, project_id) VALUES (?,?)`, tok.ID, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return tok, secret, nil
}

// ListTokens returns the caller's tokens (including revoked ones, marked as such).
func (s *Service) ListTokens(ctx context.Context, p Principal) ([]APIToken, error) {
	if err := p.requireSession(); err != nil {
		return nil, err
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, name, prefix, scope, created_at, expires_at, last_used_at, revoked_at FROM api_tokens WHERE user_id = ? ORDER BY created_at DESC`, p.UserID)
	if err != nil {
		return nil, err
	}
	out := []APIToken{}
	for rows.Next() {
		var t APIToken
		var created int64
		var expires, used, revoked sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.Scope, &created, &expires, &used, &revoked); err != nil {
			rows.Close()
			return nil, err
		}
		t.CreatedAt, t.ExpiresAt, t.LastUsedAt, t.RevokedAt = msTime(created), msTimePtr(expires), msTimePtr(used), msTimePtr(revoked)
		t.ProjectIDs = []string{}
		out = append(out, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		prows, err := s.db.R.QueryContext(ctx, `SELECT project_id FROM api_token_projects WHERE token_id = ?`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for prows.Next() {
			var id string
			if err := prows.Scan(&id); err != nil {
				prows.Close()
				return nil, err
			}
			out[i].ProjectIDs = append(out[i].ProjectIDs, id)
		}
		prows.Close()
	}
	return out, nil
}

// RevokeToken disables one of the caller's tokens immediately.
func (s *Service) RevokeToken(ctx context.Context, p Principal, id string) error {
	if err := p.requireSession(); err != nil {
		return err
	}
	res, err := s.db.W.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ? AND user_id = ?`, s.nowMS(), id, p.UserID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound("token")
	}
	return nil
}

// RotateToken replaces one of the caller's tokens with a fresh secret, atomically: the old
// token stops working and the new one (with the same name, scope, projects and expiry policy)
// starts working in the same transaction, so there is never a gap with zero live credentials
// nor a window where both the old and new secret work because a second call failed.
func (s *Service) RotateToken(ctx context.Context, p Principal, id string) (*APIToken, string, error) {
	if err := p.requireSession(); err != nil {
		return nil, "", err
	}
	secret := tokenPrefix + randomToken(32)
	now := s.nowMS()
	tok := &APIToken{ID: newID(), Prefix: secret[:len(tokenPrefix)+6], CreatedAt: msTime(now), ProjectIDs: []string{}}
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var name, scope string
		var expires sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT name, scope, expires_at FROM api_tokens WHERE id = ? AND user_id = ? AND revoked_at IS NULL`, id, p.UserID).
			Scan(&name, &scope, &expires); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errNotFound("token")
			}
			return err
		}
		tok.Name, tok.Scope = name, scope
		if expires.Valid {
			t := msTime(expires.Int64)
			tok.ExpiresAt = &t
		}
		rows, err := tx.QueryContext(ctx, `SELECT project_id FROM api_token_projects WHERE token_id = ?`, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var pid string
			if err := rows.Scan(&pid); err != nil {
				rows.Close()
				return err
			}
			tok.ProjectIDs = append(tok.ProjectIDs, pid)
		}
		rows.Close()
		if _, err := tx.ExecContext(ctx, `INSERT INTO api_tokens (id, user_id, name, prefix, token_hash, scope, created_at, expires_at) VALUES (?,?,?,?,?,?,?,?)`,
			tok.ID, p.UserID, tok.Name, tok.Prefix, hashToken(secret), tok.Scope, now, expires); err != nil {
			return err
		}
		for _, pid := range tok.ProjectIDs {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO api_token_projects (token_id, project_id) VALUES (?,?)`, tok.ID, pid); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at = ? WHERE id = ?`, now, id)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	return tok, secret, nil
}

// AuthenticateToken resolves a bearer token to a Principal.
func (s *Service) AuthenticateToken(ctx context.Context, secret string) (Principal, error) {
	var p Principal
	var tokenID, scope string
	var admin int
	var revoked, disabled, expires sql.NullInt64
	var lastUsed sql.NullInt64
	err := s.db.R.QueryRowContext(ctx, `SELECT t.id, t.scope, t.revoked_at, t.expires_at, t.last_used_at, u.id, u.username, u.is_admin, u.disabled_at
		FROM api_tokens t JOIN users u ON u.id = t.user_id WHERE t.token_hash = ?`, hashToken(secret)).
		Scan(&tokenID, &scope, &revoked, &expires, &lastUsed, &p.UserID, &p.Username, &admin, &disabled)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (revoked.Valid || disabled.Valid)) {
		return p, errUnauthorized("invalid or revoked API token")
	}
	if err != nil {
		return p, err
	}
	if expires.Valid && s.nowMS() > expires.Int64 {
		return p, errUnauthorized("API token expired")
	}
	grant := &TokenGrant{ID: tokenID, Scope: scope}
	rows, err := s.db.R.QueryContext(ctx, `SELECT project_id FROM api_token_projects WHERE token_id = ?`, tokenID)
	if err != nil {
		return p, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return p, err
		}
		grant.ProjectIDs = append(grant.ProjectIDs, id)
	}
	rows.Close()
	if now := s.nowMS(); !lastUsed.Valid || now-lastUsed.Int64 > 60_000 {
		_, _ = s.db.W.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, now, tokenID)
	}
	p.IsAdmin = false // tokens never carry administrator rights
	p.Token = grant
	return p, nil
}
