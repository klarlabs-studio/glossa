-- 0022 — Identity: preview origins and in-context grants (RFC 0004 §5.2).
--
-- The in-product editor runs on a page the product serves, not on
-- Studio's, so it cannot use Studio's session cookie. Instead a popup on
-- Studio mints a short-lived bearer token for it. Two tables carry that:
--
--   identity_preview_origins — the origins a project's editor may run
--   on. Registering one is the decision that a person's permissions may
--   be borrowed by a page served from there, so it is listed, explicit
--   and revocable. Tenant-owned, but read in system scope too: a CORS
--   preflight carries no credentials, so the server has to answer "is
--   this origin registered anywhere?" before any tenant is known.
--
--   identity_in_context_grants — the minted grants. Like API tokens,
--   only the SHA-256 of the secret is stored and the row is resolved in
--   system scope, because the tenant follows from the grant. Unlike API
--   tokens the actor is a person, the permissions are that person's
--   grant cut down to the editor's ceiling (locale scopes and all), and
--   the row is bound to one project and one origin. They expire in 15
--   minutes and are swept, never revoked: there is nothing long-lived
--   to manage.

-- ── preview origins (tenant-owned) ─────────────────────────────────

CREATE TABLE identity_preview_origins (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    -- Catalog owns projects; Identity keeps the id and nothing else, so
    -- there is no cross-context foreign key (as intelligence_project_settings).
    project_id uuid        NOT NULL,
    -- Canonical scheme://host[:port], lower-case, default port dropped;
    -- domain.ParseOrigin is the authority, this is the coarse guard.
    -- https everywhere, http on loopback only (development).
    origin     text        NOT NULL
                           CHECK (origin = lower(origin)
                                  AND char_length(origin) BETWEEN 8 AND 255
                                  AND (origin ~ '^https://[a-z0-9.:\[\]-]+$'
                                       OR origin ~ '^http://(localhost|127\.0\.0\.1|\[::1\]|[a-z0-9.-]+\.localhost)(:[0-9]{1,5})?$')),
    label      text        NOT NULL DEFAULT '' CHECK (char_length(label) <= 100),
    created_by text        NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (project_id, origin)
);
CREATE INDEX identity_preview_origins_tenant ON identity_preview_origins (tenant_id, project_id);
CREATE INDEX identity_preview_origins_origin ON identity_preview_origins (origin);

-- ── in-context grants (tenant-owned) ───────────────────────────────

CREATE TABLE identity_in_context_grants (
    id           uuid        PRIMARY KEY,
    tenant_id    uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id   uuid        NOT NULL,
    person_id    uuid        NOT NULL REFERENCES identity_people (id) ON DELETE CASCADE,
    -- SHA-256 of the whole glossa_ctx_… secret; the secret is never stored.
    token_hash   text        NOT NULL UNIQUE CHECK (char_length(token_hash) = 64),
    origin       text        NOT NULL CHECK (char_length(origin) BETWEEN 8 AND 255),
    -- The person's grant ∩ the editor's ceiling, as
    -- {"translations.write": ["de"], "catalog.read": []}; an empty array
    -- means every locale.
    permissions  jsonb       NOT NULL CHECK (jsonb_typeof(permissions) = 'object'),
    created_at   timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL CHECK (expires_at > created_at),
    last_used_at timestamptz
);
CREATE INDEX identity_in_context_grants_tenant ON identity_in_context_grants (tenant_id, project_id);
CREATE INDEX identity_in_context_grants_expiry ON identity_in_context_grants (expires_at);
-- Removing a preview origin ends the sessions minted for it, so
-- revoking access is immediate and not "within fifteen minutes".
CREATE INDEX identity_in_context_grants_origin ON identity_in_context_grants (project_id, origin);

-- ── row-level security ─────────────────────────────────────────────

ALTER TABLE identity_preview_origins ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity_preview_origins FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_preview_origins_tenant_isolation ON identity_preview_origins
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());
GRANT SELECT, INSERT, DELETE ON identity_preview_origins TO glossa_app;

ALTER TABLE identity_in_context_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity_in_context_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_in_context_grants_tenant_isolation ON identity_in_context_grants
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());
GRANT SELECT, INSERT, DELETE ON identity_in_context_grants TO glossa_app;

-- Pre-tenant lookups (system scope).
--   preview origins: a CORS preflight is answered before any credential
--   is seen, so the server checks the origin against every project's
--   registered origins. It reads the origin column and nothing else.
CREATE POLICY identity_preview_origins_system_select ON identity_preview_origins
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (id, tenant_id, project_id, origin) ON identity_preview_origins TO glossa_system;

--   grants: which tenant, project, person and origin a bearer grant
--   belongs to, and what it allows. Using one bumps last_used_at, and
--   nothing else; the sweep drops expired rows.
CREATE POLICY identity_in_context_grants_system_select ON identity_in_context_grants
    FOR SELECT TO glossa_system USING (true);
CREATE POLICY identity_in_context_grants_system_touch ON identity_in_context_grants
    FOR UPDATE TO glossa_system USING (true) WITH CHECK (true);
CREATE POLICY identity_in_context_grants_system_sweep ON identity_in_context_grants
    FOR DELETE TO glossa_system USING (true);
GRANT SELECT (id, tenant_id, project_id, person_id, token_hash, origin, permissions,
              created_at, expires_at, last_used_at)
    ON identity_in_context_grants TO glossa_system;
GRANT UPDATE (last_used_at) ON identity_in_context_grants TO glossa_system;
GRANT DELETE ON identity_in_context_grants TO glossa_system;
