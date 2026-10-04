-- Continue watching loads one user's positions; the primary key is
-- (recording_id, user_id), so index user_id on its own.
CREATE INDEX IF NOT EXISTS recording_positions_user ON recording_positions (user_id);
