-- 0041 — Identity: project scope, vendors, visibility and groups
-- (RFC 0006 §3.3, §4; §13 wave 1).
--
-- ┌─────────────────────────────────────────────────────────────────┐
-- │ MODELLED, NOT ENFORCED. These columns are stored and validated, │
-- │ and nothing reads them to keep anyone out yet. A project-scoped │
-- │ member or token still reaches every project; a member with      │
-- │ visibility 'assigned' still reads the whole tenant. RFC 0006    │
-- │ wave 2 enforces both in every read path at once, behind a sweep │
-- │ over every endpoint. Until then glossa_system deliberately gets │
-- │ no grant on the new columns: the lookups that build a request's │
-- │ principal cannot even see them. See domain.RestrictionEnforced. │
-- └─────────────────────────────────────────────────────────────────┘
--
-- Project scope is not an isolation boundary — the tenant is, and RLS
-- stays that one rule (§14 decision 7) — so project ids are plain uuids
-- with no foreign key into Catalog, as identity_preview_origins keeps
-- them. A vendor is a group of members inside the customer's tenant,
-- not a tenant of its own (§14 decision 6, §15 q3): every table here
-- is tenant-owned under forced RLS, and nothing crosses tenants.

-- ── vendors ─────────────────────────────────────────────────────────

CREATE TABLE identity_vendors (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    contact    text        NOT NULL DEFAULT '' CHECK (char_length(contact) <= 200),
    -- Locales the vendor offers; descriptive, they grant nothing.
    locales    text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(locales) <= 200),
    version    integer     NOT NULL CHECK (version > 0),
    created_by text        NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    -- The target of identity_members' tenant-matching foreign key.
    UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX identity_vendors_name ON identity_vendors (tenant_id, lower(name));

ALTER TABLE identity_vendors ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity_vendors FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_vendors_tenant_isolation ON identity_vendors
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());
GRANT SELECT, INSERT, UPDATE, DELETE ON identity_vendors TO glossa_app;

-- ── members: project scope, vendor, visibility ──────────────────────

ALTER TABLE identity_members
    -- Empty is every project: every membership that exists today.
    ADD COLUMN projects   uuid[] NOT NULL DEFAULT '{}' CHECK (cardinality(projects) <= 100),
    ADD COLUMN vendor_id  uuid,
    ADD COLUMN visibility text   NOT NULL DEFAULT 'all' CHECK (visibility IN ('all', 'assigned')),
    -- A vendor member sees only their assignments (§3.3) …
    ADD CONSTRAINT identity_members_vendor_assigned
        CHECK (vendor_id IS NULL OR visibility = 'assigned'),
    -- … and someone who sees only their assignments is a translator:
    -- they can't review, publish, import or manage anything.
    ADD CONSTRAINT identity_members_assigned_translator
        CHECK (visibility = 'all' OR roles = ARRAY['translator']),
    -- An owner answers for the whole tenant.
    ADD CONSTRAINT identity_members_owner_unscoped
        CHECK (cardinality(projects) = 0 OR NOT ('owner' = ANY (roles))),
    -- The target of identity_group_members' tenant-matching foreign key.
    ADD CONSTRAINT identity_members_tenant_id_key UNIQUE (tenant_id, id),
    -- A member's vendor is in the member's own tenant, and a vendor
    -- with members can't be deleted out from under them.
    ADD CONSTRAINT identity_members_vendor_fkey FOREIGN KEY (tenant_id, vendor_id)
        REFERENCES identity_vendors (tenant_id, id) ON DELETE RESTRICT;
CREATE INDEX identity_members_vendor ON identity_members (tenant_id, vendor_id) WHERE vendor_id IS NOT NULL;

-- ── tokens: project scope ───────────────────────────────────────────

ALTER TABLE identity_api_tokens
    ADD COLUMN projects uuid[] NOT NULL DEFAULT '{}' CHECK (cardinality(projects) <= 100);

-- ── groups ──────────────────────────────────────────────────────────
--
-- A named set of members (§4.3). Groups carry no permissions — there is
-- deliberately no roles column — and exist so that assignments and
-- approvals can name people without naming persons.

CREATE TABLE identity_groups (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name       text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    version    integer     NOT NULL CHECK (version > 0),
    created_by text        NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX identity_groups_name ON identity_groups (tenant_id, lower(name));

ALTER TABLE identity_groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity_groups FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_groups_tenant_isolation ON identity_groups
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());
GRANT SELECT, INSERT, UPDATE, DELETE ON identity_groups TO glossa_app;

-- Removing a member or a group removes the membership row with it, and
-- the composite keys keep a group and its members in one tenant.
CREATE TABLE identity_group_members (
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    group_id   uuid        NOT NULL,
    member_id  uuid        NOT NULL,
    added_by   text        NOT NULL,
    added_at   timestamptz NOT NULL,
    PRIMARY KEY (group_id, member_id),
    FOREIGN KEY (tenant_id, group_id) REFERENCES identity_groups (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, member_id) REFERENCES identity_members (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX identity_group_members_member ON identity_group_members (tenant_id, member_id);

ALTER TABLE identity_group_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity_group_members FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_group_members_tenant_isolation ON identity_group_members
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());
GRANT SELECT, INSERT, DELETE ON identity_group_members TO glossa_app;
