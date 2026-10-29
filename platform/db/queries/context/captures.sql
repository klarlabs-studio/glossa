-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.

-- name: InsertCapture :execrows
-- One capture per (route, viewport, locale) of a build: a repeat
-- inserts nothing and the caller compares it with the first.
INSERT INTO context_captures (id, tenant_id, build_id, project_id, route, viewport_width, viewport_height, locale,
                              image_digest, image_width, image_height, image_bytes, created_by, created_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(build_id), sqlc.arg(project_id), sqlc.arg(route),
        sqlc.arg(viewport_width), sqlc.arg(viewport_height), sqlc.arg(locale), sqlc.arg(image_digest),
        sqlc.arg(image_width), sqlc.arg(image_height), sqlc.arg(image_bytes), sqlc.arg(created_by),
        sqlc.arg(created_at))
ON CONFLICT (build_id, route, viewport_width, viewport_height, locale) DO NOTHING;

-- name: GetTenantImageBytes :one
-- What the tenant's capture images occupy in object storage: the sum
-- over the distinct images its captures reference, since the same
-- pixels are stored once per project (RFC 0004 §3.3). This is what the
-- per-tenant storage quota compares against; RLS scopes it to the
-- current tenant.
SELECT coalesce(sum(bytes), 0)::bigint AS stored_bytes
FROM (SELECT max(image_bytes) AS bytes FROM context_captures GROUP BY project_id, image_digest) AS images;

-- name: GetCaptureByShot :one
SELECT * FROM context_captures
WHERE build_id = sqlc.arg(build_id) AND route = sqlc.arg(route) AND viewport_width = sqlc.arg(viewport_width)
  AND viewport_height = sqlc.arg(viewport_height) AND locale = sqlc.arg(locale);

-- name: GetPreviousCaptureOfScope :one
-- The capture this application showed last of one (route, viewport,
-- locale): the previous sighting the two-sighting rule of RFC 0005 §5.2
-- counts against.
--
-- The scope is the application's and not the branch's, because the rule
-- is flake control and not blame: a finding two consecutive captures of
-- the same page agree on is not a font that was installed on one runner
-- and missing on the next, whichever branch each capture came from. It
-- is also what `glossa capture --check`'s own record does, whose
-- .glossa/visual-sightings.json is keyed by application and scope and
-- outlives a branch switch; the two must not quietly differ.
--
-- The upload that asks has not stored its captures yet, so the newest
-- row here is the one immediately before it.
SELECT c.id
FROM context_captures c
JOIN context_builds b ON b.id = c.build_id
WHERE c.project_id = sqlc.arg(project_id) AND b.application_id = sqlc.arg(application_id)
  AND c.route = sqlc.arg(route) AND c.viewport_width = sqlc.arg(viewport_width)
  AND c.viewport_height = sqlc.arg(viewport_height) AND c.locale = sqlc.arg(locale)
ORDER BY c.created_at DESC, c.id DESC
LIMIT 1;

-- name: CountBuildCaptures :one
SELECT count(*) FROM context_captures WHERE build_id = sqlc.arg(build_id);

-- name: InsertRegions :exec
INSERT INTO context_regions (capture_id, position, tenant_id, message_key, message_id, kind, x, y, width, height, visible)
SELECT sqlc.arg(capture_id), r.position, app_current_tenant(), r.message_key,
       NULLIF(r.message_id, '00000000-0000-0000-0000-000000000000'::uuid), r.kind, r.x, r.y, r.width, r.height,
       r.visible
FROM (SELECT unnest(sqlc.arg(positions)::int[]) AS position, unnest(sqlc.arg(keys)::text[]) AS message_key,
             unnest(sqlc.arg(message_ids)::uuid[]) AS message_id, unnest(sqlc.arg(kinds)::text[]) AS kind,
             unnest(sqlc.arg(xs)::int[]) AS x, unnest(sqlc.arg(ys)::int[]) AS y,
             unnest(sqlc.arg(widths)::int[]) AS width, unnest(sqlc.arg(heights)::int[]) AS height,
             unnest(sqlc.arg(visibles)::boolean[]) AS visible) AS r;

-- name: ListRegions :many
SELECT * FROM context_regions WHERE capture_id = sqlc.arg(capture_id) ORDER BY position;

-- name: ListBuildImages :many
-- The images the given builds' captures reference.
SELECT DISTINCT image_digest FROM context_captures WHERE build_id = ANY(sqlc.arg(build_ids)::uuid[]);

-- name: CountCapturesOfBuilds :one
-- How many captures the given builds hold: what a purge deletes with
-- them (RFC 0004 §11 counts deletions by kind).
SELECT count(*)::int FROM context_captures WHERE build_id = ANY(sqlc.arg(build_ids)::uuid[]);

-- name: ListReferencedImages :many
-- Which of digests a capture of the project still references.
SELECT DISTINCT image_digest FROM context_captures
WHERE project_id = sqlc.arg(project_id) AND image_digest = ANY(sqlc.arg(digests)::text[]);

-- name: GetCapture :one
SELECT * FROM context_captures WHERE id = sqlc.arg(id);

-- name: ListBuildUnknownRegionKeys :many
-- The keys of a build's regions the catalog didn't know at ingest.
SELECT DISTINCT r.message_key
FROM context_regions r
JOIN context_captures c ON c.id = r.capture_id
WHERE c.build_id = sqlc.arg(build_id) AND r.message_id IS NULL
ORDER BY r.message_key;

-- name: ListMessageCaptures :many
-- The captures in the given (current) builds that show a message: the
-- default branch first, then by application, route, locale and the
-- widest viewport.
SELECT c.id, c.build_id, c.project_id, c.route, c.viewport_width, c.viewport_height, c.locale, c.image_digest,
       c.image_width, c.image_height, c.created_by, c.created_at,
       b.application_id, b.commit_sha, b.branch, b.on_default_branch
FROM context_captures c
JOIN context_builds b ON b.id = c.build_id
WHERE c.build_id = ANY(sqlc.arg(build_ids)::uuid[])
  AND EXISTS (SELECT 1 FROM context_regions r WHERE r.capture_id = c.id AND r.message_id = sqlc.arg(message_id)::uuid)
ORDER BY b.on_default_branch DESC, b.application_id, c.route, c.locale, c.viewport_width DESC, c.viewport_height DESC, c.id
LIMIT sqlc.arg(max_rows);

-- name: ListMessageRegions :many
-- A message's regions on the given captures, by capture and position.
SELECT * FROM context_regions
WHERE capture_id = ANY(sqlc.arg(capture_ids)::uuid[]) AND message_id = sqlc.arg(message_id)::uuid
ORDER BY capture_id, position;

-- name: ListCapturedMessageIDs :many
-- The messages with a visible region on the given (current) builds'
-- captures.
SELECT DISTINCT r.message_id::uuid AS message_id
FROM context_regions r
JOIN context_captures c ON c.id = r.capture_id
WHERE c.build_id = ANY(sqlc.arg(build_ids)::uuid[]) AND r.message_id IS NOT NULL AND r.visible;

-- name: ListProjectImages :many
-- Every image a capture of the project references.
SELECT DISTINCT image_digest FROM context_captures WHERE project_id = sqlc.arg(project_id);

-- name: CaptureShowsAny :one
-- Whether the capture has a region of any of the messages: what an
-- assigned member may see a screenshot for (RFC 0006 §3.3).
SELECT EXISTS (
    SELECT 1 FROM context_regions
    WHERE capture_id = sqlc.arg(capture_id)::uuid AND message_id = ANY(sqlc.arg(message_ids)::uuid[])
)::boolean;
