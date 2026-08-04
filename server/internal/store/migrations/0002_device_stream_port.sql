-- Stream port for /auto/v* URLs. Real HDHomeRun devices use 5004 when HTTP is on
-- port 80; hdhrfake (and other nonstandard BaseURLs) reuse the BaseURL port.
ALTER TABLE devices ADD COLUMN stream_port INTEGER NOT NULL DEFAULT 5004;
