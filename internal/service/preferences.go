package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
)

// Preferences are small per-user UI settings (for example the list's column layout for each
// device class). They are stored as one JSON object and updated by merging top-level keys, so
// two devices saving different keys never overwrite each other.
const (
	maxPreferencesBytes = 16 << 10
	maxPreferenceKeys   = 32
)

var prefKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// GetPreferences returns the caller's preferences ({} when none are saved). Browser sessions only.
func (s *Service) GetPreferences(ctx context.Context, p Principal) (map[string]json.RawMessage, error) {
	if err := p.requireSession(); err != nil {
		return nil, err
	}
	return s.readPreferences(ctx, s.db.R, p.UserID)
}

func (s *Service) readPreferences(ctx context.Context, q querier, userID string) (map[string]json.RawMessage, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT data FROM user_preferences WHERE user_id = ?`, userID).Scan(&raw)
	out := map[string]json.RawMessage{}
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]json.RawMessage{}, nil // never let a damaged record break the app
	}
	return out, nil
}

// MergePreferences sets the given top-level keys (a JSON null value deletes a key) and returns the result.
func (s *Service) MergePreferences(ctx context.Context, p Principal, patch map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if err := p.requireSession(); err != nil {
		return nil, err
	}
	for k, v := range patch {
		if !prefKeyRe.MatchString(k) {
			return nil, invalid("preferences", "key %q must be lowercase letters, digits, '.', '_' or '-' (at most 64 characters)", k)
		}
		if !json.Valid(v) {
			return nil, invalid("preferences", "value of %q is not valid JSON", k)
		}
	}
	var out map[string]json.RawMessage
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		cur, err := s.readPreferences(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		for k, v := range patch {
			if string(v) == "null" {
				delete(cur, k)
			} else {
				cur[k] = v
			}
		}
		if len(cur) > maxPreferenceKeys {
			return invalid("preferences", "at most %d preference keys are allowed", maxPreferenceKeys)
		}
		data, err := json.Marshal(cur)
		if err != nil {
			return err
		}
		if len(data) > maxPreferencesBytes {
			return invalid("preferences", "preferences may not exceed %d KiB in total", maxPreferencesBytes>>10)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO user_preferences (user_id, data, updated_at) VALUES (?,?,?)
			ON CONFLICT(user_id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`, p.UserID, string(data), s.nowMS())
		out = cur
		return err
	})
	return out, err
}
