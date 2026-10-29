-- Groups and vendors (RFC 0006 §3.3, §4.3). Tenant scope (db.TenantTx):
-- RLS limits every statement to the current tenant.

-- name: InsertVendor :execrows
INSERT INTO identity_vendors (id, tenant_id, name, contact, locales, version, created_by, created_at, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(name), sqlc.arg(contact), sqlc.arg(locales),
        sqlc.arg(version), sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(created_at))
ON CONFLICT (id) DO NOTHING;

-- name: GetVendor :one
SELECT * FROM identity_vendors WHERE id = sqlc.arg(id);

-- name: LockVendor :one
SELECT * FROM identity_vendors WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: ListVendors :many
SELECT * FROM identity_vendors
WHERE id > sqlc.arg(after)
ORDER BY id
LIMIT sqlc.arg(max_rows);

-- name: UpdateVendor :execrows
UPDATE identity_vendors
SET name = sqlc.arg(name), contact = sqlc.arg(contact), locales = sqlc.arg(locales),
    version = sqlc.arg(version), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(version) - 1;

-- name: DeleteVendor :execrows
DELETE FROM identity_vendors WHERE id = sqlc.arg(id);

-- name: InsertGroup :execrows
INSERT INTO identity_groups (id, tenant_id, name, version, created_by, created_at, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(name), sqlc.arg(version), sqlc.arg(created_by),
        sqlc.arg(created_at), sqlc.arg(created_at))
ON CONFLICT (id) DO NOTHING;

-- name: GetGroup :one
SELECT * FROM identity_groups WHERE id = sqlc.arg(id);

-- name: LockGroup :one
SELECT * FROM identity_groups WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: ListGroups :many
SELECT * FROM identity_groups
WHERE id > sqlc.arg(after)
ORDER BY id
LIMIT sqlc.arg(max_rows);

-- name: GroupMemberIDs :many
SELECT member_id FROM identity_group_members
WHERE group_id = sqlc.arg(group_id)
ORDER BY member_id;

-- name: UpdateGroup :execrows
UPDATE identity_groups
SET name = sqlc.arg(name), version = sqlc.arg(version), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(version) - 1;

-- name: InsertGroupMember :exec
INSERT INTO identity_group_members (tenant_id, group_id, member_id, added_by, added_at)
VALUES (app_current_tenant(), sqlc.arg(group_id), sqlc.arg(member_id), sqlc.arg(added_by), sqlc.arg(added_at));

-- name: DeleteGroupMember :execrows
DELETE FROM identity_group_members WHERE group_id = sqlc.arg(group_id) AND member_id = sqlc.arg(member_id);

-- name: DeleteGroup :execrows
DELETE FROM identity_groups WHERE id = sqlc.arg(id);
