-- 0045 down — drop the audit trail.

DROP TABLE IF EXISTS audit_entries;
DROP FUNCTION IF EXISTS audit_entries_link();
