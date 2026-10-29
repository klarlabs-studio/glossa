-- name: InsertSuggestion :exec
INSERT INTO intelligence_suggestions (
    id, tenant_id, job_id, project_id, message_id, message_key, namespace, locale, source_revision,
    message_mf2, model, findings, term_findings, provenance, origin, score, explanation, action, action_note,
    risk_tags, calls, usage, cost_micro_usd, status, version, created_at
) VALUES (
    sqlc.arg(id), app_current_tenant(), sqlc.arg(job_id), sqlc.arg(project_id), sqlc.arg(message_id),
    sqlc.arg(message_key), sqlc.arg(namespace), sqlc.arg(locale), sqlc.arg(source_revision),
    sqlc.arg(message_mf2), sqlc.arg(model), sqlc.arg(findings), sqlc.arg(term_findings), sqlc.arg(provenance),
    sqlc.arg(origin), sqlc.arg(score), sqlc.arg(explanation), sqlc.arg(action), sqlc.narg(action_note),
    sqlc.arg(risk_tags)::text[], sqlc.arg(calls), sqlc.arg(usage), sqlc.arg(cost_micro_usd), sqlc.arg(status), 1,
    sqlc.arg(created_at)
);

-- SupersedePending retires the pending suggestions of a message and
-- locale other than keep: only the newest waits for review.
-- name: SupersedePending :exec
UPDATE intelligence_suggestions
SET status = 'superseded', version = version + 1
WHERE message_id = sqlc.arg(message_id) AND locale = sqlc.arg(locale) AND status = 'pending' AND id <> sqlc.arg(keep);

-- name: GetSuggestion :one
SELECT * FROM intelligence_suggestions WHERE id = sqlc.arg(id);

-- name: LockSuggestion :one
SELECT * FROM intelligence_suggestions WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: DecideSuggestion :execrows
UPDATE intelligence_suggestions
SET status = sqlc.arg(status), translation_revision = sqlc.narg(translation_revision),
    decided_by = sqlc.narg(decided_by), decided_at = sqlc.narg(decided_at), decision = sqlc.narg(decision),
    action_note = sqlc.narg(action_note), version = version + 1
WHERE id = sqlc.arg(id) AND version = sqlc.arg(expected_version);

-- name: ListSuggestions :many
SELECT * FROM intelligence_suggestions
WHERE (sqlc.narg(project_id)::uuid IS NULL OR project_id = sqlc.narg(project_id)::uuid)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(locale)::text IS NULL OR locale = sqlc.narg(locale)::text)
  AND (sqlc.narg(message_id)::uuid IS NULL OR message_id = sqlc.narg(message_id)::uuid)
  AND (sqlc.narg(job_id)::uuid IS NULL OR job_id = sqlc.narg(job_id)::uuid)
  -- projects limits the rows to a project-scoped caller's projects
  -- (RFC 0006 §4.1), in the query so a page's size says nothing about
  -- the others.
  AND (sqlc.narg(projects)::uuid[] IS NULL OR project_id = ANY (sqlc.narg(projects)::uuid[]))
  AND (sqlc.narg(before_at)::timestamptz IS NULL OR (created_at, id) < (sqlc.narg(before_at)::timestamptz, sqlc.arg(before_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(max_rows)::int;

-- ReviewQueue lists a project's pending suggestions by risk: lowest
-- score first, then the most risk tags, then id (keyset).
-- name: ReviewQueue :many
SELECT * FROM intelligence_suggestions
WHERE project_id = sqlc.arg(project_id) AND status = 'pending'
  AND (cardinality(sqlc.arg(locales)::text[]) = 0 OR locale = ANY (sqlc.arg(locales)::text[]))
  AND (NOT sqlc.arg(has_after)::boolean
       OR (score, -cardinality(risk_tags), id) > (sqlc.arg(after_score)::float8, sqlc.arg(after_risk)::int, sqlc.arg(after_id)::uuid))
ORDER BY score, cardinality(risk_tags) DESC, id
LIMIT sqlc.arg(max_rows)::int;

-- DecisionStats summarizes people's decisions on a project's
-- suggestions per locale since a time: the acceptance rate and edit
-- distance metrics.
-- name: DecisionStats :many
SELECT locale,
       count(*) FILTER (WHERE status = 'accepted')::bigint AS accepted,
       count(*) FILTER (WHERE status = 'accepted' AND decision ? 'edit')::bigint AS edited,
       count(*) FILTER (WHERE status = 'rejected')::bigint AS rejected,
       coalesce(avg(coalesce((decision -> 'edit' ->> 'distance')::float8, 0)) FILTER (WHERE status = 'accepted'), 0)::float8 AS mean_edit_distance,
       coalesce(avg(coalesce((decision -> 'edit' ->> 'ratio')::float8, 0)) FILTER (WHERE status = 'accepted'), 0)::float8 AS mean_edit_ratio
FROM intelligence_suggestions
WHERE project_id = sqlc.arg(project_id) AND decided_at >= sqlc.arg(since) AND status IN ('accepted', 'rejected')
GROUP BY locale
ORDER BY locale;

-- name: SuggestionCounts :many
SELECT locale, status, count(*)::bigint AS n
FROM intelligence_suggestions
WHERE project_id = sqlc.arg(project_id) AND created_at >= sqlc.arg(since)
GROUP BY locale, status;

-- QueueAges is the review queue's depth and how long its items have
-- been waiting, per locale (RFC 0005 §8). The queue itself already
-- exists; its age is what a dashboard needs and no query answered.
--
-- An item has been waiting since it was written: there is no separate
-- enqueued_at, and a suggestion is reviewable the moment it lands. The
-- percentiles are percentile_cont, so they interpolate between the two
-- closest ranks exactly as the Go side does when it has to compute one
-- itself.
-- name: QueueAges :many
SELECT locale,
       count(*)::integer AS waiting,
       percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM sqlc.arg(now)::timestamptz - created_at))::float8 AS p50_seconds,
       percentile_cont(0.9) WITHIN GROUP (ORDER BY extract(epoch FROM sqlc.arg(now)::timestamptz - created_at))::float8 AS p90_seconds,
       max(extract(epoch FROM sqlc.arg(now)::timestamptz - created_at))::float8 AS oldest_seconds
FROM intelligence_suggestions
WHERE project_id = sqlc.arg(project_id) AND status = 'pending'
  AND (cardinality(sqlc.arg(locales)::text[]) = 0 OR locale = ANY (sqlc.arg(locales)::text[]))
GROUP BY locale
ORDER BY locale;

-- ── disclosures ────────────────────────────────────────────────────

-- name: InsertDisclosure :exec
INSERT INTO intelligence_disclosures (
    id, tenant_id, job_id, project_id, message_id, locale, task, provider, model, system_sha256, sent, occurred_at
) VALUES (
    sqlc.arg(id), app_current_tenant(), sqlc.arg(job_id), sqlc.arg(project_id), sqlc.arg(message_id), sqlc.arg(locale),
    sqlc.arg(task), sqlc.arg(provider), sqlc.arg(model), sqlc.arg(system_sha256), sqlc.arg(sent), sqlc.arg(occurred_at)
)
ON CONFLICT (id) DO NOTHING;

-- name: ListDisclosures :many
SELECT * FROM intelligence_disclosures
WHERE (sqlc.narg(job_id)::uuid IS NULL OR job_id = sqlc.narg(job_id)::uuid)
  AND (sqlc.narg(message_id)::uuid IS NULL OR message_id = sqlc.narg(message_id)::uuid)
  AND (sqlc.narg(project_id)::uuid IS NULL OR project_id = sqlc.narg(project_id)::uuid)
  AND (sqlc.narg(provider)::text IS NULL OR provider = sqlc.narg(provider)::text)
  -- projects limits the rows to a project-scoped caller's projects
  -- (RFC 0006 §4.1), in the query so a page's size says nothing about
  -- the others.
  AND (sqlc.narg(projects)::uuid[] IS NULL OR project_id = ANY (sqlc.narg(projects)::uuid[]))
  AND (sqlc.narg(before_at)::timestamptz IS NULL OR (occurred_at, id) < (sqlc.narg(before_at)::timestamptz, sqlc.arg(before_id)::uuid))
ORDER BY occurred_at DESC, id DESC
LIMIT sqlc.arg(max_rows)::int;
