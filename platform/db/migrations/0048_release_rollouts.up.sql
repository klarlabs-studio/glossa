-- 0048 — Release: staged rollouts (RFC 0006 §5.2, runtimes/SPEC.md §1.4).
--
-- A rollout serves a candidate release to a share of an environment's
-- installations beside the release the environment points at. The
-- runtime picks its side from the manifest's `rollout` member (salt and
-- percent); this table is where the manifest writer reads them.
--
-- At most one rollout per environment is active (the partial unique
-- index). An ended rollout stays as history: who started it, how far it
-- went, and who ended it how — completed, aborted by a person, aborted
-- by the max_duration sweep (expired), or aborted by a rollback
-- (rolled_back). The application never deletes a row.
--
-- The salt is 16 random bytes as 22 base64url characters, fixed for the
-- rollout's life; percent is an integer 0–100 (SPEC §1.4).

CREATE TABLE release_rollouts (
    id                   uuid        PRIMARY KEY,
    tenant_id            uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id           uuid        NOT NULL,
    environment          text        NOT NULL,
    candidate_release_id uuid        NOT NULL REFERENCES release_releases (id),
    stable_release_id    uuid        NOT NULL REFERENCES release_releases (id),
    percent              smallint    NOT NULL CHECK (percent BETWEEN 0 AND 100),
    salt                 text        NOT NULL CHECK (salt ~ '^[A-Za-z0-9_-]{22}$'),
    status               text        NOT NULL CHECK (status IN ('active', 'completed', 'aborted')),
    max_duration_seconds bigint      NOT NULL CHECK (max_duration_seconds BETWEEN 3600 AND 7776000),
    expires_at           timestamptz NOT NULL,
    -- A start forced past the environment's check policy (RFC 0005
    -- §4.1), recorded as release_deployments records one (0036).
    forced               boolean     NOT NULL DEFAULT false,
    force_reason         text        NOT NULL DEFAULT '' CHECK (char_length(force_reason) <= 1000),
    started_by           text        NOT NULL,
    started_at           timestamptz NOT NULL,
    updated_at           timestamptz NOT NULL,
    version              integer     NOT NULL CHECK (version > 0),
    ended_by             text,
    ended_at             timestamptz,
    end_reason           text        CHECK (end_reason IN ('completed', 'aborted', 'expired', 'rolled_back')),
    FOREIGN KEY (project_id, environment) REFERENCES release_environments (project_id, name) ON DELETE CASCADE,
    CHECK (candidate_release_id <> stable_release_id),
    CHECK (forced = (force_reason <> '')),
    CHECK ((status = 'active') = (ended_at IS NULL)),
    CHECK ((ended_at IS NULL) = (ended_by IS NULL) AND (ended_at IS NULL) = (end_reason IS NULL)),
    CHECK (status <> 'completed' OR end_reason = 'completed'),
    CHECK (status <> 'aborted' OR end_reason IN ('aborted', 'expired', 'rolled_back'))
);
CREATE UNIQUE INDEX release_rollouts_one_active ON release_rollouts (project_id, environment)
    WHERE status = 'active';
CREATE INDEX release_rollouts_environment ON release_rollouts (project_id, environment, started_at DESC);
CREATE INDEX release_rollouts_tenant ON release_rollouts (tenant_id);
CREATE INDEX release_rollouts_expiry ON release_rollouts (expires_at) WHERE status = 'active';

ALTER TABLE release_rollouts ENABLE ROW LEVEL SECURITY;
ALTER TABLE release_rollouts FORCE ROW LEVEL SECURITY;
CREATE POLICY release_rollouts_tenant_isolation ON release_rollouts
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());
-- No DELETE: an ended rollout is history.
GRANT SELECT, INSERT, UPDATE ON release_rollouts TO glossa_app;

-- The max_duration sweep finds expired active rollouts across tenants
-- (system scope release.rollout_sweeper), then aborts each in its
-- tenant's scope; the rollouts gauge counts the active ones. Both read
-- identifiers, the status and the expiry only.
CREATE POLICY release_rollouts_system_select ON release_rollouts
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (id, tenant_id, project_id, environment, status, expires_at) ON release_rollouts TO glossa_system;
