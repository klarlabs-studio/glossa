-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.
--
-- Assignments and approvals (RFC 0006 §3.1–3.2, migration 0044).

-- ── assignments ─────────────────────────────────────────────────────

-- name: InsertAssignment :exec
INSERT INTO workflow_assignments (id, tenant_id, project_id, instance_id, assignee, permission, due_at, state,
                                  version, created_by, created_at, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.narg(instance_id), sqlc.arg(assignee),
        sqlc.arg(permission), sqlc.narg(due_at), sqlc.arg(state), sqlc.arg(version), sqlc.arg(created_by),
        sqlc.arg(created_at), sqlc.arg(created_at));

-- name: LockAssignee :exec
-- Serializes the assignments made to one assignee in this tenant, so
-- the limit of open assignments (RFC 0006 §9.6) holds under concurrent
-- assigning: the count below is read under this lock.
SELECT pg_advisory_xact_lock(hashtextextended(
    'workflow.assignee:' || app_current_tenant()::text || ':' || sqlc.arg(assignee)::text, 0));

-- name: CountLiveAssignmentsOf :one
SELECT count(*)::integer FROM workflow_assignments
WHERE assignee = sqlc.arg(assignee) AND state IN ('open', 'accepted');

-- name: InsertAssignmentUnits :exec
INSERT INTO workflow_assignment_units (tenant_id, assignment_id, message_id, locale)
SELECT app_current_tenant(), sqlc.arg(assignment_id), u.message_id, u.locale
FROM (SELECT unnest(sqlc.arg(message_ids)::uuid[]) AS message_id, unnest(sqlc.arg(locales)::text[]) AS locale) AS u;

-- name: GetAssignment :one
SELECT * FROM workflow_assignments WHERE id = sqlc.arg(id);

-- name: LockAssignment :one
SELECT * FROM workflow_assignments WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: AssignmentUnits :many
SELECT assignment_id, message_id, locale FROM workflow_assignment_units
WHERE assignment_id = ANY(sqlc.arg(assignment_ids)::uuid[])
ORDER BY assignment_id, message_id, locale;

-- name: UpdateAssignment :execrows
UPDATE workflow_assignments
SET state = sqlc.arg(state), version = sqlc.arg(version), updated_at = sqlc.arg(updated_at),
    closed_by = sqlc.narg(closed_by), closed_at = sqlc.narg(closed_at), reason = sqlc.arg(reason)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(version) - 1;

-- name: ListAssignments :many
SELECT * FROM workflow_assignments
WHERE id > sqlc.arg(after)
  AND (sqlc.arg(project_id)::uuid = '00000000-0000-0000-0000-000000000000' OR project_id = sqlc.arg(project_id))
  AND (cardinality(sqlc.arg(states)::text[]) = 0 OR state = ANY(sqlc.arg(states)::text[]))
  AND (NOT sqlc.arg(by_assignee)::boolean OR assignee = ANY(sqlc.arg(assignees)::text[]))
  -- A project-scoped caller's lists are cut in the query, before the
  -- page is, so a page's size never counts rows they cannot see.
  AND (NOT sqlc.arg(by_projects)::boolean OR project_id = ANY(sqlc.arg(projects)::uuid[]))
  -- One unit, or one message's or one locale's: the assignments that
  -- cover it.
  AND (NOT sqlc.arg(by_unit)::boolean OR EXISTS (
        SELECT 1 FROM workflow_assignment_units u
        WHERE u.tenant_id = workflow_assignments.tenant_id AND u.assignment_id = workflow_assignments.id
          AND (sqlc.arg(unit_message)::uuid = '00000000-0000-0000-0000-000000000000' OR u.message_id = sqlc.arg(unit_message))
          AND (sqlc.arg(unit_locale)::text = '' OR u.locale = sqlc.arg(unit_locale))))
ORDER BY id
LIMIT sqlc.arg(max_rows);

-- name: CoveredUnits :many
-- The units that assignments to any of the assignees cover in a
-- project: live ones, and those completed since done_since (§3.3).
SELECT DISTINCT u.message_id, u.locale
FROM workflow_assignments a
JOIN workflow_assignment_units u ON u.tenant_id = a.tenant_id AND u.assignment_id = a.id
WHERE a.project_id = sqlc.arg(project_id)
  AND a.assignee = ANY(sqlc.arg(assignees)::text[])
  AND (a.state IN ('open', 'accepted') OR (a.state = 'done' AND a.closed_at >= sqlc.arg(done_since)))
  AND (NOT sqlc.arg(one_unit)::boolean OR (u.message_id = sqlc.arg(message_id) AND u.locale = sqlc.arg(locale)))
ORDER BY u.message_id, u.locale;

-- ── approvals ───────────────────────────────────────────────────────

-- name: InsertApproval :exec
INSERT INTO workflow_approvals (id, tenant_id, project_id, instance_id, subject_kind, subject_id, locale, required,
                                eligible, distinct_from_author, due_at, state, version, created_by, created_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.narg(instance_id), sqlc.arg(subject_kind),
        sqlc.arg(subject_id), sqlc.arg(locale), sqlc.arg(required), sqlc.arg(eligible),
        sqlc.arg(distinct_from_author), sqlc.narg(due_at), sqlc.arg(state), sqlc.arg(version),
        sqlc.arg(created_by), sqlc.arg(created_at));

-- name: GetApproval :one
SELECT * FROM workflow_approvals WHERE id = sqlc.arg(id);

-- name: LockApproval :one
SELECT * FROM workflow_approvals WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: LatestApproval :one
SELECT * FROM workflow_approvals
WHERE project_id = sqlc.arg(project_id) AND subject_kind = sqlc.arg(subject_kind)
  AND subject_id = sqlc.arg(subject_id) AND locale = sqlc.arg(locale)
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: ListApprovals :many
SELECT * FROM workflow_approvals
WHERE id > sqlc.arg(after)
  AND (sqlc.arg(project_id)::uuid = '00000000-0000-0000-0000-000000000000' OR project_id = sqlc.arg(project_id))
  AND (sqlc.arg(subject_kind)::text = '' OR subject_kind = sqlc.arg(subject_kind))
  AND (sqlc.arg(subject_id)::uuid = '00000000-0000-0000-0000-000000000000' OR subject_id = sqlc.arg(subject_id))
  AND (sqlc.arg(locale)::text = '' OR locale = sqlc.arg(locale))
  AND (cardinality(sqlc.arg(states)::text[]) = 0 OR state = ANY(sqlc.arg(states)::text[]))
  AND (NOT sqlc.arg(by_projects)::boolean OR project_id = ANY(sqlc.arg(projects)::uuid[]))
ORDER BY id
LIMIT sqlc.arg(max_rows);

-- name: ApprovalDecisions :many
SELECT * FROM workflow_approval_decisions WHERE approval_id = sqlc.arg(approval_id) ORDER BY seq;

-- name: DecisionsOf :many
SELECT * FROM workflow_approval_decisions
WHERE approval_id = ANY(sqlc.arg(approval_ids)::uuid[])
ORDER BY approval_id, seq;

-- name: InsertDecision :exec
INSERT INTO workflow_approval_decisions (tenant_id, approval_id, seq, principal, verdict, reason, decided_at)
VALUES (app_current_tenant(), sqlc.arg(approval_id), sqlc.arg(seq), sqlc.arg(principal), sqlc.arg(verdict),
        sqlc.arg(reason), sqlc.arg(decided_at));

-- name: UpdateApproval :execrows
UPDATE workflow_approvals
SET state = sqlc.arg(state), version = sqlc.arg(version), closed_at = sqlc.narg(closed_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(version) - 1;
