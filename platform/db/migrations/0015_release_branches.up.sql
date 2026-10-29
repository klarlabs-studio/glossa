-- 0015 — Release: branch environments and delivery-key scopes
-- (RFC 0004 §4.2, §4.3).
--
-- A branch environment previews one open branch: its release is built
-- from the main catalog plus that branch's overlay, under a fixed
-- preview policy, and can't be promoted. Its name is pr-<number> or
-- br-<8 hex of sha256(branch)>; those names are reserved for branch
-- environments, so glossa-edge can tell one by its name alone.
--
-- A delivery key reads the environments on its allowlist and, as a
-- preview key, every branch environment. Existing keys keep what they
-- could read by default: the four default environments. New keys
-- default to production.

-- ── environments ───────────────────────────────────────────────────

ALTER TABLE release_environments
    ADD COLUMN kind   text NOT NULL DEFAULT 'standard' CHECK (kind IN ('standard', 'branch')),
    ADD COLUMN branch text CHECK (octet_length(branch) BETWEEN 1 AND 255),
    ADD CONSTRAINT release_environments_branch_ref_check CHECK ((kind = 'branch') = (branch IS NOT NULL)),
    ADD CONSTRAINT release_environments_branch_name_check
        CHECK (kind <> 'branch' OR name ~ '^(pr-[1-9][0-9]{0,17}|br-[0-9a-f]{8})$');

-- One environment per branch.
CREATE UNIQUE INDEX release_environments_branch ON release_environments (project_id, branch)
    WHERE branch IS NOT NULL;

-- ── releases ───────────────────────────────────────────────────────
-- The branch whose overlay a release was built with (NULL: the main
-- catalog). A branch release can't be promoted.

ALTER TABLE release_releases
    ADD COLUMN branch text CHECK (octet_length(branch) BETWEEN 1 AND 255);

-- ── publish requests ───────────────────────────────────────────────
-- A branch push, or a change to a branch message's translations, asks
-- for the branch environment to be published again, debounced: each
-- request moves not_before later (bounded by first_requested_at), and
-- the publisher publishes once it is due. request_id changes with every
-- request; the publish uses it as its idempotency key.

CREATE TABLE release_publish_requests (
    tenant_id          uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id         uuid        NOT NULL,
    environment        text        NOT NULL,
    request_id         uuid        NOT NULL,
    first_requested_at timestamptz NOT NULL,
    not_before         timestamptz NOT NULL,
    requested_by       text        NOT NULL,
    PRIMARY KEY (project_id, environment),
    FOREIGN KEY (project_id, environment) REFERENCES release_environments (project_id, name) ON DELETE CASCADE,
    CHECK (not_before >= first_requested_at)
);
CREATE INDEX release_publish_requests_tenant ON release_publish_requests (tenant_id);
CREATE INDEX release_publish_requests_due ON release_publish_requests (not_before);

ALTER TABLE release_publish_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE release_publish_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY release_publish_requests_tenant_isolation ON release_publish_requests
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());
GRANT SELECT, INSERT, UPDATE, DELETE ON release_publish_requests TO glossa_app;

-- The publisher finds due requests across tenants (system scope
-- release.publisher), then publishes each in its tenant's scope.
CREATE POLICY release_publish_requests_system_select ON release_publish_requests
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (tenant_id, project_id, environment, not_before) ON release_publish_requests TO glossa_system;

-- ── delivery keys ──────────────────────────────────────────────────
-- environments: the allowlist, never a branch environment by name.
-- branches: a preview key, which also reads every branch environment.
-- index_version: the key index object format last written for the key
-- (1: before scopes; 2: with them). Existing keys migrate to the default
-- environments; the key index task rewrites their objects.

ALTER TABLE release_delivery_keys
    ADD COLUMN environments  text[]   NOT NULL DEFAULT '{development,preview,staging,production}',
    ADD COLUMN branches      boolean  NOT NULL DEFAULT false,
    ADD COLUMN index_version smallint NOT NULL DEFAULT 1 CHECK (index_version > 0),
    ADD CONSTRAINT release_delivery_keys_scope_check CHECK (
        cardinality(environments) <= 50
        AND (cardinality(environments) > 0 OR branches)
        AND array_to_string(environments, ',') ~ '^([a-z0-9][a-z0-9-]{0,62}(,[a-z0-9][a-z0-9-]{0,62})*)?$'
    );
ALTER TABLE release_delivery_keys ALTER COLUMN environments SET DEFAULT '{production}';

-- The key index task finds active keys whose object predates their
-- format across tenants (system scope release.key_index), then rewrites
-- each in its tenant's scope.
CREATE INDEX release_delivery_keys_index_version ON release_delivery_keys (index_version)
    WHERE revoked_at IS NULL;
CREATE POLICY release_delivery_keys_system_select ON release_delivery_keys
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (tenant_id, project_id, id, revoked_at, index_version) ON release_delivery_keys TO glossa_system;
