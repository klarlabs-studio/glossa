-- 0018 — the per-tenant capture storage quota (RFC 0004 §3.3, §10, the
-- owner's decision of 2026-09-20).
--
-- A tenant may keep at most GLOSSA_CONTEXT_STORAGE_QUOTA_BYTES (2 GB by
-- default) of capture images in object storage. What a tenant holds is
-- computed from the rows that reference those images rather than from a
-- counter of its own: a counter would need a second write path and
-- would drift from the bucket whenever a purge, a project deletion or a
-- failed upload disagreed with it.
--
-- Images are content-addressed per project (context/<tenant>/<project>/
-- img/<sha256>.png), so a tenant's stored bytes are the sum over the
-- distinct (project_id, image_digest) pairs its captures reference. The
-- size of the re-encoded PNG is therefore recorded on every capture
-- that shows it; re-encoding is deterministic, so the captures of one
-- image all carry the same number.
--
-- Rows written before this migration have no size on record and count
-- as 0. That understates a tenant that captured during M3 development;
-- their builds age out of retention (§2.3) within days, and nothing
-- stored is lost by it.

ALTER TABLE context_captures
    ADD COLUMN image_bytes bigint NOT NULL DEFAULT 0 CHECK (image_bytes >= 0);

-- The quota sums one row per (project, image); the index keeps that
-- sum off a full scan of the tenant's captures. It extends the index
-- migration 0012 created for finding an image's remaining references,
-- which this replaces.
DROP INDEX IF EXISTS context_captures_image;
CREATE INDEX context_captures_image ON context_captures (project_id, image_digest) INCLUDE (image_bytes);
