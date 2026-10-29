-- 0001 — glossa-server kernel: roles, tenants, transactional outbox.
--
-- Tenancy model (RFC 0002 §6, Klarlabs product standard §2): one shared
-- schema, isolation enforced by Postgres row-level security. Every
-- tenant-owned table has ENABLE + FORCE ROW LEVEL SECURITY and a policy
-- keyed on app_current_tenant(), which reads the transaction-local GUC
-- app.tenant_id set by the unit of work. FORCE makes the policies bind
-- the table owner too; only superusers and BYPASSRLS roles escape them,
-- and glossa-server refuses to start on such a connection.
--
-- Roles
--   owner          runs migrations and owns every object. In production
--                  it is the CNPG database owner with CREATEROLE (never a
--                  superuser); tests use a CREATEROLE owner as well.
--   glossa_app     the application's login role: NOSUPERUSER,
--                  NOBYPASSRLS, DML only, fully subject to RLS. Created
--                  here as NOLOGIN; the deployment grants LOGIN and a
--                  password out of band (CNPG managed role with a
--                  passwordSecret, or ALTER ROLE … LOGIN PASSWORD …).
--   glossa_system  NOLOGIN, NOBYPASSRLS. Reachable only by `SET LOCAL
--                  ROLE` inside the kernel's system-scope transaction.
--                  It sees a table only where a migration grants it
--                  privileges *and* adds a policy for it — today, the
--                  outbox relay's SELECT/UPDATE on outbox_events.
--
-- Roles are cluster-wide, so they are created only if missing and are
-- never dropped by a down migration.

DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'glossa_app') THEN
        CREATE ROLE glossa_app NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'glossa_system') THEN
        CREATE ROLE glossa_system NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS;
    END IF;
    -- glossa_app may SET ROLE glossa_system but does not inherit its
    -- privileges or policies: system scope is always an explicit switch.
    IF NOT EXISTS (
        SELECT FROM pg_auth_members m
        JOIN pg_roles g ON g.oid = m.roleid
        JOIN pg_roles u ON u.oid = m.member
        WHERE g.rolname = 'glossa_system' AND u.rolname = 'glossa_app'
    ) THEN
        GRANT glossa_system TO glossa_app WITH INHERIT FALSE, SET TRUE;
    END IF;
END
$$;

GRANT USAGE ON SCHEMA public TO glossa_app, glossa_system;

-- The tenant a transaction is scoped to, or NULL outside tenant scope.
-- NULL never equals anything, so an unscoped transaction sees no
-- tenant-owned rows and can write none.
CREATE FUNCTION app_current_tenant() RETURNS uuid
    LANGUAGE sql STABLE PARALLEL SAFE
    AS $$ SELECT nullif(current_setting('app.tenant_id', true), '')::uuid $$;

-- ── tenants ────────────────────────────────────────────────────────
-- The tenancy root. It has no tenant_id column: a tenant's row is keyed
-- by its own id. Creating a tenant means scoping the transaction to the
-- new id first, so the WITH CHECK below admits exactly that row.

CREATE TABLE tenants (
    id         uuid        PRIMARY KEY,
    kind       text        NOT NULL CHECK (kind IN ('individual', 'organization')),
    slug       text        NOT NULL UNIQUE
                           CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
    name       text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenants_tenant_isolation ON tenants
    USING (id = app_current_tenant())
    WITH CHECK (id = app_current_tenant());

GRANT SELECT, INSERT, UPDATE ON tenants TO glossa_app;

-- ── outbox_events ──────────────────────────────────────────────────
-- Domain events written in the same transaction as the state change
-- that raised them, then delivered in-process at least once.
--
--   status        pending → delivered | dead
--   attempts      delivery attempts started (incremented at claim, so a
--                 handler that crashes the process still counts)
--   available_at  earliest next claim: now() for new events, the lease
--                 end while claimed, the backoff deadline after a failure
--   claim_token   identifies the current lease; settlement must match it
--   delivered_to  subscribers that already handled the event, so a retry
--                 re-runs only the ones that failed

CREATE TABLE outbox_events (
    id             uuid        PRIMARY KEY,
    tenant_id      uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    event_type     text        NOT NULL CHECK (char_length(event_type) BETWEEN 1 AND 200),
    aggregate_type text        NOT NULL CHECK (char_length(aggregate_type) BETWEEN 1 AND 200),
    aggregate_id   text        NOT NULL CHECK (char_length(aggregate_id) BETWEEN 1 AND 200),
    payload        jsonb       NOT NULL,
    trace_context  jsonb       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at    timestamptz NOT NULL DEFAULT now(),
    status         text        NOT NULL DEFAULT 'pending'
                               CHECK (status IN ('pending', 'delivered', 'dead')),
    attempts       integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at   timestamptz NOT NULL DEFAULT now(),
    claim_token    uuid,
    delivered_to   text[]      NOT NULL DEFAULT '{}',
    last_error     text,
    delivered_at   timestamptz,
    dead_at        timestamptz
);

CREATE INDEX outbox_events_claimable ON outbox_events (available_at, id)
    WHERE status = 'pending';
CREATE INDEX outbox_events_tenant ON outbox_events (tenant_id, occurred_at);
CREATE INDEX outbox_events_dead ON outbox_events (dead_at) WHERE status = 'dead';

ALTER TABLE outbox_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbox_events FORCE ROW LEVEL SECURITY;
CREATE POLICY outbox_events_tenant_isolation ON outbox_events
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());

-- The relay claims and settles events across tenants. It can read and
-- update them, but not insert or delete: it can't forge or drop events.
CREATE POLICY outbox_events_system_select ON outbox_events
    FOR SELECT TO glossa_system USING (true);
CREATE POLICY outbox_events_system_update ON outbox_events
    FOR UPDATE TO glossa_system USING (true) WITH CHECK (true);

GRANT SELECT, INSERT ON outbox_events TO glossa_app;
GRANT SELECT, UPDATE ON outbox_events TO glossa_system;
