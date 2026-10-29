-- Tenant scope (db.TenantTx): RLS limits every statement to the current
-- tenant.

-- name: InsertConcept :execrows
-- ON CONFLICT: an Idempotency-Key derives the ID, so a retried create
-- inserts nothing and the caller replays the first.
INSERT INTO knowledge_concepts (id, tenant_id, project_id, definition, domain, note, product_ref, version,
                                created_by, created_at, updated_by, updated_at)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.narg(project_id), sqlc.arg(definition), sqlc.arg(domain),
        sqlc.arg(note), sqlc.arg(product_ref), sqlc.arg(version), sqlc.arg(created_by), sqlc.arg(created_at),
        sqlc.arg(updated_by), sqlc.arg(updated_at))
ON CONFLICT (id) DO NOTHING;

-- name: UpdateConcept :execrows
UPDATE knowledge_concepts
SET definition = sqlc.arg(definition), domain = sqlc.arg(domain), note = sqlc.arg(note),
    product_ref = sqlc.arg(product_ref), version = sqlc.arg(version), updated_by = sqlc.arg(updated_by),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(expected_version);

-- name: DeleteConcept :exec
DELETE FROM knowledge_concepts WHERE id = sqlc.arg(id);

-- name: GetConcept :one
SELECT * FROM knowledge_concepts WHERE id = sqlc.arg(id);

-- name: LockConcept :one
SELECT * FROM knowledge_concepts WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: DeleteTermsOfConcept :exec
DELETE FROM knowledge_terms WHERE concept_id = sqlc.arg(concept_id);

-- name: InsertTerm :exec
INSERT INTO knowledge_terms (id, tenant_id, concept_id, position, locale, text, status, part_of_speech,
                             case_sensitive, note)
VALUES (sqlc.arg(id), app_current_tenant(), sqlc.arg(concept_id), sqlc.arg(position), sqlc.arg(locale),
        sqlc.arg(text), sqlc.arg(status), sqlc.arg(part_of_speech), sqlc.arg(case_sensitive), sqlc.arg(note));

-- name: TermsOfConcepts :many
SELECT * FROM knowledge_terms WHERE concept_id = ANY (sqlc.arg(concept_ids)::uuid[])
ORDER BY concept_id, position;

-- name: ListConcepts :many
-- Concepts in id order after the cursor. project: that project's and
-- the tenant-wide ones (the termbase that applies to it). pattern
-- (ILIKE) searches term texts (in locale, when given) and definitions.
SELECT c.* FROM knowledge_concepts c
WHERE c.id > sqlc.arg(after)
  AND (sqlc.narg(project_id)::uuid IS NULL OR c.project_id IS NULL OR c.project_id = sqlc.narg(project_id))
  -- projects limits project-owned rows to a project-scoped caller's
  -- projects (RFC 0006 §4.1); tenant-wide rows stay. Filtered here so a
  -- page's size says nothing about the others.
  AND (sqlc.narg(projects)::uuid[] IS NULL OR c.project_id IS NULL OR c.project_id = ANY (sqlc.narg(projects)::uuid[]))
  AND (sqlc.narg(domain)::text IS NULL OR c.domain = sqlc.narg(domain))
  AND (sqlc.narg(locale)::text IS NULL
       OR EXISTS (SELECT FROM knowledge_terms t WHERE t.concept_id = c.id AND t.locale = sqlc.narg(locale)))
  AND (sqlc.narg(pattern)::text IS NULL
       OR c.definition ILIKE sqlc.narg(pattern)
       OR EXISTS (SELECT FROM knowledge_terms t
                  WHERE t.concept_id = c.id AND t.text ILIKE sqlc.narg(pattern)
                    AND (sqlc.narg(locale)::text IS NULL OR t.locale = sqlc.narg(locale))))
ORDER BY c.id
LIMIT sqlc.arg(max_rows);

-- name: ConceptsWithTermsIn :many
-- The concepts in scope with a term in any of the locales: what
-- recognition and terminology QA need for a locale pair.
SELECT c.* FROM knowledge_concepts c
WHERE (c.project_id IS NULL OR c.project_id = sqlc.narg(project_id))
  AND EXISTS (SELECT FROM knowledge_terms t WHERE t.concept_id = c.id AND t.locale = ANY (sqlc.arg(locales)::text[]))
ORDER BY c.id;

-- name: InsertConceptRevision :exec
INSERT INTO knowledge_concept_revisions (tenant_id, concept_id, project_id, version, action, snapshot, author,
                                         created_at)
VALUES (app_current_tenant(), sqlc.arg(concept_id), sqlc.narg(project_id), sqlc.arg(version), sqlc.arg(action),
        sqlc.arg(snapshot), sqlc.arg(author), sqlc.arg(created_at));

-- name: ListConceptRevisions :many
SELECT * FROM knowledge_concept_revisions
WHERE concept_id = sqlc.arg(concept_id) AND version < sqlc.arg(before)
ORDER BY version DESC
LIMIT sqlc.arg(max_rows);

-- name: DeleteProjectConcepts :exec
DELETE FROM knowledge_concepts WHERE project_id = sqlc.arg(project_id);

-- name: DeleteProjectConceptRevisions :exec
DELETE FROM knowledge_concept_revisions WHERE project_id = sqlc.arg(project_id);
