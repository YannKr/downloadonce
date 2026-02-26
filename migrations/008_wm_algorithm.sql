-- Add algorithm tag to watermark index and download tokens.
-- Existing rows default to 'dwtDctSvd-python' (embedded by the Python pipeline).
ALTER TABLE watermark_index ADD COLUMN wm_algorithm TEXT NOT NULL DEFAULT 'dwtDctSvd-python';
ALTER TABLE download_tokens ADD COLUMN wm_algorithm TEXT;
