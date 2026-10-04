-- Per-account limits (0 = unlimited): concurrent streams, and tuners the
-- account occupies alone (channels no other account is watching).
ALTER TABLE users ADD COLUMN max_streams INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN max_tuners INTEGER NOT NULL DEFAULT 0;
