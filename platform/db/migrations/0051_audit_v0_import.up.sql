-- 0051 — Audit: v0.3's history, imported (RFC 0006 §7.2, amended in
-- wave 4).
--
-- A third source, 'import': one entry per row of v0.3's audit_log,
-- written once by `glossa import --from v0 --v0-db … --history` through
-- the audit-imports route. Its actor is a v0.3 actor — v0:<user uuid>,
-- v0:ai[:<label>], v0:system[:<label>] or v0:unknown — and only an
-- imported entry may name one, so a v0.3 actor can never be passed off
-- as having acted on this platform, nor a platform actor as having
-- acted in v0.3. Its summary holds identifiers and SHA-256 digests of
-- the text before and after, never the text (§6.1).
--
-- Idempotency needs no new index: an imported entry's event_id is
-- derived from the v0.3 row id (UUID v5, audit/domain.V0EventID), and
-- (tenant_id, event_id) is already unique, so importing a tenant twice,
-- or once per project, records each row once.
--
-- Nothing else changes: the table stays INSERT and SELECT only for
-- glossa_app under forced RLS, the link trigger still refuses a gap or a
-- fork, and the canonical form is unchanged ("import" is a new value of
-- its "source" member, so every entry chained before keeps its hash).
-- An imported entry is appended at the end of the chain like any other:
-- the chain orders by append, not by occurred_at.
--
-- 0045 wrote both checks on their columns, which Postgres named after
-- the column.
ALTER TABLE audit_entries DROP CONSTRAINT audit_entries_source_check;
ALTER TABLE audit_entries DROP CONSTRAINT audit_entries_actor_check;
ALTER TABLE audit_entries ADD CONSTRAINT audit_entries_source_check
    CHECK (source IN ('outbox', 'direct', 'import'));
ALTER TABLE audit_entries ADD CONSTRAINT audit_entries_actor_check CHECK (
    CASE WHEN source = 'import' THEN
        char_length(actor) <= 110
        AND actor ~ '^v0:(unknown|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|(ai|system)(:[^[:cntrl:]]{1,100})?)$'
    ELSE
        actor = 'unknown'
        OR actor ~ '^(person|token|system):[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    END);
