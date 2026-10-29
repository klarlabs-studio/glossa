-- The stored policies stay in settings; nothing reads them any more.
ALTER TABLE catalog_projects DROP CONSTRAINT catalog_projects_check_policy_check;
