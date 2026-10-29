-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant, so none of these take a tenant_id to filter on.

-- name: InsertMember :execrows
-- ON CONFLICT (id) DO NOTHING makes a retried idempotent create a no-op;
-- the caller then loads the existing row.
INSERT INTO identity_members (id, tenant_id, person_id, email, roles, locales, projects, vendor_id,
                              visibility, status, version, created_by, created_at, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.narg(person_id), sqlc.arg(email), sqlc.arg(roles),
        sqlc.arg(locales), sqlc.arg(projects)::uuid[], sqlc.narg(vendor_id), sqlc.arg(visibility),
        sqlc.arg(status), sqlc.arg(version), sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(created_at))
ON CONFLICT (id) DO NOTHING;

-- name: GetMember :one
SELECT m.*, coalesce(p.display_name, '')::text AS display_name
FROM identity_members m
LEFT JOIN identity_people p ON p.id = m.person_id
WHERE m.id = sqlc.arg(id);

-- name: LockMember :one
SELECT * FROM identity_members WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: LockActiveOwners :many
-- Locks the owner rows, so two concurrent demotions can't both see a
-- second owner and leave none.
SELECT id FROM identity_members
WHERE status = 'active' AND 'owner' = ANY (roles)
ORDER BY id
FOR UPDATE;

-- name: ListMembers :many
SELECT m.*, coalesce(p.display_name, '')::text AS display_name
FROM identity_members m
LEFT JOIN identity_people p ON p.id = m.person_id
WHERE m.id > sqlc.arg(after)
ORDER BY m.id
LIMIT sqlc.arg(max_rows);

-- name: UpdateMemberAccess :execrows
UPDATE identity_members
SET roles = sqlc.arg(roles), locales = sqlc.arg(locales), version = sqlc.arg(version),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(version) - 1;

-- name: UpdateMemberRestriction :execrows
-- Project scope, vendor and visibility (RFC 0006 §3.3, §4.1).
UPDATE identity_members
SET projects = sqlc.arg(projects)::uuid[], vendor_id = sqlc.narg(vendor_id), visibility = sqlc.arg(visibility),
    version = sqlc.arg(version), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(version) - 1;

-- name: CountVendorMembers :one
SELECT count(*) FROM identity_members WHERE vendor_id = sqlc.arg(vendor_id);

-- name: ActivateMember :execrows
UPDATE identity_members
SET person_id = sqlc.arg(person_id), status = 'active', version = version + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND status = 'invited';

-- name: DeleteMember :execrows
DELETE FROM identity_members WHERE id = sqlc.arg(id);

-- System scope (db.SystemTx), before a tenant is chosen.

-- name: SystemGetActiveMembership :one
SELECT id, roles, locales, projects, visibility FROM identity_members
WHERE person_id = sqlc.arg(person_id) AND tenant_id = sqlc.arg(tenant_id) AND status = 'active';

-- name: SystemListMembershipsOfPerson :many
SELECT m.id AS member_id, m.roles, m.locales, t.id AS tenant_id, t.kind, t.slug, t.name, t.created_at
FROM identity_members m
JOIN tenants t ON t.id = m.tenant_id
WHERE m.person_id = sqlc.arg(person_id) AND m.status = 'active' AND t.id > sqlc.arg(after)
ORDER BY t.id
LIMIT sqlc.arg(max_rows);

-- name: SystemListOpenInvitations :many
SELECT id, tenant_id FROM identity_members
WHERE email = sqlc.arg(email) AND status = 'invited'
ORDER BY id;

-- name: SystemGetTenant :one
SELECT * FROM tenants WHERE id = sqlc.arg(id);
