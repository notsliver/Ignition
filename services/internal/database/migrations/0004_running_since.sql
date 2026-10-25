-- When the current container started, as reported by Docker. NULL when not running.
ALTER TABLE resources ADD COLUMN running_since timestamptz;
