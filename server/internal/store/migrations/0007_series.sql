-- Series recording: program identity from the guide, recording rules, and
-- which rule/program a recording came from.
ALTER TABLE programs ADD COLUMN program_id TEXT NOT NULL DEFAULT '';
ALTER TABLE programs ADD COLUMN series_id TEXT NOT NULL DEFAULT '';
ALTER TABLE programs ADD COLUMN is_new INTEGER NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS recording_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    series_id TEXT NOT NULL DEFAULT '',
    channel_id INTEGER NOT NULL DEFAULT 0, -- 0 = any channel
    new_only INTEGER NOT NULL DEFAULT 1,
    keep_latest INTEGER NOT NULL DEFAULT 0, -- 0 = keep all
    created_at TEXT NOT NULL
);
ALTER TABLE recordings ADD COLUMN rule_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE recordings ADD COLUMN program_id TEXT NOT NULL DEFAULT '';
