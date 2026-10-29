-- 0051 down: v0.3's imported history goes with the source that held it.
-- Deleting entries rewrites a chain, which glossa_app can never do; this
-- runs as the migration owner, and only an imported tail can go without
-- leaving a gap. A chain in which live entries follow imported ones
-- cannot lose them without breaking, so the rollback refuses instead.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM audit_entries i
        WHERE i.source = 'import'
          AND EXISTS (SELECT 1 FROM audit_entries l
                      WHERE l.tenant_id = i.tenant_id AND l.sequence > i.sequence AND l.source <> 'import')
    ) THEN
        RAISE EXCEPTION '0051 down: imported v0.3 history is followed by live entries; removing it would break the chain';
    END IF;
END
$$;
DELETE FROM audit_entries WHERE source = 'import';
ALTER TABLE audit_entries DROP CONSTRAINT audit_entries_actor_check;
ALTER TABLE audit_entries DROP CONSTRAINT audit_entries_source_check;
ALTER TABLE audit_entries ADD CONSTRAINT audit_entries_source_check CHECK (source IN ('outbox', 'direct'));
ALTER TABLE audit_entries ADD CONSTRAINT audit_entries_actor_check CHECK (
    actor = 'unknown'
    OR actor ~ '^(person|token|system):[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');
