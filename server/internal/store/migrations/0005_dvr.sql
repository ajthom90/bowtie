-- DVR phase 1: one-off and manual recordings. Channel and program metadata
-- are snapshots (not foreign keys) so a recording outlives lineup and EPG
-- changes.
CREATE TABLE IF NOT EXISTS recordings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    channel_id INTEGER NOT NULL,
    channel_name TEXT NOT NULL,
    title TEXT NOT NULL,
    subtitle TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    icon_url TEXT NOT NULL DEFAULT '',
    start TEXT NOT NULL,
    stop TEXT NOT NULL,
    pad_start_sec INTEGER NOT NULL DEFAULT 0,
    pad_end_sec INTEGER NOT NULL DEFAULT 0,
    -- scheduled|waiting|recording|converting|ready|failed
    state TEXT NOT NULL,
    partial INTEGER NOT NULL DEFAULT 0,
    -- noTuner|noSignal|diskFull|error
    failure TEXT NOT NULL DEFAULT '',
    failure_detail TEXT NOT NULL DEFAULT '',
    actual_start TEXT NOT NULL DEFAULT '',
    actual_stop TEXT NOT NULL DEFAULT '',
    missed_sec INTEGER NOT NULL DEFAULT 0,
    dir TEXT NOT NULL DEFAULT '',
    size_bytes INTEGER NOT NULL DEFAULT 0,
    duration_sec INTEGER NOT NULL DEFAULT 0,
    protected INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_recordings_state_start ON recordings (state, start);
CREATE TABLE IF NOT EXISTS recording_positions (
    recording_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    position_sec INTEGER NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (recording_id, user_id)
);
