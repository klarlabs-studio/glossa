-- 0005 — Release: environments, immutable releases, the pointer history
-- of every environment, and publishable delivery keys.
--
-- Every table is tenant-owned under the standard isolation policy and
-- keyed by Catalog's project ID without a foreign key (contexts never
-- touch each other's tables, RFC 0002 §4).
--
-- Releases are immutable (intent §36): glossa_app may only SELECT and
-- INSERT them, and a trigger refuses UPDATE and DELETE for every role,
-- the owner included. The one exception is a row deleted by the
-- cascade from its tenant's deletion (erasure), which runs inside the
-- foreign key's own trigger. release_deployments is append-only by
-- grant, like the other history logs.

-- ── releases ───────────────────────────────────────────────────────

CREATE TABLE release_releases (
    id              uuid        PRIMARY KEY,
    tenant_id       uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id      uuid        NOT NULL,
    version         integer     NOT NULL CHECK (version > 0),
    parent_id       uuid        REFERENCES release_releases (id),
    -- Where it was published, and the eligibility policy it was built
    -- under ({"states": [...], "include_outdated": bool}).
    environment     text        NOT NULL CHECK (environment ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    policy          jsonb       NOT NULL CHECK (jsonb_typeof(policy) = 'object'),
    -- The manifest body every environment's manifest carries
    -- (sourceLocale, locales, fallback, artifacts), and the SHA-256 of
    -- its RFC 8785 canonical form.
    content         jsonb       NOT NULL CHECK (jsonb_typeof(content) = 'object'),
    manifest_digest text        NOT NULL CHECK (manifest_digest ~ '^[0-9a-f]{64}$'),
    stats           jsonb       NOT NULL CHECK (jsonb_typeof(stats) = 'object'),
    note            text        NOT NULL DEFAULT '' CHECK (char_length(note) <= 1000),
    created_by      text        NOT NULL,
    created_at      timestamptz NOT NULL,
    UNIQUE (project_id, version)
);
CREATE INDEX release_releases_tenant ON release_releases (tenant_id);

CREATE FUNCTION release_releases_immutable() RETURNS trigger
    LANGUAGE plpgsql AS $$
BEGIN
    -- Depth 1 is this trigger on a direct statement. A cascade from the
    -- tenant's deletion runs inside the foreign key's trigger (depth > 1).
    IF TG_OP = 'DELETE' AND pg_trigger_depth() > 1 THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'releases are immutable: % on release_releases is not allowed', TG_OP
        USING ERRCODE = 'integrity_constraint_violation';
END
$$;

CREATE TRIGGER release_releases_immutable
    BEFORE UPDATE OR DELETE ON release_releases
    FOR EACH ROW EXECUTE FUNCTION release_releases_immutable();

-- ── environments ───────────────────────────────────────────────────

CREATE TABLE release_environments (
    tenant_id          uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id         uuid        NOT NULL,
    name               text        NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,62}$' AND name <> 'a'),
    policy             jsonb       NOT NULL CHECK (jsonb_typeof(policy) = 'object'),
    current_release_id uuid        REFERENCES release_releases (id),
    version            integer     NOT NULL CHECK (version > 0),
    created_by         text        NOT NULL,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,
    PRIMARY KEY (project_id, name)
);
CREATE INDEX release_environments_tenant ON release_environments (tenant_id);

-- Every pointer move, forever: what rollback walks back through.
CREATE TABLE release_deployments (
    tenant_id           uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id          uuid        NOT NULL,
    environment         text        NOT NULL,
    number              integer     NOT NULL CHECK (number > 0),
    release_id          uuid        NOT NULL REFERENCES release_releases (id),
    previous_release_id uuid        REFERENCES release_releases (id),
    action              text        NOT NULL CHECK (action IN ('publish', 'promote', 'rollback')),
    created_by          text        NOT NULL,
    created_at          timestamptz NOT NULL,
    PRIMARY KEY (project_id, environment, number)
);
CREATE INDEX release_deployments_tenant ON release_deployments (tenant_id);
CREATE INDEX release_deployments_release ON release_deployments (release_id);

-- ── delivery keys ──────────────────────────────────────────────────
-- Publishable by design (runtimes/SPEC.md §2): stored in the clear so
-- they can be listed again. Revoked keys stay listed.

CREATE TABLE release_delivery_keys (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id uuid        NOT NULL,
    key        text        NOT NULL UNIQUE CHECK (key ~ '^glossa_pk_[A-Za-z0-9_-]{32}$'),
    name       text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    created_by text        NOT NULL,
    created_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revoked_by text,
    CHECK ((revoked_at IS NULL) = (revoked_by IS NULL))
);
CREATE INDEX release_delivery_keys_project ON release_delivery_keys (project_id, id);
CREATE INDEX release_delivery_keys_tenant ON release_delivery_keys (tenant_id);

-- ── row-level security ─────────────────────────────────────────────

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['release_releases', 'release_environments', 'release_deployments',
                             'release_delivery_keys']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I USING (tenant_id = app_current_tenant())'
                       ' WITH CHECK (tenant_id = app_current_tenant())', t || '_tenant_isolation', t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT ON release_releases TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON release_environments TO glossa_app;
GRANT SELECT, INSERT ON release_deployments TO glossa_app;
GRANT SELECT, INSERT, UPDATE ON release_delivery_keys TO glossa_app;
