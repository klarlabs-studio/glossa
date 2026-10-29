-- 0017 — the daily purge jobs' cluster-wide lease (RFC 0004 §2.3).
--
-- The jobs themselves need no grants of their own here: Context's
-- retention keeps the ones migration 0014 opened, and Catalog's
-- proposal sweep the ones migration 0016 opened for its system scope
-- catalog.proposals.
--
-- glossa-server runs several replicas, and the purge is not something
-- to run twice at once: each periodic job takes a named lease first,
-- like a worker claims a job. The lease is a single row per job, held
-- for as long as the run may take, with the last completed run
-- recorded so a replica that starts a minute later doesn't purge
-- again.

-- ── leases ─────────────────────────────────────────────────────────
-- Tenantless: a lease belongs to the deployment, not to a tenant. It
-- is reached only as glossa_system (scope "scheduler.lease"), so
-- glossa_app gets no grant at all and row-level security has nothing
-- tenant-shaped to enforce.

CREATE TABLE system_leases (
    name        text        PRIMARY KEY CHECK (name ~ '^[a-z][a-z0-9_.-]{0,62}$'),
    holder      text        NOT NULL DEFAULT '' CHECK (octet_length(holder) <= 200),
    acquired_at timestamptz,
    expires_at  timestamptz,
    -- When the job last finished a run. Due-ness is measured from it,
    -- so an interval survives restarts and rolling upgrades.
    last_run_at timestamptz
);

ALTER TABLE system_leases ENABLE ROW LEVEL SECURITY;
ALTER TABLE system_leases FORCE ROW LEVEL SECURITY;
CREATE POLICY system_leases_system ON system_leases TO glossa_system USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE ON system_leases TO glossa_system;
