-- Parental controls: program ratings (as the guide source gives them, e.g.
-- "TV-14") and per-account restrictions. allowed_channels: '' = every
-- channel, else comma-separated channel IDs. max_rating: parental ladder
-- level (0 = none).
ALTER TABLE programs ADD COLUMN rating TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN allowed_channels TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN max_rating INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN block_unrated INTEGER NOT NULL DEFAULT 0;
-- Recordings keep the program's rating (snapshot at scheduling).
ALTER TABLE recordings ADD COLUMN rating TEXT NOT NULL DEFAULT '';
