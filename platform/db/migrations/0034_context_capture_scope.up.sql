-- 0034 — the capture scope index (RFC 0005 §5.2).
--
-- The two-sighting rule counts a visual finding in the scope of one
-- (route, viewport, locale): a finding may be an error only when the
-- capture before this one of the same page showed it too. The capture
-- ingest therefore asks, once per capture of an upload, "which capture
-- did this project show last of this route, at this viewport, in this
-- locale?" — and an upload may carry 500 captures, so the answer has to
-- come from an index rather than from every capture the project ever
-- took.
--
-- It is the same column order as the per-build UNIQUE constraint, minus
-- the build and plus the project, because that is the question: not
-- "this build's capture of the scope" but "the project's last one".
-- created_at closes it: the row wanted is the newest, and the index
-- hands it over without a sort.
CREATE INDEX context_captures_scope
    ON context_captures (project_id, route, viewport_width, viewport_height, locale, created_at DESC);
