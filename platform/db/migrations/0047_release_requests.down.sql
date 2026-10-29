-- 0047 down — no release requests, and no environment requires approval.
DROP TABLE IF EXISTS release_requests;
ALTER TABLE release_environments DROP CONSTRAINT IF EXISTS release_environments_branch_no_approval;
ALTER TABLE release_environments DROP COLUMN IF EXISTS approval;
