-- Per-user interface preferences (theme, presets, time display, start page...). They follow the
-- account across browsers; the API validates every key.
ALTER TABLE users ADD COLUMN preferences jsonb NOT NULL DEFAULT '{}';
