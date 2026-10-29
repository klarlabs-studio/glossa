-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.
--
-- A finding row is immutable (migration 0027): a run is one evaluation,
-- and the next evaluation writes new rows rather than editing the last
-- one's. There is no UPDATE here and the table is not granted one.
--
-- Severity is stored as the layer emitted it — `error` or `warning`, and
-- never `waived`. A waiver is not a property of a finding but of the
-- project (RFC 0005 §2.3), it can be written after the run that found
-- the finding and it dies when the source revision moves, so whether a
-- finding is waived is decided on *read*, by the lateral join below,
-- against the waivers that are live now. Baking it into the row would
-- lose the severity the layer emitted, and a revoked waiver could never
-- give it back. What the run itself concluded is kept, in the run's own
-- counts.

-- name: InsertFindings :exec
-- A batch of a run's findings. uuid.Nil stands for an absent ID, 0 for
-- an absent line or column, -1 for an absent span offset or source
-- revision, and '' for absent JSON.
INSERT INTO quality_findings (id, tenant_id, run_id, project_id, fingerprint, layer, code, severity,
                              message_id, message_key, locale, namespace, translation_revision,
                              file, line, col, route, component, capture_id, region,
                              span_side, span_start, span_end,
                              explanation, subject, detail, evidence, fix, source_revision)
SELECT f.id, app_current_tenant(), sqlc.arg(run_id), sqlc.arg(project_id), f.fingerprint, f.layer, f.code, f.severity,
       NULLIF(f.message_id, '00000000-0000-0000-0000-000000000000'::uuid), f.message_key, f.locale, f.namespace,
       NULLIF(f.translation_revision, '00000000-0000-0000-0000-000000000000'::uuid),
       f.file, NULLIF(f.line, 0), NULLIF(f.col, 0), f.route, f.component,
       NULLIF(f.capture_id, '00000000-0000-0000-0000-000000000000'::uuid), f.region,
       NULLIF(f.span_side, ''), NULLIF(f.span_start, -1), NULLIF(f.span_end, -1),
       f.explanation, f.subject, f.detail, NULLIF(f.evidence, '')::jsonb, NULLIF(f.fix, '')::jsonb,
       NULLIF(f.source_revision, -1)
FROM (SELECT unnest(sqlc.arg(ids)::uuid[]) AS id, unnest(sqlc.arg(fingerprints)::text[]) AS fingerprint,
             unnest(sqlc.arg(layers)::text[]) AS layer, unnest(sqlc.arg(codes)::text[]) AS code,
             unnest(sqlc.arg(severities)::text[]) AS severity, unnest(sqlc.arg(message_ids)::uuid[]) AS message_id,
             unnest(sqlc.arg(message_keys)::text[]) AS message_key, unnest(sqlc.arg(locales)::text[]) AS locale,
             unnest(sqlc.arg(namespaces)::text[]) AS namespace,
             unnest(sqlc.arg(translation_revisions)::uuid[]) AS translation_revision,
             unnest(sqlc.arg(files)::text[]) AS file, unnest(sqlc.arg(lines)::int[]) AS line,
             unnest(sqlc.arg(cols)::int[]) AS col, unnest(sqlc.arg(routes)::text[]) AS route,
             unnest(sqlc.arg(components)::text[]) AS component, unnest(sqlc.arg(capture_ids)::uuid[]) AS capture_id,
             unnest(sqlc.arg(regions)::text[]) AS region, unnest(sqlc.arg(span_sides)::text[]) AS span_side,
             unnest(sqlc.arg(span_starts)::int[]) AS span_start, unnest(sqlc.arg(span_ends)::int[]) AS span_end,
             unnest(sqlc.arg(explanations)::text[]) AS explanation, unnest(sqlc.arg(subjects)::text[]) AS subject,
             unnest(sqlc.arg(details)::text[]) AS detail, unnest(sqlc.arg(evidences)::text[]) AS evidence,
             unnest(sqlc.arg(fixes)::text[]) AS fix,
             unnest(sqlc.arg(source_revisions)::int[]) AS source_revision) AS f;

-- name: ListRunFindings :many
-- A page of a run's findings, graded against the waivers that are live
-- now, in a stable order: errors, then warnings, then the waived, and
-- within each by layer, locale, message key and id. The order key is
-- byte-ordered ("C") and unique, and it is both what the rows are
-- ordered by and what the cursor carries, so a page never shifts.
--
-- The lateral picks at most one waiver — a branch-scoped one before a
-- project-scoped one, because it is the more specific reach — and picks
-- none once the source revision the finding was computed against has
-- moved past the one the waiver was made against: the German somebody
-- waived is not the German that now ships, so the finding comes back.
WITH graded AS (
    SELECT f.id, f.fingerprint, f.layer, f.code, f.severity, f.message_id, f.message_key, f.locale, f.namespace,
           f.translation_revision, f.file, f.line, f.col, f.route, f.component, f.capture_id, f.region,
           f.span_side, f.span_start, f.span_end, f.explanation, f.subject, f.detail, f.evidence, f.fix,
           f.source_revision,
           coalesce(w.id, '00000000-0000-0000-0000-000000000000'::uuid) AS waiver_id,
           (w.id IS NOT NULL)::boolean AS is_waived,
           (CASE WHEN w.id IS NOT NULL THEN 'waived' ELSE f.severity END)::text AS effective_severity,
           concat_ws(E'\x01',
                     CASE WHEN w.id IS NOT NULL THEN '2' WHEN f.severity = 'error' THEN '0' ELSE '1' END,
                     f.layer, f.locale, f.message_key, f.id::text) AS sort_key
    FROM quality_findings f
    LEFT JOIN LATERAL (
        SELECT w.id, w.source_revision, w.scope, w.created_at
        FROM quality_waivers w
        WHERE w.project_id = f.project_id AND w.fingerprint = f.fingerprint AND w.revoked_at IS NULL
          AND (w.expires_at IS NULL OR w.expires_at > sqlc.arg(now)::timestamptz)
          AND (w.scope = 'project' OR w.ref = sqlc.arg(ref)::text)
          AND (f.source_revision IS NULL OR f.source_revision = w.source_revision)
        ORDER BY (w.scope = 'branch') DESC, w.created_at DESC, w.id
        LIMIT 1
    ) w ON true
    WHERE f.run_id = sqlc.arg(run_id)
)
SELECT * FROM graded
WHERE (sqlc.arg(layer)::text = '' OR layer = sqlc.arg(layer)::text)
  AND (sqlc.arg(severity)::text = '' OR effective_severity = sqlc.arg(severity)::text)
  AND (sqlc.arg(code)::text = '' OR code = sqlc.arg(code)::text)
  AND (sqlc.arg(locale)::text = '' OR locale = sqlc.arg(locale)::text)
  AND (sqlc.arg(namespace)::text = '' OR namespace = sqlc.arg(namespace)::text)
  AND (sqlc.arg(message_key)::text = '' OR message_key = sqlc.arg(message_key)::text)
  AND (sqlc.narg(waived)::boolean IS NULL OR is_waived = sqlc.narg(waived)::boolean)
  AND (sqlc.arg(after)::text = '' OR sort_key COLLATE "C" > sqlc.arg(after)::text)
ORDER BY sort_key COLLATE "C"
LIMIT sqlc.arg(max_rows);

-- name: ListCaptureFindings :many
-- A page of the findings on one capture (RFC 0005 §5.2, §13 wave 4),
-- graded against the waivers that are live now, exactly as a run's
-- findings are.
--
-- A capture's findings are read on their own, not through the project's
-- newest run: they were recorded by the run that ingested that capture,
-- and the project's newest run is usually a later check of the catalog
-- that never saw this screenshot. Studio asks "what is wrong on this
-- picture", and `region` narrows the answer to one outlined box.
--
-- The waiver's reach is the ref of the run the finding belongs to,
-- which the join carries, so a branch-scoped waiver applies here for
-- the same branch it applies for anywhere else.
--
-- The order key is byte-ordered ("C") and unique — the run, then the
-- region, then the row — and it is both what the rows are ordered by
-- and what the cursor carries, so a page never shifts.
WITH graded AS (
    SELECT f.id, f.fingerprint, f.layer, f.code, f.severity, f.message_id, f.message_key, f.locale, f.namespace,
           f.translation_revision, f.file, f.line, f.col, f.route, f.component, f.capture_id, f.region,
           f.span_side, f.span_start, f.span_end, f.explanation, f.subject, f.detail, f.evidence, f.fix,
           f.source_revision,
           coalesce(w.id, '00000000-0000-0000-0000-000000000000'::uuid) AS waiver_id,
           (w.id IS NOT NULL)::boolean AS is_waived,
           (CASE WHEN w.id IS NOT NULL THEN 'waived' ELSE f.severity END)::text AS effective_severity,
           concat_ws(E'\x01', r.id::text, f.region, f.id::text) AS sort_key
    FROM quality_findings f
    JOIN quality_check_runs r ON r.id = f.run_id
    LEFT JOIN LATERAL (
        SELECT w.id
        FROM quality_waivers w
        WHERE w.project_id = f.project_id AND w.fingerprint = f.fingerprint AND w.revoked_at IS NULL
          AND (w.expires_at IS NULL OR w.expires_at > sqlc.arg(now)::timestamptz)
          AND (w.scope = 'project' OR w.ref = r.ref)
          AND (f.source_revision IS NULL OR f.source_revision = w.source_revision)
        ORDER BY (w.scope = 'branch') DESC, w.created_at DESC, w.id
        LIMIT 1
    ) w ON true
    WHERE f.project_id = sqlc.arg(project_id) AND f.capture_id = sqlc.arg(capture_id)
)
SELECT * FROM graded
WHERE (sqlc.arg(region)::text = '' OR region = sqlc.arg(region)::text)
  AND (sqlc.arg(after)::text = '' OR sort_key COLLATE "C" > sqlc.arg(after)::text)
ORDER BY sort_key COLLATE "C"
LIMIT sqlc.arg(max_rows);

-- name: ListCaptureFingerprints :many
-- The distinct fingerprints one capture's findings carry: what the
-- previous capture of a scope saw, which is the whole state the
-- two-sighting rule of RFC 0005 §5.2 needs (`layers.Seen`).
--
-- The server reads it from the findings it already stores, where
-- `glossa capture --check` reads it from .glossa/visual-sightings.json:
-- the same record, one of them durable and shared by every runner.
SELECT DISTINCT fingerprint
FROM quality_findings
WHERE project_id = sqlc.arg(project_id) AND capture_id = sqlc.arg(capture_id);

-- name: GetLatestFinding :one
-- The most recent stored finding carrying a fingerprint (run ids are
-- time-ordered UUIDv7): what a waiver is about, and the source revision
-- it is made against when the caller names none.
SELECT layer, code, locale, message_key, namespace, explanation, source_revision
FROM quality_findings
WHERE project_id = sqlc.arg(project_id) AND fingerprint = sqlc.arg(fingerprint)
ORDER BY run_id DESC
LIMIT 1;

-- name: CountRunFindings :one
-- The run's findings as they stand now: waived is counted on its own and
-- is never part of the other two, so the number a dashboard shows is
-- true (RFC 0005 §14 decision 5).
SELECT count(*) FILTER (WHERE w.id IS NULL AND f.severity = 'error')::int AS errors,
       count(*) FILTER (WHERE w.id IS NULL AND f.severity = 'warning')::int AS warnings,
       count(*) FILTER (WHERE w.id IS NOT NULL)::int AS waived
FROM quality_findings f
LEFT JOIN LATERAL (
    SELECT w.id
    FROM quality_waivers w
    WHERE w.project_id = f.project_id AND w.fingerprint = f.fingerprint AND w.revoked_at IS NULL
      AND (w.expires_at IS NULL OR w.expires_at > sqlc.arg(now)::timestamptz)
      AND (w.scope = 'project' OR w.ref = sqlc.arg(ref)::text)
      AND (f.source_revision IS NULL OR f.source_revision = w.source_revision)
    LIMIT 1
) w ON true
WHERE f.run_id = sqlc.arg(run_id);

-- name: CountRunFindingsByLayer :many
-- The same counts, per locale and layer: the quality summary's second
-- number (RFC 0005 §8, "outstanding findings by layer and severity,
-- plus waived"), which it needs both per project and per locale.
--
-- The locale is '' for the layers that have none — `structure`,
-- `completeness` and `source` say what is wrong with a message, not
-- with a translation of it — so a project's total is every row and a
-- locale's is the rows carrying it. Grouping in one pass is what keeps
-- twenty locales from being twenty queries.
--
-- A layer with no findings is not a row here. Which layers ran at all
-- is the run's own `layers` column, and it is a different question —
-- the one that tells "clean" from "not looked at", which is why the
-- summary reports both.
SELECT f.locale, f.layer,
       count(*) FILTER (WHERE w.id IS NULL AND f.severity = 'error')::int AS errors,
       count(*) FILTER (WHERE w.id IS NULL AND f.severity = 'warning')::int AS warnings,
       count(*) FILTER (WHERE w.id IS NOT NULL)::int AS waived
FROM quality_findings f
LEFT JOIN LATERAL (
    SELECT w.id
    FROM quality_waivers w
    WHERE w.project_id = f.project_id AND w.fingerprint = f.fingerprint AND w.revoked_at IS NULL
      AND (w.expires_at IS NULL OR w.expires_at > sqlc.arg(now)::timestamptz)
      AND (w.scope = 'project' OR w.ref = sqlc.arg(ref)::text)
      AND (f.source_revision IS NULL OR f.source_revision = w.source_revision)
    LIMIT 1
) w ON true
WHERE f.run_id = sqlc.arg(run_id)
GROUP BY f.locale, f.layer;
