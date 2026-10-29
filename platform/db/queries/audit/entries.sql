-- The Audit context's queries over audit_entries (migration 0045). All
-- run in the tenant's own scope; the table is INSERT and SELECT only.

-- LockAuditChain serializes appends to one tenant's chain for the rest
-- of the transaction. Two tenants never wait on each other (unless
-- their keys collide, which only costs a wait).
-- name: LockAuditChain :exec
SELECT pg_advisory_xact_lock(hashtextextended('glossa.audit_entries:' || sqlc.arg(tenant_id)::uuid::text, 0));

-- name: AuditHead :one
SELECT sequence, hash
FROM audit_entries
WHERE tenant_id = sqlc.arg(tenant_id)
ORDER BY sequence DESC
LIMIT 1;

-- AuditRecorded returns which of the given event ids already have an
-- entry: the idempotency check, made under the chain lock.
-- name: AuditRecorded :many
SELECT event_id
FROM audit_entries
WHERE tenant_id = sqlc.arg(tenant_id) AND event_id = ANY(sqlc.arg(event_ids)::uuid[]);

-- AuditOutboxCount counts the tenant's entries projected from the
-- outbox: when it equals the outbox's own count, the backfill has
-- nothing to do for the tenant.
-- name: AuditOutboxCount :one
SELECT count(*) FROM audit_entries WHERE tenant_id = sqlc.arg(tenant_id) AND source = 'outbox';

-- name: InsertAuditEntry :exec
INSERT INTO audit_entries (
    tenant_id, sequence, event_id, source, action, actor, occurred_at,
    aggregate_type, aggregate_id, project_id, locale, summary,
    request_id, trace_id, prev_hash, hash
) VALUES (
    sqlc.arg(tenant_id), sqlc.arg(sequence), sqlc.arg(event_id), sqlc.arg(source), sqlc.arg(action),
    sqlc.arg(actor), sqlc.arg(occurred_at), sqlc.arg(aggregate_type), sqlc.arg(aggregate_id),
    sqlc.narg(project_id), sqlc.narg(locale), sqlc.arg(summary), sqlc.narg(request_id), sqlc.narg(trace_id),
    sqlc.arg(prev_hash), sqlc.arg(hash)
);

-- InsertAuditEntries appends N consecutive entries in one statement
-- (#89), one element per entry in each array, in sequence order. The
-- link trigger runs per row in that order and sees the rows before it,
-- so it checks each link as N single inserts would. An empty string
-- stands for NULL in the optional columns (none of them allows one).
-- name: InsertAuditEntries :exec
INSERT INTO audit_entries (
    tenant_id, sequence, event_id, source, action, actor, occurred_at,
    aggregate_type, aggregate_id, project_id, locale, summary,
    request_id, trace_id, prev_hash, hash
)
SELECT sqlc.arg(tenant_id), u.sequence, u.event_id, u.source, u.action, u.actor, u.occurred_at,
       u.aggregate_type, u.aggregate_id, nullif(u.project_id, '')::uuid, nullif(u.locale, ''), u.summary,
       nullif(u.request_id, ''), nullif(u.trace_id, ''), u.prev_hash, u.hash
FROM (SELECT unnest(sqlc.arg(sequences)::bigint[]) AS sequence,
        unnest(sqlc.arg(event_ids)::uuid[]) AS event_id,
        unnest(sqlc.arg(sources)::text[]) AS source,
        unnest(sqlc.arg(actions)::text[]) AS action,
        unnest(sqlc.arg(actors)::text[]) AS actor,
        unnest(sqlc.arg(occurred_ats)::timestamptz[]) AS occurred_at,
        unnest(sqlc.arg(aggregate_types)::text[]) AS aggregate_type,
        unnest(sqlc.arg(aggregate_ids)::text[]) AS aggregate_id,
        unnest(sqlc.arg(project_ids)::text[]) AS project_id,
        unnest(sqlc.arg(locales)::text[]) AS locale,
        unnest(sqlc.arg(summaries)::jsonb[]) AS summary,
        unnest(sqlc.arg(request_ids)::text[]) AS request_id,
        unnest(sqlc.arg(trace_ids)::text[]) AS trace_id,
        unnest(sqlc.arg(prev_hashes)::bytea[]) AS prev_hash,
        unnest(sqlc.arg(hashes)::bytea[]) AS hash) AS u
ORDER BY u.sequence;

-- AuditEntriesAfter reads the chain in order from after a sequence.
-- name: AuditEntriesAfter :many
SELECT tenant_id, sequence, event_id, source, action, actor, occurred_at,
       aggregate_type, aggregate_id, project_id, locale, summary,
       request_id, trace_id, prev_hash, hash
FROM audit_entries
WHERE tenant_id = sqlc.arg(tenant_id) AND sequence > sqlc.arg(after_sequence)
ORDER BY sequence
LIMIT sqlc.arg(page_size)::int;

-- ── the read API (RFC 0006 §6.2, wave 5) ───────────────────────────

-- AuditEntryAt reads one entry by its place in the chain.
-- name: AuditEntryAt :one
SELECT tenant_id, sequence, event_id, source, action, actor, occurred_at,
       aggregate_type, aggregate_id, project_id, locale, summary,
       request_id, trace_id, prev_hash, hash
FROM audit_entries
WHERE tenant_id = sqlc.arg(tenant_id) AND sequence = sqlc.arg(sequence);

-- AuditEntriesFilteredAsc and …Desc list entries matching every filter
-- that is set, in chain order or newest first, past a cursor sequence.
-- projects, when set, limits them to those projects: a project-scoped
-- caller's, which leaves out the tenant-level entries (a NULL
-- project_id never matches ANY).
-- name: AuditEntriesFilteredAsc :many
SELECT tenant_id, sequence, event_id, source, action, actor, occurred_at,
       aggregate_type, aggregate_id, project_id, locale, summary,
       request_id, trace_id, prev_hash, hash
FROM audit_entries
WHERE tenant_id = sqlc.arg(tenant_id)
  AND sequence > sqlc.arg(after_sequence)
  AND (sqlc.narg(occurred_from)::timestamptz IS NULL OR occurred_at >= sqlc.narg(occurred_from))
  AND (sqlc.narg(occurred_to)::timestamptz IS NULL OR occurred_at < sqlc.narg(occurred_to))
  AND (sqlc.narg(first_sequence)::bigint IS NULL OR sequence >= sqlc.narg(first_sequence))
  AND (sqlc.narg(last_sequence)::bigint IS NULL OR sequence <= sqlc.narg(last_sequence))
  AND (sqlc.narg(actor)::text IS NULL OR actor = sqlc.narg(actor))
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action))
  AND (sqlc.narg(source)::text IS NULL OR source = sqlc.narg(source))
  AND (sqlc.narg(aggregate_type)::text IS NULL OR aggregate_type = sqlc.narg(aggregate_type))
  AND (sqlc.narg(aggregate_id)::text IS NULL OR aggregate_id = sqlc.narg(aggregate_id))
  AND (sqlc.narg(project)::uuid IS NULL OR project_id = sqlc.narg(project))
  AND (sqlc.narg(projects)::uuid[] IS NULL OR project_id = ANY(sqlc.narg(projects)::uuid[]))
ORDER BY sequence
LIMIT sqlc.arg(page_size)::int;

-- name: AuditEntriesFilteredDesc :many
SELECT tenant_id, sequence, event_id, source, action, actor, occurred_at,
       aggregate_type, aggregate_id, project_id, locale, summary,
       request_id, trace_id, prev_hash, hash
FROM audit_entries
WHERE tenant_id = sqlc.arg(tenant_id)
  AND sequence < sqlc.arg(before_sequence)
  AND (sqlc.narg(occurred_from)::timestamptz IS NULL OR occurred_at >= sqlc.narg(occurred_from))
  AND (sqlc.narg(occurred_to)::timestamptz IS NULL OR occurred_at < sqlc.narg(occurred_to))
  AND (sqlc.narg(first_sequence)::bigint IS NULL OR sequence >= sqlc.narg(first_sequence))
  AND (sqlc.narg(last_sequence)::bigint IS NULL OR sequence <= sqlc.narg(last_sequence))
  AND (sqlc.narg(actor)::text IS NULL OR actor = sqlc.narg(actor))
  AND (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action))
  AND (sqlc.narg(source)::text IS NULL OR source = sqlc.narg(source))
  AND (sqlc.narg(aggregate_type)::text IS NULL OR aggregate_type = sqlc.narg(aggregate_type))
  AND (sqlc.narg(aggregate_id)::text IS NULL OR aggregate_id = sqlc.narg(aggregate_id))
  AND (sqlc.narg(project)::uuid IS NULL OR project_id = sqlc.narg(project))
  AND (sqlc.narg(projects)::uuid[] IS NULL OR project_id = ANY(sqlc.narg(projects)::uuid[]))
ORDER BY sequence DESC
LIMIT sqlc.arg(page_size)::int;

-- AuditOccurredSpan is the chain segment a time range's entries occupy
-- — the least and greatest sequence that occurred in [from, to) — and
-- how many entries occurred in it. An export of the range is that
-- segment, and only when the segment holds no other entry.
-- name: AuditOccurredSpan :one
SELECT coalesce(min(sequence), 0)::bigint AS first_sequence,
       coalesce(max(sequence), 0)::bigint AS last_sequence,
       count(*)::bigint AS inside
FROM audit_entries
WHERE tenant_id = sqlc.arg(tenant_id)
  AND occurred_at >= sqlc.arg(occurred_from) AND occurred_at < sqlc.arg(occurred_to);
