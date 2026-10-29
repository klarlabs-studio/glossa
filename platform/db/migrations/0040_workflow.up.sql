-- 0040 — Workflow: definitions, their immutable versions, and bindings
-- (RFC 0006 §2.3, §13 wave 1).
--
-- A workflow definition is data: a glossa.workflow/v1 document (a
-- statekit Native JSON chart plus the envelope that binds its guard and
-- action names to the platform's vocabulary). What a document may *say*
-- is the domain's to check, at save, by compiling and linting it
-- (workflow/domain.Compile); storage only knows it is an object. A
-- vocabulary bounded twice would be two places to change when an RFC
-- amendment adds a primitive.
--
-- Instances, their transition log and the sweep's timers are wave 2's
-- (the instance runner); this migration is what they will point at.
--
-- Workflow keys rows by Catalog's project id without a foreign key
-- (RFC 0002 §4): it learns about projects through a port.

-- ── definitions ─────────────────────────────────────────────────────
--
-- A definition's identity: its name, its subject and its optional
-- project scope. The documents are its versions. Deleting a definition
-- records when, and keeps its versions — an instance that ran on one
-- must stay explainable — and its bindings go with it, so a deleted
-- definition is never selected again. A deleted name can be reused.
CREATE TABLE workflow_definitions (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    -- NULL: every project of the tenant may bind it.
    project_id uuid,
    name       text        NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,63}$'),
    subject    text        NOT NULL CHECK (subject IN ('translation', 'release_request')),
    -- The newest version's number. Bumped in the transaction that
    -- inserts that version; the versions table's unique key is what
    -- keeps two concurrent saves from both landing.
    latest     integer     NOT NULL CHECK (latest >= 1),
    created_by text        NOT NULL,
    created_at timestamptz NOT NULL,
    deleted_at timestamptz,
    -- The composite key the other tables reference, so a version or a
    -- binding can never point at another tenant's definition — a
    -- foreign key check does not go through row-level security.
    UNIQUE (tenant_id, id)
);
CREATE INDEX workflow_definitions_tenant ON workflow_definitions (tenant_id);
-- One live definition per name in a scope: the tenant's, or one
-- project's.
CREATE UNIQUE INDEX workflow_definitions_name ON workflow_definitions (tenant_id, project_id, name)
    NULLS NOT DISTINCT WHERE deleted_at IS NULL;

ALTER TABLE workflow_definitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_definitions FORCE ROW LEVEL SECURITY;
CREATE POLICY workflow_definitions_tenant_isolation ON workflow_definitions
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

-- UPDATE only of what changes after creation: the latest version's
-- number, and the deletion. Name, subject and scope are identity.
GRANT SELECT, INSERT, DELETE ON workflow_definitions TO glossa_app;
GRANT UPDATE (latest, deleted_at) ON workflow_definitions TO glossa_app;

-- ── versions ────────────────────────────────────────────────────────
--
-- Immutable (§2.3): a save appends version n+1, a running instance stays
-- on the version it started with, and nothing rewrites a version. The
-- application role has no UPDATE. DELETE is erasing a deleted tenant's
-- or project's data, not editing history.
CREATE TABLE workflow_definition_versions (
    id            uuid        PRIMARY KEY,
    tenant_id     uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    definition_id uuid        NOT NULL,
    version       integer     NOT NULL CHECK (version >= 1),
    -- The schema the document was saved under, so it is loaded by the
    -- same loader whatever schemas exist later.
    schema        text        NOT NULL CHECK (schema ~ '^glossa\.workflow/v[0-9]+$'),
    document      jsonb       NOT NULL CHECK (jsonb_typeof(document) = 'object'),
    created_by    text        NOT NULL,
    created_at    timestamptz NOT NULL,
    FOREIGN KEY (tenant_id, definition_id) REFERENCES workflow_definitions (tenant_id, id) ON DELETE CASCADE,
    UNIQUE (definition_id, version)
);
CREATE INDEX workflow_definition_versions_tenant ON workflow_definition_versions (tenant_id);

ALTER TABLE workflow_definition_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_definition_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY workflow_definition_versions_tenant_isolation ON workflow_definition_versions
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

GRANT SELECT, INSERT, DELETE ON workflow_definition_versions TO glossa_app;

-- ── bindings ────────────────────────────────────────────────────────
--
-- Which definition runs for a subject: a project, optionally narrowed to
-- some locales and one namespace, resolved with the check policy's
-- precedence rule (RFC 0005 §4.1) — more fields named wins, a tie goes
-- to the later binding. "Later" is `position`, assigned by the
-- database in creation order. Changing a binding is unbinding and
-- binding again, which makes it the later one.
CREATE TABLE workflow_bindings (
    id            uuid        PRIMARY KEY,
    tenant_id     uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id    uuid        NOT NULL,
    -- The bound definition's subject, so resolving for a subject is one
    -- index scan and a translation's binding never selects a release
    -- request's definition.
    subject       text        NOT NULL CHECK (subject IN ('translation', 'release_request')),
    -- Canonical BCP 47 tags, sorted; empty means every locale.
    locales       text[]      NOT NULL DEFAULT '{}',
    -- NULL means every namespace.
    namespace     text        CHECK (namespace <> ''),
    definition_id uuid        NOT NULL,
    position      bigint      GENERATED ALWAYS AS IDENTITY,
    created_by    text        NOT NULL,
    created_at    timestamptz NOT NULL,
    FOREIGN KEY (tenant_id, definition_id) REFERENCES workflow_definitions (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX workflow_bindings_tenant ON workflow_bindings (tenant_id);
CREATE INDEX workflow_bindings_project ON workflow_bindings (project_id, subject, position);
CREATE INDEX workflow_bindings_definition ON workflow_bindings (definition_id);
-- Two bindings with the same selector could never both apply: the
-- earlier would be dead. Refuse the second instead of keeping it.
CREATE UNIQUE INDEX workflow_bindings_selector ON workflow_bindings (tenant_id, project_id, subject, locales, namespace)
    NULLS NOT DISTINCT;

ALTER TABLE workflow_bindings ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_bindings FORCE ROW LEVEL SECURITY;
CREATE POLICY workflow_bindings_tenant_isolation ON workflow_bindings
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

GRANT SELECT, INSERT, DELETE ON workflow_bindings TO glossa_app;
