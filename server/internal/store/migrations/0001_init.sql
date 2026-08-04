CREATE TABLE IF NOT EXISTS schema_migrations (
    filename TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL,
    max_quality TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS devices (
    device_id TEXT PRIMARY KEY,
    ip TEXT NOT NULL,
    model TEXT NOT NULL,
    tuner_count INTEGER NOT NULL,
    manual INTEGER NOT NULL DEFAULT 0,
    last_seen TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS channels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id TEXT NOT NULL,
    guide_number TEXT NOT NULL,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 0,
    epg_channel_id TEXT NOT NULL DEFAULT '',
    UNIQUE (device_id, guide_number)
);

CREATE TABLE IF NOT EXISTS epg_channels (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    callsign TEXT NOT NULL,
    icon_url TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS programs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    epg_channel_id TEXT NOT NULL,
    start TEXT NOT NULL,
    stop TEXT NOT NULL,
    title TEXT NOT NULL,
    subtitle TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    icon_url TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_programs_epg_channel_start
    ON programs (epg_channel_id, start);

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
