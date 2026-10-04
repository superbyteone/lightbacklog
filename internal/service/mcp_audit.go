package service

import (
	"context"
	"database/sql"
	"time"
)

// MCPAuditEntry is one recorded call through the MCP server: who, which tool, when, and the
// outcome. Deliberately carries no arguments/payload -- task content can be sensitive, and this
// is a forensic trail, not a debug log.
type MCPAuditEntry struct {
	TokenID    string
	UserID     string
	Tool       string
	Status     string // "ok" or "error"
	ErrorCode  string
	ClientIP   string
	DurationMS int64
	CreatedAt  time.Time
}

// RecordMCPAudit persists a batch of entries in one transaction. It is meant to be called by
// the MCP server's own batching writer (see internal/mcp), not synchronously on every tool
// call, so it never adds write-lock latency to an individual call.
func (s *Service) RecordMCPAudit(ctx context.Context, entries []MCPAuditEntry) error {
	if len(entries) == 0 {
		return nil
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, e := range entries {
			var errCode sql.NullString
			if e.ErrorCode != "" {
				errCode = sql.NullString{String: e.ErrorCode, Valid: true}
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO mcp_audit_log (id, token_id, user_id, tool, status, error_code, client_ip, duration_ms, created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
				newID(), e.TokenID, e.UserID, e.Tool, e.Status, errCode, e.ClientIP, e.DurationMS, e.CreatedAt.UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListMCPAudit returns recent MCP audit entries newest-first, optionally restricted to one
// token. It backs the `mcp-audit list` CLI command; there is no REST endpoint for it yet.
func (s *Service) ListMCPAudit(ctx context.Context, tokenID string, limit int) ([]MCPAuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT token_id, user_id, tool, status, error_code, client_ip, duration_ms, created_at FROM mcp_audit_log`
	args := []any{}
	if tokenID != "" {
		q += ` WHERE token_id = ?`
		args = append(args, tokenID)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MCPAuditEntry
	for rows.Next() {
		var e MCPAuditEntry
		var tokenID, userID, errCode sql.NullString
		var created int64
		if err := rows.Scan(&tokenID, &userID, &e.Tool, &e.Status, &errCode, &e.ClientIP, &e.DurationMS, &created); err != nil {
			return nil, err
		}
		e.TokenID, e.UserID, e.ErrorCode = tokenID.String, userID.String, errCode.String
		e.CreatedAt = msTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// purgeMCPAudit deletes audit rows older than the configured retention window.
func (s *Service) purgeMCPAudit(ctx context.Context, now int64) error {
	_, err := s.db.W.ExecContext(ctx, `DELETE FROM mcp_audit_log WHERE created_at < ?`, now-s.cfg.MCPAuditRetention.Milliseconds())
	return err
}
