-- Optional work order for tasks: 1 is done first, 2 next, and so on. NULL means "not sequenced".
ALTER TABLE tasks ADD COLUMN sequence INTEGER CHECK (sequence IS NULL OR (sequence >= 0 AND sequence <= 1000000));
CREATE INDEX tasks_sequence_idx ON tasks(sequence) WHERE sequence IS NOT NULL;
