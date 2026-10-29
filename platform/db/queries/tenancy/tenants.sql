-- Queries run inside a tenant-scoped transaction (db.TenantTx), so RLS
-- limits them to the current tenant; app_current_tenant() makes that
-- explicit rather than trusting a caller-supplied id.

-- name: InsertTenant :one
INSERT INTO tenants (id, kind, slug, name)
VALUES (sqlc.arg(id), sqlc.arg(kind), sqlc.arg(slug), sqlc.arg(name))
RETURNING created_at;

-- name: GetCurrentTenant :one
SELECT id, kind, slug, name, created_at
FROM tenants
WHERE id = app_current_tenant();
