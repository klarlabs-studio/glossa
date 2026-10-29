-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.

-- name: InsertBranch :execrows
-- Two first pushes of one branch race on (project_id, name); the loser
-- inserts nothing and locks the winner's row.
INSERT INTO catalog_branches (id, tenant_id, project_id, name, pr_number, head_commit, state, preview_url,
                              closed_at, removed_keys, invalid_items, version, created_by, created_at, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(project_id), sqlc.arg(name), sqlc.narg(pr_number),
        sqlc.arg(head_commit), sqlc.arg(state), sqlc.arg(preview_url), sqlc.narg(closed_at),
        sqlc.arg(removed_keys)::text[], sqlc.arg(invalid_items), sqlc.arg(version), sqlc.arg(created_by),
        sqlc.arg(created_at), sqlc.arg(updated_at))
ON CONFLICT (project_id, name) DO NOTHING;

-- name: GetBranch :one
SELECT * FROM catalog_branches WHERE project_id = sqlc.arg(project_id) AND name = sqlc.arg(name);

-- name: LockBranch :one
SELECT * FROM catalog_branches
WHERE project_id = sqlc.arg(project_id) AND name = sqlc.arg(name)
FOR UPDATE;

-- name: GetBranchByID :one
-- URLs address a branch by its ID: a branch name may hold slashes.
SELECT * FROM catalog_branches WHERE project_id = sqlc.arg(project_id) AND id = sqlc.arg(id);

-- name: GetBranchesByIDs :many
SELECT * FROM catalog_branches WHERE id = ANY (sqlc.arg(ids)::uuid[]);

-- name: ListBranches :many
-- A project's branches by name, after the given one, optionally in one
-- state (the Branches API's state filter).
SELECT * FROM catalog_branches
WHERE project_id = sqlc.arg(project_id) AND name > sqlc.arg(after)
  AND (sqlc.narg(state)::text IS NULL OR state = sqlc.narg(state))
ORDER BY name
LIMIT sqlc.arg(max_rows);

-- name: UpdateBranch :execrows
UPDATE catalog_branches
SET pr_number = sqlc.narg(pr_number), head_commit = sqlc.arg(head_commit), state = sqlc.arg(state),
    preview_url = sqlc.arg(preview_url), closed_at = sqlc.narg(closed_at),
    removed_keys = sqlc.arg(removed_keys)::text[], invalid_items = sqlc.arg(invalid_items),
    version = sqlc.arg(version), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(expected_version);

-- name: UpsertProposal :exec
INSERT INTO catalog_proposals (tenant_id, branch_id, key, message_id, kind, syntax, text, model, base_revision,
                               author, created_at, updated_at)
VALUES (app_current_tenant(), sqlc.arg(branch_id), sqlc.arg(key), sqlc.arg(message_id), sqlc.arg(kind),
        sqlc.arg(syntax), sqlc.arg(text), sqlc.arg(model), sqlc.narg(base_revision), sqlc.arg(author),
        sqlc.arg(created_at), sqlc.arg(updated_at))
ON CONFLICT (branch_id, key) DO UPDATE
SET message_id = EXCLUDED.message_id, kind = EXCLUDED.kind, syntax = EXCLUDED.syntax, text = EXCLUDED.text,
    model = EXCLUDED.model, base_revision = EXCLUDED.base_revision, author = EXCLUDED.author,
    updated_at = EXCLUDED.updated_at;

-- name: DeleteProposal :exec
DELETE FROM catalog_proposals WHERE branch_id = sqlc.arg(branch_id) AND key = sqlc.arg(key);

-- DeleteProposals deletes many branches' proposals in one statement
-- (#89), one (branch_id, key) pair per array element.
-- name: DeleteProposals :exec
DELETE FROM catalog_proposals p
USING (SELECT unnest(sqlc.arg(branch_ids)::uuid[]) AS branch_id,
              unnest(sqlc.arg(keys)::text[]) AS key) AS d
WHERE p.branch_id = d.branch_id AND p.key = d.key;

-- name: ListBranchProposals :many
SELECT * FROM catalog_proposals WHERE branch_id = sqlc.arg(branch_id) ORDER BY key;

-- name: ListBranchProposalsPage :many
-- One page of a branch's proposals, by key (the Branches API).
SELECT * FROM catalog_proposals
WHERE branch_id = sqlc.arg(branch_id) AND key > sqlc.arg(after)
ORDER BY key
LIMIT sqlc.arg(max_rows);

-- name: ListProposalsForMessages :many
-- Every branch's proposal for the messages, by message and branch.
SELECT * FROM catalog_proposals
WHERE message_id = ANY (sqlc.arg(message_ids)::uuid[])
ORDER BY message_id, branch_id;

-- name: LockMessagesByIDs :many
-- In key order, the one lock order of every bulk writer.
SELECT * FROM catalog_messages
WHERE project_id = sqlc.arg(project_id) AND id = ANY (sqlc.arg(ids)::uuid[])
ORDER BY key
FOR UPDATE;

-- name: ListActiveKeysExcept :many
-- A project's live keys that a complete branch push didn't have.
SELECT key FROM catalog_messages
WHERE project_id = sqlc.arg(project_id) AND state = 'active' AND NOT (key = ANY (sqlc.arg(keys)::text[]))
ORDER BY key;

-- name: LockOrphanedProposedMessages :many
-- The tenant's proposed messages that no open branch proposes: the
-- sweep's candidates (it obsoletes those whose branches all closed
-- long enough ago). Locked in (project, key) order.
SELECT m.* FROM catalog_messages m
WHERE m.state = 'proposed'
  AND NOT EXISTS (SELECT FROM catalog_proposals p JOIN catalog_branches b ON b.id = p.branch_id
                  WHERE p.message_id = m.id AND p.kind = 'new_key' AND b.state = 'open')
ORDER BY m.project_id, m.key
FOR UPDATE OF m;

-- name: ListClosedBranches :many
-- The project's closed and merged branches with when they closed: what
-- Context's retention needs to delete a closed branch's builds after
-- the grace period (RFC 0004 §2.3).
SELECT name, closed_at FROM catalog_branches
WHERE project_id = sqlc.arg(project_id) AND closed_at IS NOT NULL
ORDER BY name;
