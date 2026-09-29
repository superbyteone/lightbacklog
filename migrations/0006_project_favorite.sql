-- Per-user project favoriting: a member can star a project to pin it to the top of their own project list.
ALTER TABLE project_members ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0 CHECK (favorite IN (0,1));
