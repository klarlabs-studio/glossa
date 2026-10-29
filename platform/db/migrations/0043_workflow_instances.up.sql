-- 0043 — Workflow: instances, their transition log, and the default
-- definition's seeding record (RFC 0006 §2.5, §13 wave 2).
--
-- An instance exists only while work is in flight. It is created by the
-- first trigger under a binding; when it reaches a final state its
-- snapshot is dropped, its status becomes `finished`, and its
-- transition log stays. The row count is the work in progress, not the
-- catalog's size times its locales (intent §63.10–11).
--
-- Stepping happens in the outbox handler's tenant transaction with the
-- instance row locked (SELECT … FOR UPDATE), so two events about one
-- subject serialize; idempotency is the transition log's unique
-- (instance, outbox event id).
--
-- Like 0040, Workflow keys rows by Catalog's project and message ids
-- without a foreign key (RFC 0002 §4): it learns about them through
-- ports. Every reference inside Workflow is a composite (tenant_id, …)
-- foreign key, because a foreign key check does not go through
-- row-level security.

-- An instance points at the exact version it runs on.
ALTER TABLE workflow_definition_versions
    ADD CONSTRAINT workflow_definition_versions_tenant_version UNIQUE (tenant_id, definition_id, version);

-- ── instances ───────────────────────────────────────────────────────
CREATE TABLE workflow_instances (
    id            uuid        PRIMARY KEY,
    tenant_id     uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id    uuid        NOT NULL,
    definition_id uuid        NOT NULL,
    version       integer     NOT NULL CHECK (version >= 1),
    subject_kind  text        NOT NULL CHECK (subject_kind IN ('translation', 'release_request')),
    -- The message of a translation unit; the release request otherwise.
    subject_id    uuid        NOT NULL,
    -- The unit's canonical BCP 47 tag; '' for a release request.
    locale        text        NOT NULL DEFAULT '' CHECK ((subject_kind = 'translation') = (locale <> '')),
    -- The chart state it is in. '' is "not started": the initial
    -- state's entry actions were refused, so the instance never entered
    -- it, and the next event tries again.
    state         text        NOT NULL,
    -- The statekit snapshot to Restore from; NULL once finished.
    snapshot      jsonb       CHECK (snapshot IS NULL OR jsonb_typeof(snapshot) = 'object'),
    status        text        NOT NULL CHECK (status IN ('active', 'finished')),
    -- Timers (RFC 0006 §2.3): an action with a due period stores when
    -- timer.due and timer.overdue fall due, for the state it set them
    -- in. The sweep raises each once and clears it; leaving the state
    -- clears both. No timer lives in a process.
    due_at        timestamptz,
    overdue_at    timestamptz,
    timer_state   text,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    finished_at   timestamptz,
    FOREIGN KEY (tenant_id, definition_id, version)
        REFERENCES workflow_definition_versions (tenant_id, definition_id, version) ON DELETE CASCADE,
    UNIQUE (tenant_id, id),
    CHECK ((status = 'finished') = (finished_at IS NOT NULL)),
    CHECK (status = 'active' OR (snapshot IS NULL AND due_at IS NULL AND overdue_at IS NULL)),
    CHECK ((due_at IS NULL AND overdue_at IS NULL) = (timer_state IS NULL))
);
CREATE INDEX workflow_instances_tenant ON workflow_instances (tenant_id);
-- One active instance per definition and subject: two concurrent first
-- triggers race on this index, and the loser steps the winner's
-- instance instead of starting a second.
CREATE UNIQUE INDEX workflow_instances_active
    ON workflow_instances (tenant_id, definition_id, subject_kind, subject_id, locale) WHERE status = 'active';
-- An event finds the active instances of its subject.
CREATE INDEX workflow_instances_subject
    ON workflow_instances (subject_kind, subject_id, locale) WHERE status = 'active';
-- The read API lists a project's instances, newest first.
CREATE INDEX workflow_instances_project ON workflow_instances (project_id, id DESC);
-- The sweep finds what has fallen due.
CREATE INDEX workflow_instances_timers ON workflow_instances (LEAST(due_at, overdue_at))
    WHERE status = 'active' AND timer_state IS NOT NULL;

ALTER TABLE workflow_instances ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_instances FORCE ROW LEVEL SECURITY;
CREATE POLICY workflow_instances_tenant_isolation ON workflow_instances
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

-- UPDATE only of what stepping changes; the subject, the project and
-- the version an instance runs on are its identity (moving an instance
-- to a new version is wave 6's explicit rebase). No DELETE: a finished
-- instance is what its transition log hangs off, and retention is
-- wave 6's.
GRANT SELECT, INSERT ON workflow_instances TO glossa_app;
GRANT UPDATE (state, snapshot, status, due_at, overdue_at, timer_state, updated_at, finished_at)
    ON workflow_instances TO glossa_app;

-- ── system scope workflow.timers ────────────────────────────────────
--
-- The timer sweep runs on the kernel scheduler outside any tenant, finds
-- which tenants have a timer due, and then raises each one in that
-- tenant's own scope. This opens to glossa_system the tenant and the
-- timer columns only: no subject, no state, no project.
CREATE POLICY workflow_instances_system_select ON workflow_instances
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (tenant_id, status, due_at, overdue_at, timer_state) ON workflow_instances TO glossa_system;

-- ── transitions ─────────────────────────────────────────────────────
--
-- The instance's log, append-only: every event that reached it, what
-- the guards said, which actions ran as whom and what became of each,
-- and whether the instance moved (applied), the event no longer applied
-- (ignored — the world moved on, which is normal), or an action was
-- refused and the instance stayed where it was (refused).
CREATE TABLE workflow_transitions (
    tenant_id       uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    instance_id     uuid        NOT NULL,
    seq             bigint      NOT NULL CHECK (seq >= 1),
    -- '' is "not started" (see workflow_instances.state).
    from_state      text        NOT NULL,
    event           text        NOT NULL,
    to_state        text        NOT NULL,
    outcome         text        NOT NULL CHECK (outcome IN ('applied', 'ignored', 'refused')),
    guards          jsonb       NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(guards) = 'array'),
    actions         jsonb       NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(actions) = 'array'),
    -- The outbox spelling of who caused the event; the actions ran as
    -- them (§2.5).
    actor           text        NOT NULL,
    outbox_event_id uuid        NOT NULL,
    at              timestamptz NOT NULL,
    PRIMARY KEY (instance_id, seq),
    -- Idempotency: an event steps an instance once, however often the
    -- outbox delivers it.
    UNIQUE (instance_id, outbox_event_id),
    FOREIGN KEY (tenant_id, instance_id) REFERENCES workflow_instances (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX workflow_transitions_tenant ON workflow_transitions (tenant_id);

ALTER TABLE workflow_transitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_transitions FORCE ROW LEVEL SECURITY;
CREATE POLICY workflow_transitions_tenant_isolation ON workflow_transitions
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

-- Append-only for the application role: no UPDATE, no DELETE.
GRANT SELECT, INSERT ON workflow_transitions TO glossa_app;

-- ── seeding ─────────────────────────────────────────────────────────
--
-- The default review definition is seeded once per tenant, on first
-- use (§2.3). This row is the "once": a tenant that deleted the default
-- ("no workflow", M4's behaviour) never gets it back by accident.
CREATE TABLE workflow_seeds (
    tenant_id     uuid        PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    -- The definition seeded; NULL when the tenant already had its own
    -- definition of that name, which is then left alone.
    definition_id uuid,
    seeded_at     timestamptz NOT NULL,
    -- Definitions are never deleted, only marked (0040); the row goes
    -- with its tenant.
    FOREIGN KEY (tenant_id, definition_id) REFERENCES workflow_definitions (tenant_id, id)
);

ALTER TABLE workflow_seeds ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_seeds FORCE ROW LEVEL SECURITY;
CREATE POLICY workflow_seeds_tenant_isolation ON workflow_seeds
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

GRANT SELECT, INSERT ON workflow_seeds TO glossa_app;
