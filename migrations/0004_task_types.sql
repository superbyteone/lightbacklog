-- Optional task type (Feature, Bug). A lookup table like statuses and priorities, so more types
-- can be added later without another schema change. NULL means "no type".
CREATE TABLE task_types (
    slug     TEXT PRIMARY KEY,
    name     TEXT NOT NULL,
    position INTEGER NOT NULL,
    color    TEXT NOT NULL
) STRICT;
INSERT INTO task_types (slug, name, position, color) VALUES
    ('feature', 'Feature', 1, '#7c3aed'),
    ('bug',     'Bug',     2, '#dc2626');

ALTER TABLE tasks ADD COLUMN type TEXT REFERENCES task_types(slug);
CREATE INDEX tasks_type_idx ON tasks(type) WHERE type IS NOT NULL;
