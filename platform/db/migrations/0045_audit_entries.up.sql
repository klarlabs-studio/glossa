-- 0045 — Audit: the tenant's tamper-evident trail (RFC 0006 §6.1, §9.5).
--
-- audit_entries is a projection: one entry per outbox event (written by
-- the outbox subscriber, or by the backfill for events recorded before
-- this table existed), plus the security-relevant acts that never reach
-- the outbox — sign-ins and failed sign-ins, MCP tool calls — written
-- directly through the Audit context's Recorder.
--
-- What an entry holds, and what it never does:
--
--   sequence          the entry's place in its tenant's chain: 1, 2, 3…
--                     with no gaps (the trigger below refuses one).
--   event_id          the act recorded: the outbox event's id, or a
--                     direct write's own id. Unique per tenant, so a
--                     redelivered event or a re-run backfill records
--                     nothing twice.
--   action            the event type ("localization.translation.revised")
--                     or a direct action ("identity.person.signed_in").
--   actor             "person:<uuid>", "token:<uuid>", "system:<uuid>",
--                     or "unknown" where it was never recorded (events
--                     from before 0042, failed sign-ins).
--   aggregate_*,      the target, and the project and locale where the
--   project_id,       act names them.
--   locale
--   summary           the act without its content: identifiers and
--                     selectors verbatim, everything else as its shape
--                     ("string(len=27)"), as mcp_tool_calls records
--                     arguments. No message text, no translation text,
--                     no email, no secret ever enters it; the text is in
--                     the revision log the entry points at by id.
--   request_id,       the request that caused it, where known.
--   trace_id
--   prev_hash, hash   the chain: hash = sha256(prev_hash ‖ JCS(entry)),
--                     prev_hash of the first entry 32 zero bytes. The
--                     canonical form is audit/domain.Entry.Canonical.
--
-- Append-only for the application role: glossa_app may INSERT and
-- SELECT and nothing else, as with mcp_tool_calls. The chain proves an
-- export was not edited; it is no protection against a database
-- superuser, who could rewrite every row and every hash (§9.5).
-- Entries live as long as their tenant (§6.2): deleting the tenant
-- deletes its chain.

CREATE TABLE audit_entries (
    tenant_id      uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    sequence       bigint      NOT NULL CHECK (sequence >= 1),
    event_id       uuid        NOT NULL,
    source         text        NOT NULL CHECK (source IN ('outbox', 'direct')),
    action         text        NOT NULL CHECK (
                                   char_length(action) <= 200
                                   AND action ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
    actor          text        NOT NULL CHECK (
                                   actor = 'unknown'
                                   OR actor ~ '^(person|token|system):[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
    occurred_at    timestamptz NOT NULL,
    aggregate_type text        NOT NULL CHECK (char_length(aggregate_type) BETWEEN 1 AND 200),
    aggregate_id   text        NOT NULL CHECK (char_length(aggregate_id) BETWEEN 1 AND 200),
    project_id     uuid,
    locale         text        CHECK (locale ~ '^[A-Za-z0-9-]{1,35}$'),
    summary        jsonb       NOT NULL DEFAULT '{}'::jsonb
                               CHECK (jsonb_typeof(summary) = 'object' AND octet_length(summary::text) <= 16384),
    request_id     text        CHECK (request_id ~ '^[A-Za-z0-9._-]{1,128}$'),
    trace_id       text        CHECK (trace_id ~ '^[0-9a-f]{32}$'),
    prev_hash      bytea       NOT NULL CHECK (octet_length(prev_hash) = 32),
    hash           bytea       NOT NULL CHECK (octet_length(hash) = 32),
    recorded_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, sequence),
    UNIQUE (tenant_id, event_id)
);

-- The questions wave 5's API filters by: a time range, an actor, an
-- action, a project.
CREATE INDEX audit_entries_occurred ON audit_entries (tenant_id, occurred_at);
CREATE INDEX audit_entries_actor ON audit_entries (tenant_id, actor, sequence);
CREATE INDEX audit_entries_project ON audit_entries (tenant_id, project_id, sequence) WHERE project_id IS NOT NULL;

-- The chain's shape, held by the database as well as by the code: an
-- entry must be its tenant's next sequence and link to the hash of the
-- entry before it (32 zero bytes for the first). The application
-- serializes appends per tenant (a transaction-scoped advisory lock), so
-- this never fires in normal operation; it is what makes a gap or a
-- fork impossible rather than merely unlikely. It cannot check the hash
-- itself (that needs the canonical encoding) — the verifier does.
CREATE FUNCTION audit_entries_link() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    last_sequence bigint;
    last_hash     bytea;
BEGIN
    SELECT e.sequence, e.hash INTO last_sequence, last_hash
    FROM audit_entries e
    WHERE e.tenant_id = NEW.tenant_id
    ORDER BY e.sequence DESC
    LIMIT 1;
    IF NEW.sequence <> coalesce(last_sequence, 0) + 1 THEN
        RAISE EXCEPTION 'audit_entries: sequence % does not follow %', NEW.sequence, coalesce(last_sequence, 0)
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    IF NEW.prev_hash <> coalesce(last_hash, '\x0000000000000000000000000000000000000000000000000000000000000000'::bytea) THEN
        RAISE EXCEPTION 'audit_entries: prev_hash of sequence % is not the previous hash', NEW.sequence
            USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
END
$$;

CREATE TRIGGER audit_entries_link BEFORE INSERT ON audit_entries
    FOR EACH ROW EXECUTE FUNCTION audit_entries_link();

ALTER TABLE audit_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_entries FORCE ROW LEVEL SECURITY;
CREATE POLICY audit_entries_tenant_isolation ON audit_entries
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());
-- Append-only: no UPDATE, no DELETE, no TRUNCATE.
GRANT SELECT, INSERT ON audit_entries TO glossa_app;
