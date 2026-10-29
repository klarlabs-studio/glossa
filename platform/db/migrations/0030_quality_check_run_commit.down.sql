DROP INDEX IF EXISTS quality_check_runs_commit;
ALTER TABLE quality_check_runs DROP COLUMN IF EXISTS commit_sha;
