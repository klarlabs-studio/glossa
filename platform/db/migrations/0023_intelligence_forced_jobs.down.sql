-- 0023 down — drop the forced flag.

ALTER TABLE intelligence_jobs DROP COLUMN IF EXISTS forced;
