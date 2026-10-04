package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// User is the public view of an account.
type User struct {
	ID         string     `json:"id"`
	Username   string     `json:"username"`
	Email      string     `json:"email"`
	IsAdmin    bool       `json:"is_admin"`
	CreatedAt  time.Time  `json:"created_at"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
}

// Argon2id parameters: the OWASP minimum recommendation (19 MiB, 2 passes, 1 lane). Memory is
// kept modest because the container is small; hashes are also serialized (see hashSem).
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

func (s *Service) hashPassword(pw string) (string, error) {
	s.hashSem <- struct{}{}
	defer func() { <-s.hashSem }()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	debug.FreeOSMemory() // hand the hashing arena back to the OS right away
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func (s *Service) verifyPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	s.hashSem <- struct{}{}
	defer func() { <-s.hashSem }()
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	debug.FreeOSMemory()
	return subtle.ConstantTimeCompare(got, want) == 1
}

func validateUsername(u string) *Error {
	n := utf8.RuneCountInString(u)
	if n < 2 || n > 40 {
		return invalid("username", "must be 2-40 characters")
	}
	for _, r := range u {
		if !(r == '_' || r == '-' || r == '.' || r == '@' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return invalid("username", "may contain only letters, digits, '.', '_', '-' and '@'")
		}
	}
	return nil
}

func validatePassword(pw string) *Error {
	if utf8.RuneCountInString(pw) < 10 {
		return invalid("password", "must be at least 10 characters")
	}
	if len(pw) > 200 {
		return invalid("password", "must be at most 200 bytes")
	}
	return nil
}

// UserCount reports how many accounts exist (used to detect first-run bootstrap).
func (s *Service) UserCount(ctx context.Context) (int, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUserInput describes a new account.
type CreateUserInput struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

// CreateUser creates an account. It is called by the CLI (no principal) and by admins.
func (s *Service) CreateUser(ctx context.Context, in CreateUserInput) (*User, error) {
	in.Username = strings.TrimSpace(in.Username)
	in.Email = strings.TrimSpace(in.Email)
	if e := validateUsername(in.Username); e != nil {
		return nil, e
	}
	if e := validatePassword(in.Password); e != nil {
		return nil, e
	}
	if len(in.Email) > 200 {
		return nil, invalid("email", "must be at most 200 characters")
	}
	hash, err := s.hashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	u := &User{ID: newID(), Username: in.Username, Email: in.Email, IsAdmin: in.IsAdmin}
	now := s.nowMS()
	u.CreatedAt = msTime(now)
	_, err = s.db.W.ExecContext(ctx, `INSERT INTO users (id, username, email, password_hash, is_admin, created_at) VALUES (?,?,?,?,?,?)`,
		u.ID, u.Username, u.Email, hash, boolInt(in.IsAdmin), now)
	if err != nil {
		if isUnique(err) {
			return nil, errConflict("username_taken", "that username already exists")
		}
		return nil, err
	}
	return u, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ListUsers returns all accounts (admin only).
func (s *Service) ListUsers(ctx context.Context, p Principal) ([]User, error) {
	if !p.IsAdmin || p.Token != nil {
		return nil, errForbidden("administrator access required")
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, username, email, is_admin, created_at, disabled_at FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		var created int64
		var disabled sql.NullInt64
		var admin int
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &admin, &created, &disabled); err != nil {
			return nil, err
		}
		u.IsAdmin = admin == 1
		u.CreatedAt = msTime(created)
		u.DisabledAt = msTimePtr(disabled)
		out = append(out, u)
	}
	return out, rows.Err()
}

// AdminCreateUser lets an administrator add an account.
func (s *Service) AdminCreateUser(ctx context.Context, p Principal, in CreateUserInput) (*User, error) {
	if !p.IsAdmin || p.Token != nil {
		return nil, errForbidden("administrator access required")
	}
	return s.CreateUser(ctx, in)
}

// SetUserDisabled enables or disables an account (admin only). Disabling revokes sessions.
func (s *Service) SetUserDisabled(ctx context.Context, p Principal, userID string, disabled bool) error {
	if !p.IsAdmin || p.Token != nil {
		return errForbidden("administrator access required")
	}
	if disabled && userID == p.UserID {
		return errConflict("cannot_disable_self", "you cannot disable your own account")
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var val any
		if disabled {
			val = s.nowMS()
		}
		res, err := tx.ExecContext(ctx, `UPDATE users SET disabled_at = ? WHERE id = ?`, val, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errNotFound("user")
		}
		if disabled {
			_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
		}
		return err
	})
}

// ChangePassword verifies the current password, sets a new one and revokes every other session.
func (s *Service) ChangePassword(ctx context.Context, p Principal, current, next, keepSessionID string) error {
	if err := p.requireSession(); err != nil {
		return err
	}
	if e := validatePassword(next); e != nil {
		return e
	}
	var hash string
	if err := s.db.R.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, p.UserID).Scan(&hash); err != nil {
		return err
	}
	if !s.verifyPassword(current, hash) {
		return invalid("current_password", "is incorrect")
	}
	newHash, err := s.hashPassword(next)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, newHash, p.UserID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id_hash <> ?`, p.UserID, hashToken(keepSessionID))
		return err
	})
}

func errValidationCode(code, msg string) *Error {
	e := errUnauthorized(msg)
	e.Code = code
	return e
}

// SetPassword resets a user's password from the CLI (recovery path; revokes all sessions).
func (s *Service) SetPassword(ctx context.Context, username, password string) error {
	if e := validatePassword(password); e != nil {
		return e
	}
	hash, err := s.hashPassword(password)
	if err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE username = ?`, hash, username)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errNotFound("user")
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE username = ?)`, username)
		return err
	})
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func randomToken(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Session is what a successful login returns: the secret cookie value plus its expiry.
type Session struct {
	Secret    string
	ExpiresAt time.Time
	User      *User
}

// Login verifies credentials and opens a server-side session.
func (s *Service) Login(ctx context.Context, username, password, userAgent string) (*Session, error) {
	var u User
	var hash string
	var admin int
	var created int64
	var disabled sql.NullInt64
	err := s.db.R.QueryRowContext(ctx, `SELECT id, username, email, is_admin, created_at, disabled_at, password_hash FROM users WHERE username = ?`,
		strings.TrimSpace(username)).Scan(&u.ID, &u.Username, &u.Email, &admin, &created, &disabled, &hash)
	bad := errValidationCode("invalid_credentials", "invalid username or password")
	if errors.Is(err, sql.ErrNoRows) {
		s.verifyPassword(password, s.fakeHash()) // equalize timing
		return nil, bad
	}
	if err != nil {
		return nil, err
	}
	if !s.verifyPassword(password, hash) || disabled.Valid {
		return nil, bad
	}
	u.IsAdmin = admin == 1
	u.CreatedAt = msTime(created)
	secret := randomToken(32)
	now := s.nowMS()
	expires := now + s.cfg.SessionTTL.Milliseconds()
	if len(userAgent) > 200 {
		userAgent = userAgent[:200]
	}
	if _, err := s.db.W.ExecContext(ctx, `INSERT INTO sessions (id_hash, user_id, created_at, last_seen_at, expires_at, user_agent) VALUES (?,?,?,?,?,?)`,
		hashToken(secret), u.ID, now, now, expires, userAgent); err != nil {
		return nil, err
	}
	return &Session{Secret: secret, ExpiresAt: msTime(expires), User: &u}, nil
}

func (s *Service) fakeHash() string {
	s.fakeOnce.Do(func() { s.fakeHashValue, _ = s.hashPassword("not-a-real-password") })
	return s.fakeHashValue
}

// Logout deletes the session identified by its cookie secret.
func (s *Service) Logout(ctx context.Context, secret string) error {
	_, err := s.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hashToken(secret))
	return err
}

// AuthenticateSession resolves a session cookie to a Principal, enforcing idle and absolute expiry
// and sliding the idle window forward at most once a minute.
func (s *Service) AuthenticateSession(ctx context.Context, secret string) (Principal, error) {
	var p Principal
	var admin int
	var last, expires int64
	var disabled sql.NullInt64
	h := hashToken(secret)
	err := s.db.R.QueryRowContext(ctx, `SELECT u.id, u.username, u.is_admin, u.disabled_at, se.last_seen_at, se.expires_at
		FROM sessions se JOIN users u ON u.id = se.user_id WHERE se.id_hash = ?`, h).
		Scan(&p.UserID, &p.Username, &admin, &disabled, &last, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return p, errUnauthorized("not logged in")
	}
	if err != nil {
		return p, err
	}
	now := s.nowMS()
	if disabled.Valid || now > expires || now-last > s.cfg.SessionIdle.Milliseconds() {
		_, _ = s.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, h)
		return p, errUnauthorized("session expired")
	}
	if now-last > 60_000 {
		_, _ = s.db.W.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id_hash = ?`, now, h)
	}
	p.IsAdmin = admin == 1
	return p, nil
}

// GetUser returns the caller's own account.
func (s *Service) GetUser(ctx context.Context, p Principal) (*User, error) {
	var u User
	var admin int
	var created int64
	err := s.db.R.QueryRowContext(ctx, `SELECT id, username, email, is_admin, created_at FROM users WHERE id = ?`, p.UserID).
		Scan(&u.ID, &u.Username, &u.Email, &admin, &created)
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin == 1
	u.CreatedAt = msTime(created)
	return &u, nil
}

// PurgeExpired removes expired sessions, stale idempotency keys and orphaned unattached uploads.
func (s *Service) PurgeExpired(ctx context.Context) error {
	now := s.nowMS()
	if _, err := s.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ? OR last_seen_at < ?`, now, now-s.cfg.SessionIdle.Milliseconds()); err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE created_at < ?`, now-24*3600*1000); err != nil {
		return err
	}
	if err := s.purgeMCPAudit(ctx, now); err != nil {
		return err
	}
	return s.purgeOrphanUploads(ctx, now-24*3600*1000)
}

func isUnique(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "constraint failed: UNIQUE"))
}

// IdempotentResponse is a stored response for a replayed request.
type IdempotentResponse struct {
	Status int
	Body   []byte
}

// IdempotencyLookup returns the stored response for (user, key). If the key was used with a
// different request body, it fails with idempotency_key_reuse.
func (s *Service) IdempotencyLookup(ctx context.Context, userID, key, requestHash string) (*IdempotentResponse, error) {
	var hash string
	var status int
	var body string
	err := s.db.R.QueryRowContext(ctx, `SELECT request_hash, status_code, response_body FROM idempotency_keys WHERE user_id = ? AND key = ?`, userID, key).
		Scan(&hash, &status, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if hash != requestHash {
		return nil, &Error{Status: 422, Code: "idempotency_key_reuse", Message: "this Idempotency-Key was already used with a different request"}
	}
	return &IdempotentResponse{Status: status, Body: []byte(body)}, nil
}

// IdempotencyStore remembers a successful response for later replays (kept 24 hours).
func (s *Service) IdempotencyStore(ctx context.Context, userID, key, requestHash string, status int, body []byte) {
	_, _ = s.db.W.ExecContext(ctx, `INSERT OR IGNORE INTO idempotency_keys (user_id, key, request_hash, status_code, response_body, created_at) VALUES (?,?,?,?,?,?)`,
		userID, key, requestHash, status, string(body), s.nowMS())
}
