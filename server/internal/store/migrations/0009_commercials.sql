-- Commercial breaks found by Comskip, as JSON [{"start":s,"end":s}] on the
-- recording's playback timeline. NULL = detection hasn't run; '[]' = it ran
-- and found none (or failed, so it isn't retried forever).
ALTER TABLE recordings ADD COLUMN commercials TEXT;
