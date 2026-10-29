-- ── providers ──────────────────────────────────────────────────────

-- name: InsertProvider :execrows
INSERT INTO intelligence_providers (
    id, tenant_id, name, kind, base_url, models, enabled, api_key_sealed,
    version, created_by, created_at, updated_by, updated_at
) VALUES (
    sqlc.arg(id), app_current_tenant(), sqlc.arg(name), sqlc.arg(kind), sqlc.arg(base_url),
    sqlc.arg(models)::text[], sqlc.arg(enabled), sqlc.narg(api_key_sealed), 1,
    sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(created_by), sqlc.arg(created_at)
)
ON CONFLICT (id) DO NOTHING;

-- name: GetProvider :one
SELECT * FROM intelligence_providers WHERE id = sqlc.arg(id);

-- name: LockProvider :one
SELECT * FROM intelligence_providers WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: ListProviders :many
SELECT * FROM intelligence_providers
WHERE sqlc.arg(after)::text = '' OR name > sqlc.arg(after)::text
ORDER BY name
LIMIT sqlc.arg(max_rows)::int;

-- name: AllProviders :many
SELECT * FROM intelligence_providers ORDER BY name;

-- name: UpdateProvider :execrows
UPDATE intelligence_providers
SET name = sqlc.arg(name), kind = sqlc.arg(kind), base_url = sqlc.arg(base_url),
    models = sqlc.arg(models)::text[], enabled = sqlc.arg(enabled),
    api_key_sealed = sqlc.narg(api_key_sealed), version = version + 1,
    updated_by = sqlc.arg(updated_by), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(expected_version);

-- name: DeleteProvider :execrows
DELETE FROM intelligence_providers WHERE id = sqlc.arg(id);

-- ── settings ───────────────────────────────────────────────────────

-- name: GetSettings :one
SELECT * FROM intelligence_settings WHERE tenant_id = app_current_tenant();

-- name: LockSettings :one
SELECT * FROM intelligence_settings WHERE tenant_id = app_current_tenant() FOR UPDATE;

-- name: InsertSettings :execrows
INSERT INTO intelligence_settings (
    tenant_id, provider_consent, consent_changed_by, consent_changed_at, max_concurrent_jobs,
    monthly_budget_micro_usd, prices, version, updated_by, updated_at
) VALUES (
    app_current_tenant(), sqlc.arg(provider_consent), sqlc.narg(consent_changed_by), sqlc.narg(consent_changed_at),
    sqlc.arg(max_concurrent_jobs), sqlc.arg(monthly_budget_micro_usd), sqlc.arg(prices), 1,
    sqlc.arg(updated_by), sqlc.arg(updated_at)
)
ON CONFLICT (tenant_id) DO NOTHING;

-- name: UpdateSettings :execrows
UPDATE intelligence_settings
SET provider_consent = sqlc.arg(provider_consent), consent_changed_by = sqlc.narg(consent_changed_by),
    consent_changed_at = sqlc.narg(consent_changed_at), max_concurrent_jobs = sqlc.arg(max_concurrent_jobs),
    monthly_budget_micro_usd = sqlc.arg(monthly_budget_micro_usd), prices = sqlc.arg(prices),
    version = version + 1, updated_by = sqlc.arg(updated_by), updated_at = sqlc.arg(updated_at)
WHERE tenant_id = app_current_tenant() AND version = sqlc.arg(expected_version);

-- name: GetProjectSettings :one
SELECT * FROM intelligence_project_settings WHERE project_id = sqlc.arg(project_id);

-- name: InsertProjectSettings :execrows
INSERT INTO intelligence_project_settings (
    project_id, tenant_id, namespace_tags, auto_translate_locales, review_policy, version, updated_by, updated_at
) VALUES (
    sqlc.arg(project_id), app_current_tenant(), sqlc.arg(namespace_tags), sqlc.arg(auto_translate_locales)::text[],
    sqlc.arg(review_policy), 1, sqlc.arg(updated_by), sqlc.arg(updated_at)
)
ON CONFLICT (project_id) DO NOTHING;

-- name: UpdateProjectSettings :execrows
UPDATE intelligence_project_settings
SET namespace_tags = sqlc.arg(namespace_tags), auto_translate_locales = sqlc.arg(auto_translate_locales)::text[],
    review_policy = sqlc.arg(review_policy), version = version + 1,
    updated_by = sqlc.arg(updated_by), updated_at = sqlc.arg(updated_at)
WHERE project_id = sqlc.arg(project_id) AND version = sqlc.arg(expected_version);

-- ── routing ────────────────────────────────────────────────────────

-- name: GetRoutingPolicy :one
SELECT * FROM intelligence_routing_policies
WHERE project_id IS NOT DISTINCT FROM sqlc.narg(project_id)::uuid;

-- name: ListRoutingPolicies :many
SELECT * FROM intelligence_routing_policies ORDER BY project_id NULLS FIRST;

-- name: InsertRoutingPolicy :execrows
INSERT INTO intelligence_routing_policies (id, tenant_id, project_id, policy, version, updated_by, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.narg(project_id), sqlc.arg(policy), 1, sqlc.arg(updated_by), sqlc.arg(updated_at))
ON CONFLICT (tenant_id, project_id) DO NOTHING;

-- name: UpdateRoutingPolicy :execrows
UPDATE intelligence_routing_policies
SET policy = sqlc.arg(policy), version = version + 1, updated_by = sqlc.arg(updated_by), updated_at = sqlc.arg(updated_at)
WHERE project_id IS NOT DISTINCT FROM sqlc.narg(project_id)::uuid AND version = sqlc.arg(expected_version);

-- name: DeleteRoutingPolicy :execrows
DELETE FROM intelligence_routing_policies WHERE project_id = sqlc.arg(project_id);

-- ── spend ──────────────────────────────────────────────────────────

-- name: InsertSpend :exec
INSERT INTO intelligence_spend (
    id, tenant_id, job_id, project_id, task, provider, model, input_tokens, output_tokens,
    cache_read_tokens, cache_write_tokens, cost_micro_usd, priced, occurred_at
) VALUES (
    sqlc.arg(id), app_current_tenant(), sqlc.narg(job_id), sqlc.narg(project_id), sqlc.arg(task),
    sqlc.arg(provider), sqlc.arg(model), sqlc.arg(input_tokens), sqlc.arg(output_tokens),
    sqlc.arg(cache_read_tokens), sqlc.arg(cache_write_tokens), sqlc.arg(cost_micro_usd), sqlc.arg(priced),
    sqlc.arg(occurred_at)
);

-- name: SpendSince :one
SELECT coalesce(sum(cost_micro_usd), 0)::bigint AS total, count(*)::bigint AS calls
FROM intelligence_spend
WHERE tenant_id = app_current_tenant() AND occurred_at >= sqlc.arg(since);

-- name: SpendByProviderSince :many
SELECT provider, model, coalesce(sum(cost_micro_usd), 0)::bigint AS cost, count(*)::bigint AS calls,
       coalesce(sum(input_tokens), 0)::bigint AS input_tokens, coalesce(sum(output_tokens), 0)::bigint AS output_tokens
FROM intelligence_spend
WHERE tenant_id = app_current_tenant() AND occurred_at >= sqlc.arg(since)
GROUP BY provider, model
ORDER BY provider, model;

-- name: ListSpend :many
SELECT * FROM intelligence_spend
WHERE tenant_id = app_current_tenant() AND occurred_at >= sqlc.arg(since)
  AND (sqlc.narg(before_at)::timestamptz IS NULL OR (occurred_at, id) < (sqlc.narg(before_at)::timestamptz, sqlc.arg(before_id)::uuid))
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(max_rows)::int;
