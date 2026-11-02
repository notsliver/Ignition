-- Only filenames live in PostgreSQL. Image bytes live in the uploads Docker volume.
ALTER TABLE users ADD COLUMN avatar_file text NOT NULL DEFAULT '';
ALTER TABLE teams ADD COLUMN icon_file text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX users_avatar_file_idx ON users(avatar_file) WHERE avatar_file <> '';
CREATE UNIQUE INDEX teams_icon_file_idx ON teams(icon_file) WHERE icon_file <> '';
