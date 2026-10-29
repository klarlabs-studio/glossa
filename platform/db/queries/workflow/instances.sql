-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.
--
-- Workflow instances and their transition log (RFC 0006 §2.5, migration
-- 0043). Stepping locks the instance row for the rest of the
-- transaction, so two events about one subject serialize.

-- name: LockActiveInstancesOfSubject :many
-- The active instances of one subject, locked, in id order so two
-- handlers locking several never deadlock on each other.
SELECT * FROM workflow_instances
WHERE subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id) AND locale = sqlc.arg(locale)
  AND status = 'active'
ORDER BY id
FOR UPDATE;

-- name: LockActiveInstancesOfProject :many
-- A project's active instances of one subject kind, locked, in id order
-- (a project-wide event such as check_run.recorded).
SELECT * FROM workflow_instances
WHERE project_id = sqlc.arg(project_id) AND subject_kind = sqlc.arg(subject_kind) AND status = 'active'
ORDER BY id
LIMIT sqlc.arg(max_rows)
FOR UPDATE;

-- name: LockInstance :one
SELECT * FROM workflow_instances WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: InsertInstance :execrows
-- Loses quietly to a concurrent first trigger that already created the
-- active instance of this definition and subject; the caller then
-- locks and steps that one.
INSERT INTO workflow_instances (id, tenant_id, project_id, definition_id, version, subject_kind, subject_id, locale,
                                state, snapshot, status, created_at, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(definition_id), sqlc.arg(version),
        sqlc.arg(subject_kind), sqlc.arg(subject_id), sqlc.arg(locale), '', sqlc.narg(snapshot), 'active',
        sqlc.arg(created_at), sqlc.arg(created_at))
ON CONFLICT (tenant_id, definition_id, subject_kind, subject_id, locale) WHERE status = 'active' DO NOTHING;

-- name: UpdateInstance :execrows
UPDATE workflow_instances
SET state = sqlc.arg(state), snapshot = sqlc.narg(snapshot), status = sqlc.arg(status),
    due_at = sqlc.narg(due_at), overdue_at = sqlc.narg(overdue_at), timer_state = sqlc.narg(timer_state),
    updated_at = sqlc.arg(updated_at), finished_at = sqlc.narg(finished_at)
WHERE id = sqlc.arg(id);

-- name: RebaseInstance :execrows
-- Moves a locked, active instance to another version of its definition
-- (migration 0056 grants the column). Its state, status and timer stay.
UPDATE workflow_instances
SET version = sqlc.arg(version), snapshot = sqlc.narg(snapshot), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status = 'active';

-- name: DeleteFinishedInstances :execrows
-- Retention (migration 0056): up to max_rows instances that finished
-- before the cutoff, oldest first, with their transition logs (the
-- foreign key cascades). The policy lets the application role delete
-- nothing but finished instances; the WHERE says so too.
DELETE FROM workflow_instances
WHERE id IN (
    SELECT f.id FROM workflow_instances f
    WHERE f.status = 'finished' AND f.finished_at < sqlc.arg(cutoff)::timestamptz
    ORDER BY f.finished_at, f.id
    LIMIT sqlc.arg(max_rows)
    FOR UPDATE SKIP LOCKED
);

-- name: ListTenantsWithExpiredInstances :many
-- System scope workflow.retention (db.SystemTx): which tenants hold an
-- instance that finished before the cutoff. Reads only the columns
-- migrations 0043 and 0056 grant glossa_system.
SELECT DISTINCT tenant_id FROM workflow_instances
WHERE status = 'finished' AND finished_at < sqlc.arg(cutoff)
LIMIT sqlc.arg(max_rows);

-- name: HasTransition :one
SELECT EXISTS (
    SELECT 1 FROM workflow_transitions
    WHERE instance_id = sqlc.arg(instance_id) AND outbox_event_id = sqlc.arg(outbox_event_id)
);

-- name: AppendTransition :exec
-- The next number in the instance's log. The caller holds the
-- instance's row lock, so no other append can take the same one.
INSERT INTO workflow_transitions (tenant_id, instance_id, seq, from_state, event, to_state, outcome, guards, actions,
                                  actor, outbox_event_id, at)
VALUES (app_current_tenant(), sqlc.arg(instance_id),
        (SELECT coalesce(max(seq), 0) + 1 FROM workflow_transitions t WHERE t.instance_id = sqlc.arg(instance_id)),
        sqlc.arg(from_state), sqlc.arg(event), sqlc.arg(to_state), sqlc.arg(outcome), sqlc.arg(guards),
        sqlc.arg(actions), sqlc.arg(actor), sqlc.arg(outbox_event_id), sqlc.arg(at));

-- name: LockDueTimers :many
-- The active instances whose timer has fallen due; ones another sweep
-- is raising are skipped rather than waited for.
SELECT * FROM workflow_instances
WHERE status = 'active' AND timer_state IS NOT NULL AND LEAST(due_at, overdue_at) <= sqlc.arg(now)
ORDER BY LEAST(due_at, overdue_at), id
LIMIT sqlc.arg(max_rows)
FOR UPDATE SKIP LOCKED;

-- name: GetInstance :one
SELECT * FROM workflow_instances WHERE id = sqlc.arg(id);

-- name: ListInstances :many
-- Newest first; the cursor is the last id of the previous page (ids are
-- UUIDv7, so id order is creation order).
SELECT * FROM workflow_instances
WHERE (sqlc.narg(project_id)::uuid IS NULL OR project_id = sqlc.narg(project_id)::uuid)
  AND (sqlc.narg(definition_id)::uuid IS NULL OR definition_id = sqlc.narg(definition_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(locale)::text IS NULL OR locale = sqlc.narg(locale)::text)
  AND (sqlc.narg(subject_id)::uuid IS NULL OR subject_id = sqlc.narg(subject_id)::uuid)
  AND (sqlc.narg(after)::uuid IS NULL OR id < sqlc.narg(after)::uuid)
ORDER BY id DESC
LIMIT sqlc.arg(max_rows);

-- name: ListTransitions :many
SELECT * FROM workflow_transitions WHERE instance_id = sqlc.arg(instance_id) ORDER BY seq;

-- name: InsertSeed :execrows
-- The once of "seeded once per tenant": a second seeding loses here.
INSERT INTO workflow_seeds (tenant_id, definition_id, seeded_at)
VALUES (app_current_tenant(), sqlc.narg(definition_id), sqlc.arg(seeded_at))
ON CONFLICT DO NOTHING;

-- name: HasSeed :one
SELECT EXISTS (SELECT 1 FROM workflow_seeds);

-- name: LiveDefinitionNamed :one
-- Whether the tenant already has a tenant-wide live definition of this
-- name (the default is then the tenant's own, and is left alone).
SELECT EXISTS (
    SELECT 1 FROM workflow_definitions WHERE project_id IS NULL AND name = sqlc.arg(name) AND deleted_at IS NULL
);

-- name: LiveTenantDefinition :one
-- The tenant-wide live definition of this name: the seeded release
-- approval definition a release request runs on when no binding names
-- another (RFC 0006 §5.1).
SELECT * FROM workflow_definitions WHERE project_id IS NULL AND name = sqlc.arg(name) AND deleted_at IS NULL;

-- name: ListTenantsWithDueTimers :many
-- System scope workflow.timers (db.SystemTx): which tenants have a timer
-- due. Reads only the columns migration 0043 grants glossa_system.
SELECT DISTINCT tenant_id FROM workflow_instances
WHERE status = 'active' AND timer_state IS NOT NULL AND LEAST(due_at, overdue_at) <= sqlc.arg(now)
LIMIT sqlc.arg(max_rows);

-- name: CountInstancesByStatus :many
-- System scope workflow.timers: the glossa_workflow_instances gauge
-- (RFC 0006 §10.1). Reads only the status migration 0043 grants.
SELECT status, count(*)::integer AS instances FROM workflow_instances GROUP BY status;

-- name: CountLiveAssignments :one
-- System scope workflow.timers: the glossa_assignments_open gauge
-- (RFC 0006 §10.1). Reads only the state and due date migration 0055
-- grants.
SELECT
    count(*) FILTER (WHERE due_at IS NOT NULL AND due_at < sqlc.arg(now))::integer AS overdue,
    count(*) FILTER (WHERE due_at IS NULL OR due_at >= sqlc.arg(now))::integer AS on_time
FROM workflow_assignments WHERE state IN ('open', 'accepted');
