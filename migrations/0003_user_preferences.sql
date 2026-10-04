-- Small per-user UI preferences (column layout per device class, ...), stored as one JSON object.
CREATE TABLE user_preferences (
    user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    data       TEXT NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;
