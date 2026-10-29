-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.
--
-- Scope filter used by the lookups: all_projects reaches every unit of
-- the tenant; otherwise tenant-wide units and the given project's.

-- ── derivation ─────────────────────────────────────────────────────

-- name: EnsureDerivation :exec
-- Creates the translation's bookkeeping row so LockDerivation always
-- has a row to lock; a concurrent insert waits for the first to commit.
INSERT INTO knowledge_tm_derivations (translation_id, tenant_id, project_id, revision, updated_at)
VALUES (sqlc.arg(translation_id), app_current_tenant(), sqlc.arg(project_id), 0, sqlc.arg(updated_at))
ON CONFLICT (translation_id) DO NOTHING;

-- name: LockDerivation :one
SELECT revision FROM knowledge_tm_derivations WHERE translation_id = sqlc.arg(translation_id) FOR UPDATE;

-- name: SetDerivation :exec
UPDATE knowledge_tm_derivations SET revision = sqlc.arg(revision), updated_at = sqlc.arg(updated_at)
WHERE translation_id = sqlc.arg(translation_id);

-- name: LockActiveUnitOfTranslation :one
SELECT * FROM knowledge_tm_units
WHERE translation_id = sqlc.arg(translation_id) AND retired_at IS NULL
FOR UPDATE;

-- Set-based forms of the above for a delivered batch (#109). Rows are
-- written and locked in translation order, the order every writer takes
-- them in, so concurrent batches cannot deadlock.

-- name: EnsureDerivations :exec
INSERT INTO knowledge_tm_derivations (translation_id, tenant_id, project_id, revision, updated_at)
SELECT v.translation_id, app_current_tenant(), v.project_id, 0, sqlc.arg(updated_at)
FROM (SELECT unnest(sqlc.arg(translation_ids)::uuid[]) AS translation_id,
             unnest(sqlc.arg(project_ids)::uuid[]) AS project_id) v
ORDER BY v.translation_id
ON CONFLICT (translation_id) DO NOTHING;

-- name: LockDerivations :many
SELECT translation_id, revision FROM knowledge_tm_derivations
WHERE translation_id = ANY (sqlc.arg(translation_ids)::uuid[])
ORDER BY translation_id
FOR UPDATE;

-- name: SetDerivations :exec
UPDATE knowledge_tm_derivations d SET revision = v.revision, updated_at = sqlc.arg(updated_at)
FROM (SELECT unnest(sqlc.arg(translation_ids)::uuid[]) AS translation_id,
             unnest(sqlc.arg(revisions)::int[]) AS revision) v
WHERE d.translation_id = v.translation_id;

-- name: LockActiveUnitsOfTranslations :many
SELECT * FROM knowledge_tm_units
WHERE translation_id = ANY (sqlc.arg(translation_ids)::uuid[]) AND retired_at IS NULL
ORDER BY translation_id
FOR UPDATE;

-- ── units ──────────────────────────────────────────────────────────

-- name: InsertTMUnit :exec
INSERT INTO knowledge_tm_units (id, tenant_id, project_id, origin, translation_id, translation_revision, message_id,
                                message_key, namespace, source_locale, target_locale, source_mf2, target_mf2,
                                target_model, source_normalized, target_normalized, source_hash, signature,
                                source_vars, hit_count, last_hit_at, created_by, created_at, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.narg(project_id), sqlc.arg(origin), sqlc.narg(translation_id),
        sqlc.narg(translation_revision), sqlc.narg(message_id), sqlc.arg(message_key), sqlc.arg(namespace),
        sqlc.arg(source_locale), sqlc.arg(target_locale), sqlc.arg(source_mf2), sqlc.arg(target_mf2),
        sqlc.arg(target_model), sqlc.arg(source_normalized), sqlc.arg(target_normalized), sqlc.arg(source_hash),
        sqlc.arg(signature), sqlc.arg(source_vars), 0, NULL, sqlc.arg(created_by), sqlc.arg(created_at),
        sqlc.arg(updated_at));

-- name: TouchTMUnit :exec
UPDATE knowledge_tm_units
SET translation_revision = sqlc.arg(translation_revision), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: RetireTMUnit :execrows
UPDATE knowledge_tm_units
SET retired_at = sqlc.arg(retired_at), retired_reason = sqlc.arg(retired_reason), retired_by = sqlc.arg(retired_by),
    updated_at = sqlc.arg(retired_at)
WHERE id = sqlc.arg(id) AND retired_at IS NULL;

-- name: InsertTMUnits :exec
-- Zero ids stand for NULL in the nullable uuid columns, as does revision
-- 0 (arrays carry no NULLs through the driver).
INSERT INTO knowledge_tm_units (id, tenant_id, project_id, origin, translation_id, translation_revision, message_id,
                                message_key, namespace, source_locale, target_locale, source_mf2, target_mf2,
                                target_model, source_normalized, target_normalized, source_hash, signature,
                                source_vars, hit_count, last_hit_at, created_by, created_at, updated_at)
SELECT v.id, app_current_tenant(), NULLIF(v.project_id, '00000000-0000-0000-0000-000000000000'::uuid), v.origin,
       NULLIF(v.translation_id, '00000000-0000-0000-0000-000000000000'::uuid), NULLIF(v.translation_revision, 0),
       NULLIF(v.message_id, '00000000-0000-0000-0000-000000000000'::uuid), v.message_key, v.namespace,
       v.source_locale, v.target_locale, v.source_mf2, v.target_mf2, v.target_model::jsonb, v.source_normalized,
       v.target_normalized, v.source_hash, v.signature, v.source_vars::jsonb, 0, NULL, v.created_by, v.created_at,
       v.updated_at
FROM (SELECT unnest(sqlc.arg(ids)::uuid[]) AS id,
             unnest(sqlc.arg(project_ids)::uuid[]) AS project_id,
             unnest(sqlc.arg(origins)::text[]) AS origin,
             unnest(sqlc.arg(translation_ids)::uuid[]) AS translation_id,
             unnest(sqlc.arg(translation_revisions)::int[]) AS translation_revision,
             unnest(sqlc.arg(message_ids)::uuid[]) AS message_id,
             unnest(sqlc.arg(message_keys)::text[]) AS message_key,
             unnest(sqlc.arg(namespaces)::text[]) AS namespace,
             unnest(sqlc.arg(source_locales)::text[]) AS source_locale,
             unnest(sqlc.arg(target_locales)::text[]) AS target_locale,
             unnest(sqlc.arg(source_mf2s)::text[]) AS source_mf2,
             unnest(sqlc.arg(target_mf2s)::text[]) AS target_mf2,
             unnest(sqlc.arg(target_models)::text[]) AS target_model,
             unnest(sqlc.arg(source_normalizeds)::text[]) AS source_normalized,
             unnest(sqlc.arg(target_normalizeds)::text[]) AS target_normalized,
             unnest(sqlc.arg(source_hashes)::text[]) AS source_hash,
             unnest(sqlc.arg(signatures)::text[]) AS signature,
             unnest(sqlc.arg(source_vars)::text[]) AS source_vars,
             unnest(sqlc.arg(created_bys)::text[]) AS created_by,
             unnest(sqlc.arg(created_ats)::timestamptz[]) AS created_at,
             unnest(sqlc.arg(updated_ats)::timestamptz[]) AS updated_at) v;

-- name: TouchTMUnits :exec
UPDATE knowledge_tm_units u
SET translation_revision = v.revision, updated_at = sqlc.arg(updated_at)
FROM (SELECT unnest(sqlc.arg(ids)::uuid[]) AS id, unnest(sqlc.arg(revisions)::int[]) AS revision) v
WHERE u.id = v.id;

-- name: RetireTMUnits :execrows
UPDATE knowledge_tm_units u
SET retired_at = v.retired_at, retired_reason = v.reason, retired_by = v.by, updated_at = v.retired_at
FROM (SELECT unnest(sqlc.arg(ids)::uuid[]) AS id,
             unnest(sqlc.arg(retired_ats)::timestamptz[]) AS retired_at,
             unnest(sqlc.arg(reasons)::text[]) AS reason,
             unnest(sqlc.arg(bys)::text[]) AS by) v
WHERE u.id = v.id AND u.retired_at IS NULL;

-- name: GetTMUnit :one
SELECT * FROM knowledge_tm_units WHERE id = sqlc.arg(id);

-- name: LockTMUnit :one
SELECT * FROM knowledge_tm_units WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: ListTMUnits :many
-- Units in id order after the cursor. state: active, retired or all.
SELECT * FROM knowledge_tm_units
WHERE id > sqlc.arg(after)
  AND (sqlc.narg(source_locale)::text IS NULL OR source_locale = sqlc.narg(source_locale))
  AND (sqlc.narg(target_locale)::text IS NULL OR target_locale = sqlc.narg(target_locale))
  AND (sqlc.narg(project_id)::uuid IS NULL OR project_id = sqlc.narg(project_id))
  AND (sqlc.narg(translation_id)::uuid IS NULL OR translation_id = sqlc.narg(translation_id))
  -- projects limits project-owned rows to a project-scoped caller's
  -- projects (RFC 0006 §4.1); tenant-wide rows stay. Filtered here so a
  -- page's size says nothing about the others.
  AND (sqlc.narg(projects)::uuid[] IS NULL OR project_id IS NULL OR project_id = ANY (sqlc.narg(projects)::uuid[]))
  AND (sqlc.arg(state)::text = 'all'
       OR (sqlc.arg(state)::text = 'active' AND retired_at IS NULL)
       OR (sqlc.arg(state)::text = 'retired' AND retired_at IS NOT NULL))
ORDER BY id
LIMIT sqlc.arg(max_rows);

-- name: FindActiveTMUnit :one
-- An active unit with exactly this text in the locale pair and scope
-- (project, or tenant-wide when NULL): what makes a TMX import
-- idempotent.
SELECT id FROM knowledge_tm_units
WHERE retired_at IS NULL
  AND project_id IS NOT DISTINCT FROM sqlc.narg(project_id)
  AND source_locale = sqlc.arg(source_locale) AND target_locale = sqlc.arg(target_locale)
  AND source_hash = sqlc.arg(source_hash)
  AND source_mf2 = sqlc.arg(source_mf2) AND target_mf2 = sqlc.arg(target_mf2)
LIMIT 1;

-- name: CountTMHits :exec
UPDATE knowledge_tm_units SET hit_count = hit_count + 1, last_hit_at = sqlc.arg(hit_at)
WHERE id = ANY (sqlc.arg(ids)::uuid[]);

-- name: DeleteProjectTM :exec
DELETE FROM knowledge_tm_units WHERE project_id = sqlc.arg(project_id);

-- name: DeleteProjectDerivations :exec
DELETE FROM knowledge_tm_derivations WHERE project_id = sqlc.arg(project_id);

-- ── lookups ────────────────────────────────────────────────────────

-- name: ExactTMMatches :many
-- Same normalized text and placeholder signature in the locale pair,
-- the query's project first, then the most recently confirmed.
SELECT * FROM knowledge_tm_units
WHERE retired_at IS NULL
  AND source_locale = sqlc.arg(source_locale) AND target_locale = sqlc.arg(target_locale)
  AND source_hash = sqlc.arg(source_hash) AND signature = sqlc.arg(signature)
  AND (sqlc.arg(all_projects)::boolean OR project_id IS NULL OR project_id = sqlc.narg(project_id))
ORDER BY (project_id IS NOT DISTINCT FROM sqlc.narg(project_id)) DESC, updated_at DESC, id
LIMIT sqlc.arg(max_rows);

-- name: SetSimilarityThreshold :exec
-- The % operator (and so the trigram index) filters by this, for the
-- rest of the transaction.
SELECT set_config('pg_trgm.similarity_threshold', sqlc.arg(threshold)::text, true);

-- name: FuzzyTMMatches :many
-- Trigram-similar normalized text in the locale pair, most similar
-- first.
SELECT sqlc.embed(knowledge_tm_units),
       similarity(knowledge_tm_units.source_normalized, sqlc.arg(query)::text)::float8 AS similarity
FROM knowledge_tm_units
WHERE retired_at IS NULL
  AND source_locale = sqlc.arg(source_locale) AND target_locale = sqlc.arg(target_locale)
  AND (sqlc.arg(all_projects)::boolean OR project_id IS NULL OR project_id = sqlc.narg(project_id))
  AND source_normalized % sqlc.arg(query)::text
ORDER BY similarity DESC, (project_id IS NOT DISTINCT FROM sqlc.narg(project_id)) DESC, updated_at DESC, id
LIMIT sqlc.arg(max_rows);

-- name: ConcordanceSource :many
-- Active units whose source contains the pattern (ILIKE, trigram
-- indexed), closest first.
SELECT sqlc.embed(knowledge_tm_units),
       word_similarity(sqlc.arg(query)::text, knowledge_tm_units.source_normalized)::float8 AS similarity
FROM knowledge_tm_units
WHERE retired_at IS NULL
  AND (sqlc.narg(source_locale)::text IS NULL OR source_locale = sqlc.narg(source_locale))
  AND (sqlc.narg(target_locale)::text IS NULL OR target_locale = sqlc.narg(target_locale))
  AND (sqlc.arg(all_projects)::boolean OR project_id IS NULL OR project_id = sqlc.narg(project_id))
  AND source_normalized ILIKE sqlc.arg(pattern)::text
ORDER BY similarity DESC, updated_at DESC, id
LIMIT sqlc.arg(max_rows);

-- name: ConcordanceTarget :many
SELECT sqlc.embed(knowledge_tm_units),
       word_similarity(sqlc.arg(query)::text, knowledge_tm_units.target_normalized)::float8 AS similarity
FROM knowledge_tm_units
WHERE retired_at IS NULL
  AND (sqlc.narg(source_locale)::text IS NULL OR source_locale = sqlc.narg(source_locale))
  AND (sqlc.narg(target_locale)::text IS NULL OR target_locale = sqlc.narg(target_locale))
  AND (sqlc.arg(all_projects)::boolean OR project_id IS NULL OR project_id = sqlc.narg(project_id))
  AND target_normalized ILIKE sqlc.arg(pattern)::text
ORDER BY similarity DESC, updated_at DESC, id
LIMIT sqlc.arg(max_rows);
