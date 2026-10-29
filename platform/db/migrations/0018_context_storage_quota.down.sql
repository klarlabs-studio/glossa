DROP INDEX IF EXISTS context_captures_image;
CREATE INDEX context_captures_image ON context_captures (project_id, image_digest);

ALTER TABLE context_captures DROP COLUMN IF EXISTS image_bytes;
