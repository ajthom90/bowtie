-- Per-user favorite channels and recently watched channels. No foreign keys
-- (repo convention): DeleteUser and SyncLineup delete dependent rows.
CREATE TABLE IF NOT EXISTS user_favorites (
    user_id INTEGER NOT NULL,
    channel_id INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, channel_id)
);
CREATE TABLE IF NOT EXISTS user_recents (
    user_id INTEGER NOT NULL,
    channel_id INTEGER NOT NULL,
    watched_at TEXT NOT NULL,
    PRIMARY KEY (user_id, channel_id)
);
CREATE INDEX IF NOT EXISTS idx_user_recents_user_watched ON user_recents (user_id, watched_at);
