-- The outbox's history: events already delivered, read again by a
-- projection built after them (the audit backfill, RFC 0006 §6.1).
-- outbox_events has never been purged, so it is the record of every
-- domain event since the platform started.

-- OutboxHistoryTenants lists the tenants that have recorded events. It
-- runs in the "outbox.history" system scope (glossa_system may read
-- tenants and outbox_events, and nothing here writes).
-- name: OutboxHistoryTenants :many
SELECT t.id
FROM tenants t
WHERE EXISTS (SELECT 1 FROM outbox_events o WHERE o.tenant_id = t.id)
ORDER BY t.id;

-- OutboxHistoryCount counts the tenant's recorded events (tenant scope).
-- name: OutboxHistoryCount :one
SELECT count(*) FROM outbox_events WHERE tenant_id = sqlc.arg(tenant_id);

-- OutboxHistoryPage reads one page of the tenant's events in the order
-- they occurred, after a keyset cursor. It runs in the tenant's scope,
-- so row-level security keeps it to that tenant.
-- name: OutboxHistoryPage :many
SELECT id, event_type, aggregate_type, aggregate_id, actor, payload, trace_context, occurred_at
FROM outbox_events
WHERE tenant_id = sqlc.arg(tenant_id)
  AND (occurred_at, id) > (sqlc.arg(after_at)::timestamptz, sqlc.arg(after_id)::uuid)
ORDER BY occurred_at, id
LIMIT sqlc.arg(page_size)::int;
