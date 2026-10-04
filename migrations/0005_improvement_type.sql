-- Third task type, Improvement, listed first: Improvement, Feature, Bug. Existing tasks and their types are untouched.
UPDATE task_types SET position = position + 1;
INSERT INTO task_types (slug, name, position, color) VALUES ('improvement', 'Improvement', 1, '#2563eb');
