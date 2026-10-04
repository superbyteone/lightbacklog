package service

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/superbyteone/lightbacklog/internal/files"
	"github.com/superbyteone/lightbacklog/internal/store"
	"github.com/google/uuid"
)

// Config carries tunables that affect business rules.
type Config struct {
	MaxUploadBytes int64
	SessionTTL     time.Duration // absolute session lifetime
	SessionIdle    time.Duration // idle timeout
	// MCPAuditRetention is how long mcp_audit_log rows are kept before PurgeExpired deletes them.
	MCPAuditRetention time.Duration
}

func (c Config) withDefaults() Config {
	if c.MaxUploadBytes <= 0 {
		c.MaxUploadBytes = 10 << 20
	}
	if c.SessionTTL <= 0 {
		c.SessionTTL = 30 * 24 * time.Hour
	}
	if c.SessionIdle <= 0 {
		c.SessionIdle = 7 * 24 * time.Hour
	}
	if c.MCPAuditRetention <= 0 {
		c.MCPAuditRetention = 90 * 24 * time.Hour
	}
	return c
}

// Service is the single entry point for all application operations.
type Service struct {
	db      *store.DB
	files   *files.Store
	cfg     Config
	now     func() time.Time
	hashSem chan struct{} // bounds concurrent argon2 hashes (memory-hungry by design)

	fakeOnce      sync.Once
	fakeHashValue string

	hub hub // live-update subscribers
}

func New(db *store.DB, fs *files.Store, cfg Config) *Service {
	return &Service{db: db, files: fs, cfg: cfg.withDefaults(), now: time.Now, hashSem: make(chan struct{}, 1)}
}

func (s *Service) nowMS() int64 { return s.now().UnixMilli() }

func newID() string {
	id, err := uuid.NewV7()
	if err != nil {
		panic(err) // only fails if the system RNG is broken
	}
	return id.String()
}

func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func msTimePtr(ms sql.NullInt64) *time.Time {
	if !ms.Valid {
		return nil
	}
	t := msTime(ms.Int64)
	return &t
}

// withTx runs fn inside a write transaction on the single writer connection.
func (s *Service) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Ping verifies database connectivity (used by health checks).
func (s *Service) Ping(ctx context.Context) error { return s.db.Ping(ctx) }

// PrincipalFor builds a session-level principal for a named user; used only by the local
// admin CLI, which already has full access to the database file.
func (s *Service) PrincipalFor(ctx context.Context, username string) (Principal, error) {
	var p Principal
	var admin int
	var disabled sql.NullInt64
	err := s.db.R.QueryRowContext(ctx, `SELECT id, username, is_admin, disabled_at FROM users WHERE username = ?`, username).Scan(&p.UserID, &p.Username, &admin, &disabled)
	if err == sql.ErrNoRows || disabled.Valid {
		return p, errNotFound("user")
	}
	p.IsAdmin = admin == 1
	return p, err
}
