-- 0054 down — device sessions end with the column that tells them
-- apart: a down migration does not keep a credential it no longer knows.
DELETE FROM identity_sessions WHERE kind = 'device';
ALTER TABLE identity_sessions DROP COLUMN kind;
DROP TABLE IF EXISTS identity_device_authorizations;
