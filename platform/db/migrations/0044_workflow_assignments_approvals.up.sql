-- 0044 — Workflow: assignments and approvals (RFC 0006 §3.1–3.3, §13
-- wave 2).
--
-- An assignment is a batch of translation units given to one member,
-- role, group or vendor; it is what assignment-scoped visibility reads
-- (§3.3). An approval asks n distinct eligible people to sign a subject
-- off; its decisions are append-only.
--
-- Assignees are stored in one spelling — "member:<uuid>", "group:<uuid>",
-- "vendor:<uuid>", "role:<name>" — so "assigned to any of these" is one
-- indexed comparison. Members, groups and vendors are Identity's; like
-- projects (RFC 0002 §4) they are referenced without a foreign key and
-- resolved through Identity's port when an assignment is made. An
-- assignment to a group or vendor that is later deleted simply covers
-- no one.

-- ── assignments ─────────────────────────────────────────────────────

CREATE TABLE workflow_assignments (
    id          uuid        PRIMARY KEY,
    tenant_id   uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id  uuid        NOT NULL,
    -- The workflow instance whose assign action made it; NULL for one
    -- made by hand.
    instance_id uuid,
    assignee    text        NOT NULL
        CHECK (assignee ~ '^((member|group|vendor):[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|role:[a-z]+)$'),
    permission  text        NOT NULL CHECK (permission ~ '^[a-z]+\.[a-z]+$'),
    due_at      timestamptz,
    state       text        NOT NULL CHECK (state IN ('open', 'accepted', 'done', 'declined', 'expired')),
    version     integer     NOT NULL CHECK (version >= 1),
    created_by  text        NOT NULL,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    closed_by   text,
    closed_at   timestamptz,
    reason      text        NOT NULL DEFAULT '' CHECK (char_length(reason) <= 2000),
    -- A live assignment is not closed; a closed one says when and by whom.
    CHECK ((state IN ('open', 'accepted')) = (closed_at IS NULL)),
    CHECK ((closed_at IS NULL) = (closed_by IS NULL)),
    UNIQUE (tenant_id, id)
);
CREATE INDEX workflow_assignments_tenant ON workflow_assignments (tenant_id);
-- Coverage and "my work": a project's assignments to some assignees.
CREATE INDEX workflow_assignments_assignee ON workflow_assignments (tenant_id, project_id, assignee, state);
CREATE INDEX workflow_assignments_due ON workflow_assignments (due_at) WHERE state IN ('open', 'accepted');

ALTER TABLE workflow_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_assignments FORCE ROW LEVEL SECURITY;
CREATE POLICY workflow_assignments_tenant_isolation ON workflow_assignments
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

-- UPDATE only of what changes over its life; what it covers, who it is
-- for and who made it are its identity.
GRANT SELECT, INSERT, DELETE ON workflow_assignments TO glossa_app;
GRANT UPDATE (state, version, updated_at, closed_by, closed_at, reason) ON workflow_assignments TO glossa_app;

-- The units of an assignment. Fixed when it is made: no UPDATE, no
-- DELETE for the application role (they go with their assignment).
CREATE TABLE workflow_assignment_units (
    tenant_id     uuid NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    assignment_id uuid NOT NULL,
    message_id    uuid NOT NULL,
    -- A canonical BCP 47 tag.
    locale        text NOT NULL CHECK (locale <> ''),
    PRIMARY KEY (assignment_id, message_id, locale),
    FOREIGN KEY (tenant_id, assignment_id) REFERENCES workflow_assignments (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX workflow_assignment_units_tenant ON workflow_assignment_units (tenant_id);
CREATE INDEX workflow_assignment_units_unit ON workflow_assignment_units (tenant_id, message_id, locale);

ALTER TABLE workflow_assignment_units ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_assignment_units FORCE ROW LEVEL SECURITY;
CREATE POLICY workflow_assignment_units_tenant_isolation ON workflow_assignment_units
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

GRANT SELECT, INSERT ON workflow_assignment_units TO glossa_app;

-- ── approvals ───────────────────────────────────────────────────────

CREATE TABLE workflow_approvals (
    id                   uuid        PRIMARY KEY,
    tenant_id            uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id           uuid        NOT NULL,
    instance_id          uuid,
    subject_kind         text        NOT NULL CHECK (subject_kind IN ('translation', 'release_request')),
    -- The message of a translation unit, or the release request.
    subject_id           uuid        NOT NULL,
    -- A translation unit's canonical BCP 47 tag; '' for a release request.
    locale               text        NOT NULL,
    CHECK ((subject_kind = 'translation') = (locale <> '')),
    required             integer     NOT NULL CHECK (required BETWEEN 1 AND 10),
    -- Who may decide: a member, a role or a group — never a vendor.
    eligible             text        NOT NULL
        CHECK (eligible ~ '^((member|group):[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|role:[a-z]+)$'),
    distinct_from_author boolean     NOT NULL,
    due_at               timestamptz,
    state                text        NOT NULL CHECK (state IN ('pending', 'granted', 'denied')),
    version              integer     NOT NULL CHECK (version >= 1),
    created_by           text        NOT NULL,
    created_at           timestamptz NOT NULL,
    closed_at            timestamptz,
    CHECK ((state = 'pending') = (closed_at IS NULL)),
    UNIQUE (tenant_id, id)
);
CREATE INDEX workflow_approvals_tenant ON workflow_approvals (tenant_id);
-- The newest approval of a subject.
CREATE INDEX workflow_approvals_subject ON workflow_approvals (tenant_id, project_id, subject_kind, subject_id, locale, created_at DESC);

ALTER TABLE workflow_approvals ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_approvals FORCE ROW LEVEL SECURITY;
CREATE POLICY workflow_approvals_tenant_isolation ON workflow_approvals
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

GRANT SELECT, INSERT, DELETE ON workflow_approvals TO glossa_app;
GRANT UPDATE (state, version, closed_at) ON workflow_approvals TO glossa_app;

-- Decisions are append-only (§3.2): the application role may read and
-- add them, never change or remove one. They go only with their
-- approval, by the foreign key's cascade.
CREATE TABLE workflow_approval_decisions (
    tenant_id   uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    approval_id uuid        NOT NULL,
    seq         integer     NOT NULL CHECK (seq >= 1),
    -- Who decided: always a person ("person:<uuid>"); no token decides.
    principal   text        NOT NULL CHECK (principal ~ '^person:'),
    verdict     text        NOT NULL CHECK (verdict IN ('granted', 'denied')),
    reason      text        NOT NULL DEFAULT '' CHECK (char_length(reason) <= 2000),
    decided_at  timestamptz NOT NULL,
    PRIMARY KEY (approval_id, seq),
    FOREIGN KEY (tenant_id, approval_id) REFERENCES workflow_approvals (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX workflow_approval_decisions_tenant ON workflow_approval_decisions (tenant_id);

ALTER TABLE workflow_approval_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_approval_decisions FORCE ROW LEVEL SECURITY;
CREATE POLICY workflow_approval_decisions_tenant_isolation ON workflow_approval_decisions
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

GRANT SELECT, INSERT ON workflow_approval_decisions TO glossa_app;
