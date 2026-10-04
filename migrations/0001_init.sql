-- LightBacklog initial schema. Timestamps are unix milliseconds (UTC).
-- Dates without a time (due_date) are ISO YYYY-MM-DD text.

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    email         TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    is_admin      INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
    created_at    INTEGER NOT NULL,
    disabled_at   INTEGER
) STRICT;

-- Server-side sessions; only a hash of the cookie value is stored.
CREATE TABLE sessions (
    id_hash      TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    user_agent   TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX sessions_user_idx ON sessions(user_id);
CREATE INDEX sessions_expires_idx ON sessions(expires_at);

CREATE TABLE projects (
    id          TEXT PRIMARY KEY,
    key         TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    color       TEXT NOT NULL DEFAULT '#6b7280',
    archived_at INTEGER,
    created_by  TEXT NOT NULL REFERENCES users(id),
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    next_number INTEGER NOT NULL DEFAULT 1
) STRICT;
CREATE INDEX projects_archived_idx ON projects(archived_at);

CREATE TABLE project_members (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('owner', 'editor', 'viewer')),
    PRIMARY KEY (project_id, user_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX project_members_user_idx ON project_members(user_id);

-- API tokens act as their owning user, limited by scope and (optionally) a project allow-list.
CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    scope        TEXT NOT NULL CHECK (scope IN ('read', 'write')),
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    revoked_at   INTEGER
) STRICT;
CREATE INDEX api_tokens_user_idx ON api_tokens(user_id);

-- No rows for a token means "all projects the user can access".
CREATE TABLE api_token_projects (
    token_id   TEXT NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    PRIMARY KEY (token_id, project_id)
) STRICT, WITHOUT ROWID;

-- Lookup tables so statuses/priorities can become editable later without a schema change.
CREATE TABLE statuses (
    slug     TEXT PRIMARY KEY,
    name     TEXT NOT NULL,
    position INTEGER NOT NULL,
    color    TEXT NOT NULL,
    is_done  INTEGER NOT NULL DEFAULT 0 CHECK (is_done IN (0, 1))
) STRICT;
INSERT INTO statuses (slug, name, position, color, is_done) VALUES
    ('todo',        'To Do',       1, '#6b7280', 0),
    ('in_progress', 'In Progress', 2, '#2563eb', 0),
    ('blocked',     'Blocked',     3, '#dc2626', 0),
    ('done',        'Done',        4, '#16a34a', 1);

CREATE TABLE priorities (
    slug     TEXT PRIMARY KEY,
    name     TEXT NOT NULL,
    position INTEGER NOT NULL,
    color    TEXT NOT NULL
) STRICT;
INSERT INTO priorities (slug, name, position, color) VALUES
    ('high',   'High',   1, '#dc2626'),
    ('medium', 'Medium', 2, '#d97706'),
    ('low',    'Low',    3, '#6b7280');

CREATE TABLE tasks (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    number         INTEGER NOT NULL,
    title          TEXT NOT NULL,
    description_md TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'todo' REFERENCES statuses(slug),
    priority       TEXT NOT NULL DEFAULT 'medium' REFERENCES priorities(slug),
    due_date       TEXT,
    external_ref   TEXT,
    version        INTEGER NOT NULL DEFAULT 1,
    position       REAL NOT NULL DEFAULT 0,
    created_by     TEXT NOT NULL REFERENCES users(id),
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (project_id, number)
) STRICT;
CREATE UNIQUE INDEX tasks_external_ref_idx ON tasks(project_id, external_ref) WHERE external_ref IS NOT NULL;
CREATE INDEX tasks_project_status_idx ON tasks(project_id, status, position);
CREATE INDEX tasks_status_priority_idx ON tasks(status, priority, updated_at);
CREATE INDEX tasks_updated_idx ON tasks(updated_at, id);
CREATE INDEX tasks_due_idx ON tasks(due_date) WHERE due_date IS NOT NULL;

CREATE TABLE labels (
    id    TEXT PRIMARY KEY,
    name  TEXT NOT NULL UNIQUE COLLATE NOCASE,
    color TEXT NOT NULL DEFAULT '#6b7280'
) STRICT;

CREATE TABLE task_labels (
    task_id  TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    label_id TEXT NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, label_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX task_labels_label_idx ON task_labels(label_id);

-- task_id is NULL for uploads made before the task exists (e.g. images pasted in the create dialog).
CREATE TABLE attachments (
    id          TEXT PRIMARY KEY,
    task_id     TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    uploader_id TEXT NOT NULL REFERENCES users(id),
    filename    TEXT NOT NULL,
    mime        TEXT NOT NULL,
    size        INTEGER NOT NULL,
    sha256      TEXT NOT NULL,
    created_at  INTEGER NOT NULL
) STRICT;
CREATE INDEX attachments_task_idx ON attachments(task_id);
CREATE INDEX attachments_sha_idx ON attachments(sha256);

-- Replay protection for POST requests carrying an Idempotency-Key header.
CREATE TABLE idempotency_keys (
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key           TEXT NOT NULL,
    request_hash  TEXT NOT NULL,
    status_code   INTEGER NOT NULL,
    response_body TEXT NOT NULL,
    created_at    INTEGER NOT NULL,
    PRIMARY KEY (user_id, key)
) STRICT, WITHOUT ROWID;

-- Full-text search over tasks (external content: text lives in tasks, index is derived).
CREATE VIRTUAL TABLE task_fts USING fts5(
    title, description_md,
    content='tasks', content_rowid='rowid',
    tokenize='unicode61 remove_diacritics 2', prefix='2 3'
);
CREATE TRIGGER tasks_fts_ai AFTER INSERT ON tasks BEGIN
    INSERT INTO task_fts(rowid, title, description_md) VALUES (new.rowid, new.title, new.description_md);
END;
CREATE TRIGGER tasks_fts_ad AFTER DELETE ON tasks BEGIN
    INSERT INTO task_fts(task_fts, rowid, title, description_md) VALUES ('delete', old.rowid, old.title, old.description_md);
END;
CREATE TRIGGER tasks_fts_au AFTER UPDATE OF title, description_md ON tasks BEGIN
    INSERT INTO task_fts(task_fts, rowid, title, description_md) VALUES ('delete', old.rowid, old.title, old.description_md);
    INSERT INTO task_fts(rowid, title, description_md) VALUES (new.rowid, new.title, new.description_md);
END;
