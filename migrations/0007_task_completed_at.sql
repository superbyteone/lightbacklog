-- When a task last arrived in a "done" status (NULL while it is not done). Existing done tasks are
-- backfilled with their last update, the closest information available.
ALTER TABLE tasks ADD COLUMN completed_at INTEGER;
UPDATE tasks SET completed_at = updated_at WHERE status IN (SELECT slug FROM statuses WHERE is_done = 1);
