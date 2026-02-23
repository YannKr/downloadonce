-- Phase 2: Detection support
-- Add columns for detection jobs (input file path + result data)
ALTER TABLE jobs ADD COLUMN input_path TEXT;
ALTER TABLE jobs ADD COLUMN result_data TEXT;
