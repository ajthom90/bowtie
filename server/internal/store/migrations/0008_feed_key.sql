-- Personal IPTV feed key (M3U/XMLTV for Kodi, VLC, TiviMate, Plex, Jellyfin):
-- SHA-256 hex of the key; '' = no feed.
ALTER TABLE users ADD COLUMN feed_key_hash TEXT NOT NULL DEFAULT '';
