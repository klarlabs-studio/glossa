-- 0026 — The project's check policy (RFC 0004 §6.4).
--
-- `glossa check` and the Glossa pull-request check decide the same
-- question and share kernel/checkpolicy to decide it the same way. Until
-- now only the command could be told what to decide (`glossa.yaml`'s
-- check.require_complete and check.fail_on); the pull request always got
-- the command's defaults, because there was nowhere to say otherwise.
--
-- The policy is a project setting, like default_branch (0014): one more
-- member of catalog_projects.settings, absent for every project written
-- before this and read as the documented default — every locale must be
-- complete, an untranslated key in one is an error, and errors fail.
--
-- The shape, checked here because storage bounds what is stored and the
-- domain decides what it means:
--
--   "check_policy": {
--     "require_complete": null | ["de", "fr"],   null: every locale
--     "fail_on": "error" | "warning" | "never",  absent: "error"
--     "missing_translations": "error" | "warning"
--   }
--
-- null and [] are opposites here: null requires every locale, [] none.
-- The locales themselves belong to Localization, so a required locale is
-- checked against the project's on write, not here.

ALTER TABLE catalog_projects ADD CONSTRAINT catalog_projects_check_policy_check
    CHECK (NOT settings ? 'check_policy'
           OR (jsonb_typeof(settings -> 'check_policy') = 'object'
               AND jsonb_typeof(settings -> 'check_policy' -> 'require_complete') IN ('null', 'array')
               AND (NOT settings -> 'check_policy' ? 'fail_on'
                    OR settings -> 'check_policy' ->> 'fail_on' IN ('error', 'warning', 'never'))
               AND (NOT settings -> 'check_policy' ? 'missing_translations'
                    OR settings -> 'check_policy' ->> 'missing_translations' IN ('error', 'warning'))));
