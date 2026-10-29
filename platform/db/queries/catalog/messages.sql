-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.

-- name: InsertMessage :execrows
-- ON CONFLICT (id) makes a retried idempotent create a no-op; a key
-- clash still fails on the (project_id, key) constraint.
INSERT INTO catalog_messages (id, tenant_id, project_id, key, namespace, description, max_length,
                              state, source_syntax, source_text, source_model, arguments, markup,
                              source_revision, version, created_by, created_at, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(key), sqlc.arg(namespace),
        sqlc.arg(description), sqlc.narg(max_length), sqlc.arg(state), sqlc.arg(source_syntax),
        sqlc.arg(source_text), sqlc.arg(source_model), sqlc.arg(arguments), sqlc.arg(markup),
        sqlc.arg(source_revision), sqlc.arg(version), sqlc.arg(created_by), sqlc.arg(created_at),
        sqlc.arg(updated_at))
ON CONFLICT (id) DO NOTHING;

-- name: InsertSourceRevision :exec
INSERT INTO catalog_source_revisions (tenant_id, message_id, revision, syntax, text, model, author, created_at)
VALUES (app_current_tenant(), sqlc.arg(message_id), sqlc.arg(revision), sqlc.arg(syntax), sqlc.arg(text),
        sqlc.arg(model), sqlc.arg(author), sqlc.arg(created_at));

-- name: GetMessage :one
SELECT * FROM catalog_messages WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: GetMessageByKey :one
SELECT * FROM catalog_messages WHERE project_id = sqlc.arg(project_id) AND key = sqlc.arg(key);

-- name: LockMessageByKey :one
SELECT * FROM catalog_messages
WHERE project_id = sqlc.arg(project_id) AND key = sqlc.arg(key)
FOR UPDATE;

-- name: LockMessagesByKeys :many
-- One lock order (by key) for every bulk writer.
SELECT * FROM catalog_messages
WHERE project_id = sqlc.arg(project_id) AND key = ANY (sqlc.arg(keys)::text[])
ORDER BY key
FOR UPDATE;

-- name: GetMessagesByKeys :many
SELECT * FROM catalog_messages
WHERE project_id = sqlc.arg(project_id) AND key = ANY (sqlc.arg(keys)::text[])
ORDER BY key;

-- name: GetMessagesByIDs :many
SELECT * FROM catalog_messages
WHERE project_id = sqlc.arg(project_id) AND id = ANY (sqlc.arg(ids)::uuid[]);

-- name: ListMessages :many
-- Keyset pagination on key. key_like is a LIKE pattern with escaped
-- wildcards ('checkout.%'), so the text_pattern_ops index serves it.
SELECT * FROM catalog_messages
WHERE project_id = sqlc.arg(project_id)
  AND key > sqlc.arg(after)
  AND (sqlc.narg(namespace)::text IS NULL OR namespace = sqlc.narg(namespace))
  AND (sqlc.narg(state)::text IS NULL OR state = sqlc.narg(state))
  AND (sqlc.narg(key_like)::text IS NULL OR key LIKE sqlc.narg(key_like))
  -- only limits the page to ids: the messages an assigned member's
  -- units are in (RFC 0006 §3.3), filtered before the LIMIT.
  AND (NOT sqlc.arg(filter_ids)::boolean OR id = ANY (sqlc.arg(ids)::uuid[]))
ORDER BY key
LIMIT sqlc.arg(max_rows);

-- name: ListNamespaces :many
-- A page of a project's namespaces after a name, with message counts by
-- state; catalog_messages_namespaces serves it from the index alone.
SELECT namespace,
       count(*) FILTER (WHERE state = 'active')::int AS active,
       count(*) FILTER (WHERE state = 'obsolete')::int AS obsolete
FROM catalog_messages
WHERE project_id = sqlc.arg(project_id) AND namespace > sqlc.arg(after)
GROUP BY namespace
ORDER BY namespace
LIMIT sqlc.arg(max_rows);

-- name: ListActiveMessages :many
-- Every active message of a project, for a release snapshot.
SELECT * FROM catalog_messages
WHERE project_id = sqlc.arg(project_id) AND state = 'active'
ORDER BY key;

-- name: UpdateMessage :execrows
UPDATE catalog_messages
SET key = sqlc.arg(key), namespace = sqlc.arg(namespace), description = sqlc.arg(description),
    max_length = sqlc.narg(max_length), state = sqlc.arg(state), source_syntax = sqlc.arg(source_syntax),
    source_text = sqlc.arg(source_text), source_model = sqlc.arg(source_model),
    arguments = sqlc.arg(arguments), markup = sqlc.arg(markup),
    source_revision = sqlc.arg(source_revision), version = sqlc.arg(version),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(expected_version);

-- name: ListSourceRevisions :many
-- Newest first; before is exclusive.
SELECT * FROM catalog_source_revisions
WHERE message_id = sqlc.arg(message_id) AND revision < sqlc.arg(before)
ORDER BY revision DESC
LIMIT sqlc.arg(max_rows);

-- name: GetSourceRevision :one
SELECT * FROM catalog_source_revisions
WHERE message_id = sqlc.arg(message_id) AND revision = sqlc.arg(revision);

-- name: GetSourceRevisionsOf :many
-- The revisions of the project's messages named by parallel arrays (one
-- element per wanted revision); a pair with no row is simply absent.
SELECT r.* FROM catalog_source_revisions r
JOIN catalog_messages m ON m.id = r.message_id AND m.project_id = sqlc.arg(project_id)
JOIN (SELECT unnest(sqlc.arg(message_ids)::uuid[]) AS message_id,
             unnest(sqlc.arg(revisions)::int[]) AS revision) w
  ON w.message_id = r.message_id AND w.revision = r.revision;

-- ── bulk writes ────────────────────────────────────────────────────
-- A bulk upsert writes all its rows in one statement per table instead
-- of one round trip per message (#77). Each takes parallel arrays, one
-- element per row. max_lengths uses 0 for "no limit": the column only
-- admits 1..100000.

-- name: InsertMessages :execrows
-- ON CONFLICT (id) as in InsertMessage.
INSERT INTO catalog_messages (id, tenant_id, project_id, key, namespace, description, max_length,
                              state, source_syntax, source_text, source_model, arguments, markup,
                              source_revision, version, created_by, created_at, updated_at)
SELECT u.id, app_current_tenant(), u.project_id, u.key, u.namespace, u.description, nullif(u.max_length, 0),
       u.state, u.source_syntax, u.source_text, u.source_model, u.arguments, u.markup,
       u.source_revision, u.version, u.created_by, u.created_at, u.updated_at
FROM (SELECT unnest(sqlc.arg(ids)::uuid[]) AS id,
        unnest(sqlc.arg(project_ids)::uuid[]) AS project_id,
        unnest(sqlc.arg(keys)::text[]) AS key,
        unnest(sqlc.arg(namespaces)::text[]) AS namespace,
        unnest(sqlc.arg(descriptions)::text[]) AS description,
        unnest(sqlc.arg(max_lengths)::int[]) AS max_length,
        unnest(sqlc.arg(states)::text[]) AS state,
        unnest(sqlc.arg(source_syntaxes)::text[]) AS source_syntax,
        unnest(sqlc.arg(source_texts)::text[]) AS source_text,
        unnest(sqlc.arg(source_models)::jsonb[]) AS source_model,
        unnest(sqlc.arg(arguments)::jsonb[]) AS arguments,
        unnest(sqlc.arg(markups)::jsonb[]) AS markup,
        unnest(sqlc.arg(source_revisions)::int[]) AS source_revision,
        unnest(sqlc.arg(versions)::int[]) AS version,
        unnest(sqlc.arg(created_bys)::text[]) AS created_by,
        unnest(sqlc.arg(created_ats)::timestamptz[]) AS created_at,
        unnest(sqlc.arg(updated_ats)::timestamptz[]) AS updated_at) AS u
ON CONFLICT (id) DO NOTHING;

-- name: UpdateMessages :execrows
-- Each row only if its stored version is still the expected one; the
-- caller compares the count with the number of rows.
UPDATE catalog_messages m
SET key = u.key, namespace = u.namespace, description = u.description, max_length = nullif(u.max_length, 0),
    state = u.state, source_syntax = u.source_syntax, source_text = u.source_text,
    source_model = u.source_model, arguments = u.arguments, markup = u.markup,
    source_revision = u.source_revision, version = u.version, updated_at = u.updated_at
FROM (SELECT unnest(sqlc.arg(ids)::uuid[]) AS id,
        unnest(sqlc.arg(keys)::text[]) AS key,
        unnest(sqlc.arg(namespaces)::text[]) AS namespace,
        unnest(sqlc.arg(descriptions)::text[]) AS description,
        unnest(sqlc.arg(max_lengths)::int[]) AS max_length,
        unnest(sqlc.arg(states)::text[]) AS state,
        unnest(sqlc.arg(source_syntaxes)::text[]) AS source_syntax,
        unnest(sqlc.arg(source_texts)::text[]) AS source_text,
        unnest(sqlc.arg(source_models)::jsonb[]) AS source_model,
        unnest(sqlc.arg(arguments)::jsonb[]) AS arguments,
        unnest(sqlc.arg(markups)::jsonb[]) AS markup,
        unnest(sqlc.arg(source_revisions)::int[]) AS source_revision,
        unnest(sqlc.arg(versions)::int[]) AS version,
        unnest(sqlc.arg(updated_ats)::timestamptz[]) AS updated_at,
        unnest(sqlc.arg(expected_versions)::int[]) AS expected_version) AS u
WHERE m.id = u.id AND m.version = u.expected_version;

-- name: InsertSourceRevisions :exec
INSERT INTO catalog_source_revisions (tenant_id, message_id, revision, syntax, text, model, author, created_at)
SELECT app_current_tenant(), u.message_id, u.revision, u.syntax, u.text, u.model, u.author, u.created_at
FROM (SELECT unnest(sqlc.arg(message_ids)::uuid[]) AS message_id,
        unnest(sqlc.arg(revisions)::int[]) AS revision,
        unnest(sqlc.arg(syntaxes)::text[]) AS syntax,
        unnest(sqlc.arg(texts)::text[]) AS text,
        unnest(sqlc.arg(models)::jsonb[]) AS model,
        unnest(sqlc.arg(authors)::text[]) AS author,
        unnest(sqlc.arg(created_ats)::timestamptz[]) AS created_at) AS u;
