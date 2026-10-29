-- 0047 — Release approvals: an environment's approval requirement and
-- the release requests it holds (RFC 0006 §5.1, §13 wave 3).
--
-- An environment may require that a publish or a promote into it is
-- approved by n people other than the requester. Such a move becomes a
-- release request and moves no pointer; the pointer moves only when the
-- request is approved, through the same deployment path, after the
-- publish gate has been run again. Rollback never waits.
--
-- The requirement is versioned with the environment: changing it bumps
-- release_environments.version, as a policy change does, and publishes
-- release.environment.policy_changed naming the actor.

ALTER TABLE release_environments
    -- {"n": 1-10, "from": {"member"|"role"|"group": "..."},
    --  "distinct_from_requester": true}; NULL is no approval (default).
    -- A branch environment's policy is fixed and requires none.
    ADD COLUMN approval jsonb CHECK (approval IS NULL OR jsonb_typeof(approval) = 'object'),
    ADD CONSTRAINT release_environments_branch_no_approval CHECK (kind <> 'branch' OR approval IS NULL);

-- ── release requests ───────────────────────────────────────────────
--
-- Keyed by Catalog's project id without a foreign key, like every
-- Release table. The release it would deploy is immutable and always
-- recorded first (a publish's release is built, gated and stored when
-- it is requested), so the reference holds.
CREATE TABLE release_requests (
    id           uuid        PRIMARY KEY,
    tenant_id    uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id   uuid        NOT NULL,
    environment  text        NOT NULL CHECK (environment ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    release_id   uuid        NOT NULL REFERENCES release_releases (id),
    -- What the deploy records: a publish or a promote. Rollback is never
    -- held.
    action       text        NOT NULL CHECK (action IN ('publish', 'promote')),
    requester    text        NOT NULL,
    -- The environment's requirement when the request was made.
    approval     jsonb       NOT NULL CHECK (jsonb_typeof(approval) = 'object'),
    -- The publish gate's verdict when the request was made, and the
    -- force the requester asked for (RFC 0005 §4.1): it overrides the
    -- gate — again at deploy time — and never the approval.
    gate_met     boolean     NOT NULL,
    gate_unmet   jsonb       NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(gate_unmet) = 'array'),
    forced       boolean     NOT NULL DEFAULT false,
    force_reason text        NOT NULL DEFAULT '' CHECK (char_length(force_reason) <= 1000),
    state        text        NOT NULL CHECK (state IN ('pending', 'deployed', 'denied', 'withdrawn', 'refused')),
    version      integer     NOT NULL CHECK (version >= 1),
    created_at   timestamptz NOT NULL,
    decided_by   text,
    decided_at   timestamptz,
    -- Why it was refused (the gate's answer at deploy time) or withdrawn.
    reason       text        NOT NULL DEFAULT '' CHECK (char_length(reason) <= 2000),
    CONSTRAINT release_requests_forced_has_reason CHECK (forced = (force_reason <> '')),
    -- A request whose gate was not met exists only because it was forced.
    CONSTRAINT release_requests_unmet_is_forced CHECK (gate_met OR forced),
    CONSTRAINT release_requests_decided CHECK ((state = 'pending') = (decided_at IS NULL)
                                               AND (decided_at IS NULL) = (decided_by IS NULL))
);
CREATE INDEX release_requests_tenant ON release_requests (tenant_id);
CREATE INDEX release_requests_project ON release_requests (project_id, id DESC);
-- One open request per environment: a newer one withdraws the older in
-- the same transaction, so two requests can never both deploy.
CREATE UNIQUE INDEX release_requests_pending ON release_requests (project_id, environment) WHERE state = 'pending';

ALTER TABLE release_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE release_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY release_requests_tenant_isolation ON release_requests
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

-- A request is closed once and never deleted: what it asked for is
-- identity, only its outcome changes.
GRANT SELECT, INSERT ON release_requests TO glossa_app;
GRANT UPDATE (state, version, decided_by, decided_at, reason) ON release_requests TO glossa_app;
