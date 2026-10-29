-- Back to 0026's constraint: the three fields and nothing else. It
-- refuses to apply while any project stores a document with rules,
-- environments or a history, which is the right way round — a
-- constraint that silently allowed what the code can no longer read
-- would lose a project's policy without saying so.
ALTER TABLE catalog_projects DROP CONSTRAINT catalog_projects_check_policy_check;

ALTER TABLE catalog_projects ADD CONSTRAINT catalog_projects_check_policy_check
    CHECK (NOT settings ? 'check_policy'
           OR (jsonb_typeof(settings -> 'check_policy') = 'object'
               AND jsonb_typeof(settings -> 'check_policy' -> 'require_complete') IN ('null', 'array')
               AND (NOT settings -> 'check_policy' ? 'fail_on'
                    OR settings -> 'check_policy' ->> 'fail_on' IN ('error', 'warning', 'never'))
               AND (NOT settings -> 'check_policy' ? 'missing_translations'
                    OR settings -> 'check_policy' ->> 'missing_translations' IN ('error', 'warning'))));

DROP FUNCTION IF EXISTS catalog_check_policy_version_valid(jsonb);
