-- 0029 — The check policy becomes a document (RFC 0005 §4.1).
--
-- 0026 stored three fields in catalog_projects.settings.check_policy and
-- bounded them with a CHECK constraint: require_complete, fail_on and
-- missing_translations. M4 keeps all three — they are still the base
-- every project starts from — and adds what different locales,
-- namespaces and environments deserve different answers about:
--
--   "check_policy": {
--     "schema": "glossa.check-policy/v1",     absent: this schema
--     "version": 7,                           absent: 0, no versions yet
--     "require_complete": null | ["de","fr"], null: every locale
--     "fail_on": "error" | "warning" | "never",
--     "missing_translations": "error" | "warning",
--     "environments": {
--       "production": {"require_complete": null | [...],
--                      "require_review": "approved"}
--     },
--     "rules": [
--       {"layer": "terminology", "code": "term_forbidden",
--        "locale": "de", "namespace": "legal", "environment": "production",
--        "severity": "error" | "warning" | "off",
--        "mode": "enforce" | "warn"}
--     ],
--     "effective_from": "2026-09-20T12:00:00Z",
--     "grace_until": "2026-10-04T12:00:00Z",
--     "previous": { … the same shape, without a "previous" of its own … }
--   }
--
-- **Nothing is rewritten.** Every policy written under 0026 is already a
-- valid document: a version-0 one with no rules, no environments and no
-- history, which the evaluator decides exactly as the three fields
-- always did. This migration widens what may be stored and touches no
-- row, so no project's verdict can move across it. That is also why the
-- down migration can simply restore 0026's constraint: it fails, loudly
-- and before anything is lost, only if a project has meanwhile saved a
-- document the old shape cannot hold.
--
-- What storage bounds and what the domain decides is split as 0026 split
-- it. The shape and the vocabularies are checked here, because storage
-- bounds what is stored. That an advisory layer may never be raised to
-- error (§14 decision 10), that a required locale is one the project
-- has, and that a previous version really precedes this one are the
-- domain's, because they are about meaning.

-- One version of the document, without its history. The constraint
-- applies it twice — to the document and to the one version it keeps
-- behind it — rather than recursing, which is what keeps a history one
-- version deep in storage as well as in the domain.
CREATE FUNCTION catalog_check_policy_version_valid(p jsonb) RETURNS boolean
    LANGUAGE sql IMMUTABLE AS $$
    SELECT jsonb_typeof(p) = 'object'
       AND (NOT p ? 'schema' OR p ->> 'schema' = 'glossa.check-policy/v1')
       AND (NOT p ? 'version'
            OR (jsonb_typeof(p -> 'version') = 'number' AND (p ->> 'version')::numeric >= 0))
       AND jsonb_typeof(p -> 'require_complete') IN ('null', 'array')
       AND (NOT p ? 'fail_on' OR p ->> 'fail_on' IN ('error', 'warning', 'never'))
       AND (NOT p ? 'missing_translations' OR p ->> 'missing_translations' IN ('error', 'warning'))
       AND (NOT p ? 'environments'
            OR (jsonb_typeof(p -> 'environments') = 'object'
                AND NOT EXISTS (
                    SELECT 1 FROM jsonb_each(p -> 'environments') AS e(name, block)
                    WHERE jsonb_typeof(block) <> 'object'
                       OR (block ? 'require_complete'
                           AND jsonb_typeof(block -> 'require_complete') NOT IN ('null', 'array'))
                       OR (block ? 'require_review' AND block ->> 'require_review' <> 'approved'))))
       AND (NOT p ? 'rules'
            OR (jsonb_typeof(p -> 'rules') = 'array'
                AND NOT EXISTS (
                    SELECT 1 FROM jsonb_array_elements(p -> 'rules') AS r(rule)
                    WHERE jsonb_typeof(rule) <> 'object'
                       OR rule ->> 'severity' IS NULL
                       OR rule ->> 'severity' NOT IN ('error', 'warning', 'off')
                       OR (rule ? 'mode' AND rule ->> 'mode' NOT IN ('enforce', 'warn'))
                       OR (rule ? 'layer' AND rule ->> 'layer' NOT IN (
                             'structure', 'parity', 'completeness', 'terminology', 'style',
                             'length', 'locale', 'source', 'visual', 'linguistic')))))
       AND (NOT p ? 'effective_from' OR jsonb_typeof(p -> 'effective_from') = 'string')
       AND (NOT p ? 'grace_until' OR jsonb_typeof(p -> 'grace_until') = 'string');
$$;

ALTER TABLE catalog_projects DROP CONSTRAINT catalog_projects_check_policy_check;

ALTER TABLE catalog_projects ADD CONSTRAINT catalog_projects_check_policy_check
    CHECK (NOT settings ? 'check_policy'
           OR (catalog_check_policy_version_valid(settings -> 'check_policy')
               AND (NOT settings -> 'check_policy' ? 'previous'
                    OR (catalog_check_policy_version_valid(settings -> 'check_policy' -> 'previous')
                        AND NOT settings -> 'check_policy' -> 'previous' ? 'previous'))));
