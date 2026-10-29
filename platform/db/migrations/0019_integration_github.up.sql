-- 0019 — GitHub installations, Git connections and the webhook inbox
-- (RFC 0004 §6.1, §6.2).
--
-- The App itself is platform configuration (an App ID, a private key, a
-- webhook secret and OAuth credentials in one Kubernetes Secret, §14.1);
-- nothing of it is stored here. What is stored is the link between an
-- installation GitHub made and the tenant that claimed it, which of that
-- installation's repositories feed which projects, and the deliveries
-- GitHub has sent us but we have not processed yet.
--
-- Three rules shape the tables:
--
--   * An installation maps to exactly one tenant. The uniqueness on
--     GitHub's installation ID is therefore global, not per tenant: a
--     second tenant claiming the same installation hits the index and is
--     refused (`installation_already_claimed`). The index says nothing
--     about who holds it, so nothing leaks across the boundary.
--   * Repositories are keyed by their numeric ID, never by name, so a
--     rename or a transfer does not break a connection. One repository
--     can feed several projects, one per monorepo path — hence the
--     uniqueness on (repository_id, path) rather than repository_id.
--   * A delivery is stored and acknowledged, then processed by a worker.
--     Its row is tenantless until the installation resolves and moves to
--     the tenant afterwards, so the endpoint can write it before any
--     tenant is known. The payload is dropped once the delivery is
--     processed; the delivery ID stays for the replay window (7 days),
--     which is exactly what makes a duplicate a no-op.
--
-- Install intents hold the round trip together. The RFC calls for a
-- signed state bound to tenant, person and expiry; a random, unguessable
-- state whose SHA-256 is stored is that and more, because it is also
-- single-use and unforgeable rather than merely unaltered. The state
-- itself is never stored, only its hash, as with the sign-in links.

-- ── installations ──────────────────────────────────────────────────

CREATE TABLE integration_github_installations (
    id              uuid        PRIMARY KEY,
    tenant_id       uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    -- GitHub's own installation ID.
    installation_id bigint      NOT NULL CHECK (installation_id > 0),
    account_id      bigint      NOT NULL CHECK (account_id > 0),
    account_login   text        NOT NULL CHECK (char_length(account_login) BETWEEN 1 AND 100),
    account_type    text        NOT NULL CHECK (account_type IN ('User', 'Organization')),
    -- revoked: uninstalled on GitHub. The row stays so Studio can say
    -- what happened and the connections can be cleaned up deliberately.
    state           text        NOT NULL CHECK (state IN ('active', 'suspended', 'revoked')),
    connected_by    text        NOT NULL CHECK (char_length(connected_by) BETWEEN 1 AND 200),
    connected_at    timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL
);
CREATE INDEX integration_github_installations_tenant ON integration_github_installations (tenant_id);
-- One installation, one tenant (RFC 0004 §6.1).
CREATE UNIQUE INDEX integration_github_installations_github
    ON integration_github_installations (installation_id);

-- ── install intents ────────────────────────────────────────────────

CREATE TABLE integration_github_install_intents (
    id          uuid        PRIMARY KEY,
    tenant_id   uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    -- The person who started the installation; only they may finish it.
    person      text        NOT NULL CHECK (char_length(person) BETWEEN 1 AND 200),
    -- SHA-256 of the state, so the state itself is never stored.
    state_hash  bytea       NOT NULL CHECK (octet_length(state_hash) = 32),
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    redeemed_at timestamptz
);
CREATE INDEX integration_github_install_intents_tenant ON integration_github_install_intents (tenant_id);
CREATE UNIQUE INDEX integration_github_install_intents_state
    ON integration_github_install_intents (state_hash);
-- The sweep drops intents whose window has closed.
CREATE INDEX integration_github_install_intents_expiry ON integration_github_install_intents (expires_at);

-- ── Git connections ────────────────────────────────────────────────

CREATE TABLE integration_git_connections (
    id                uuid        PRIMARY KEY,
    tenant_id         uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    installation_id   uuid        NOT NULL REFERENCES integration_github_installations (id) ON DELETE CASCADE,
    -- The repository's numeric ID: a rename does not break it.
    repository_id     bigint      NOT NULL CHECK (repository_id > 0),
    -- A label for Studio, refreshed from GitHub. Never keyed on.
    repository_name   text        NOT NULL DEFAULT '' CHECK (char_length(repository_name) <= 255),
    project_id        uuid        NOT NULL,
    application_id    uuid        NOT NULL,
    default_branch    text        NOT NULL CHECK (char_length(default_branch) BETWEEN 1 AND 255),
    -- The monorepo subdirectory this connection covers ('': the whole
    -- repository). One repository feeds several projects, one per path.
    path              text        NOT NULL DEFAULT '' CHECK (char_length(path) <= 512),
    created_by        text        NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 200),
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL
);
CREATE INDEX integration_git_connections_tenant ON integration_git_connections (tenant_id);
CREATE INDEX integration_git_connections_installation ON integration_git_connections (installation_id);
-- A branch event resolves (repository, path) to its projects.
CREATE INDEX integration_git_connections_repo ON integration_git_connections (repository_id);
CREATE INDEX integration_git_connections_project ON integration_git_connections (project_id);
-- One repository feeds a path once, so a pull request has one branch per
-- project rather than two connections fighting over it.
CREATE UNIQUE INDEX integration_git_connections_repo_path
    ON integration_git_connections (repository_id, path);

-- ── the webhook inbox ──────────────────────────────────────────────

CREATE TABLE integration_github_deliveries (
    -- X-GitHub-Delivery, GitHub's own ID: the inbox key. A duplicate
    -- INSERT is a no-op, which is both the idempotency and the replay
    -- protection.
    delivery_id     text        PRIMARY KEY CHECK (char_length(delivery_id) BETWEEN 1 AND 128),
    -- NULL until the worker resolves the installation; the row moves to
    -- the tenant then.
    tenant_id       uuid        REFERENCES tenants (id) ON DELETE CASCADE,
    event           text        NOT NULL CHECK (event ~ '^[a-z][a-z_]{0,63}$'),
    action          text        NOT NULL DEFAULT '' CHECK (action ~ '^[a-z_]{0,63}$'),
    -- GitHub's installation ID from the payload (0 when it had none).
    installation_id bigint      NOT NULL DEFAULT 0 CHECK (installation_id >= 0),
    state           text        NOT NULL CHECK (state IN ('pending', 'done', 'ignored', 'failed')),
    attempts        integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    -- The raw signed body, dropped once the delivery is processed
    -- (RFC 0004 §6.2). NULL afterwards.
    payload         bytea       CHECK (payload IS NULL OR octet_length(payload) <= 5242880),
    -- Why it failed, for the operator. Never payload content.
    failure         text        NOT NULL DEFAULT '' CHECK (char_length(failure) <= 500),
    claim_token     uuid,
    received_at     timestamptz NOT NULL,
    available_at    timestamptz NOT NULL,
    processed_at    timestamptz
);
CREATE INDEX integration_github_deliveries_claimable
    ON integration_github_deliveries (available_at, delivery_id) WHERE state = 'pending';
-- The sweep removes rows past the replay window.
CREATE INDEX integration_github_deliveries_received ON integration_github_deliveries (received_at);
-- Inbox depth per event, for the §11 metric.
CREATE INDEX integration_github_deliveries_state ON integration_github_deliveries (state, event);

-- ── row-level security ─────────────────────────────────────────────

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['integration_github_installations', 'integration_github_install_intents',
                             'integration_git_connections', 'integration_github_deliveries']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I USING (tenant_id = app_current_tenant())'
                       ' WITH CHECK (tenant_id = app_current_tenant())', t || '_tenant_isolation', t);
    END LOOP;
END
$$;

-- A delivery arrives with no tenant on the request: GitHub signs it, and
-- which tenant it belongs to is only known once the installation is
-- looked up. The endpoint stores it and the worker claims, resolves and
-- settles it as glossa_system (scope "integration.github"). A tenantless
-- row is invisible to the tenant policy above, because NULL = anything
-- is never true; once tenant_id is set, that tenant sees it.
CREATE POLICY integration_github_deliveries_system ON integration_github_deliveries
    TO glossa_system USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON integration_github_deliveries TO glossa_system;

-- Resolving a delivery's installation to its tenant, across tenants. It
-- reads the mapping and the state, nothing else.
CREATE POLICY integration_github_installations_system_select ON integration_github_installations
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (tenant_id, installation_id, state) ON integration_github_installations TO glossa_system;

-- The same sweep drops install intents whose window has closed. It needs
-- a SELECT policy as well as a DELETE one: a DELETE whose WHERE clause
-- reads a column needs SELECT rights, so PostgreSQL applies the SELECT
-- policies too, and without one the sweep would quietly delete nothing.
-- The grant is the one column the sweep reads.
CREATE POLICY integration_github_install_intents_system_select ON integration_github_install_intents
    FOR SELECT TO glossa_system USING (true);
CREATE POLICY integration_github_install_intents_system_delete ON integration_github_install_intents
    FOR DELETE TO glossa_system USING (true);
GRANT SELECT (expires_at), DELETE ON integration_github_install_intents TO glossa_system;

GRANT SELECT, INSERT, UPDATE, DELETE ON integration_github_installations TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON integration_github_install_intents TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON integration_git_connections TO glossa_app;
-- The inbox is the worker's; a tenant reads its own settled deliveries.
GRANT SELECT ON integration_github_deliveries TO glossa_app;
