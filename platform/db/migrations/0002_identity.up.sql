-- 0002 — Identity: people, credentials, sessions, memberships, API tokens.
--
-- Two kinds of tables (RFC 0002 §4, Klarlabs standard §2–3):
--
--   Global — a person and their credentials exist before any tenant is
--   known (a sign-in) and span every tenant the person belongs to, so
--   they have no tenant_id. They still ENABLE + FORCE row-level security,
--   are granted only to glossa_system, and are reached only through the
--   kernel's system-scope unit of work. A request running as glossa_app
--   in tenant scope can't read or write them — with one exception: the
--   names and emails of people who are members of the current tenant
--   (identity_people_tenant_members), so member lists can show them.
--
--   Tenant-owned — memberships and API tokens carry tenant_id under the
--   standard isolation policy. Two lookups have to happen before the
--   tenant is known, so glossa_system gets narrow SELECT policies on
--   them: a session's memberships (to validate /v1/tenants/{tenant}) and
--   a token by its hash (to learn its tenant). Token resolution may also
--   bump last_used_at, and nothing else.
--
-- Every system policy is listed, with its reason, in the RLS guard test.

-- ── people (global) ────────────────────────────────────────────────

CREATE TABLE identity_people (
    id                   uuid        PRIMARY KEY,
    email                text        NOT NULL UNIQUE
                                     CHECK (email = lower(email) AND char_length(email) BETWEEN 3 AND 254),
    display_name         text        NOT NULL DEFAULT '' CHECK (char_length(display_name) <= 200),
    -- Reserved at registration; the tenant row is created (and, after a
    -- crash, re-created) in its own tenant-scoped transaction.
    individual_tenant_id uuid        NOT NULL UNIQUE,
    password_hash        text,       -- argon2id PHC string (auth-go)
    email_verified_at    timestamptz,
    created_at           timestamptz NOT NULL,
    updated_at           timestamptz NOT NULL
);

-- ── credentials and sessions (global, auth-go ports) ───────────────

-- Only the SHA-256 of the session cookie is stored (auth-go).
CREATE TABLE identity_sessions (
    token_hash text        PRIMARY KEY CHECK (char_length(token_hash) = 64),
    person_id  uuid        NOT NULL REFERENCES identity_people (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX identity_sessions_person ON identity_sessions (person_id);
CREATE INDEX identity_sessions_expiry ON identity_sessions (expires_at);

-- Single-use emailed links: sign-in (which also verifies the address)
-- and password reset. Only the SHA-256 of the token is stored.
CREATE TABLE identity_email_links (
    hash       text        PRIMARY KEY CHECK (char_length(hash) = 64),
    purpose    text        NOT NULL CHECK (purpose IN ('sign_in', 'password_reset')),
    email      text        NOT NULL CHECK (email = lower(email)),
    expires_at timestamptz NOT NULL,
    consumed   boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX identity_email_links_outstanding ON identity_email_links (email, purpose)
    WHERE NOT consumed;

-- One TOTP secret per person, AES-256-GCM sealed (auth-go aesgcm).
-- Pending until confirmed with a code; last_step makes codes single-use.
CREATE TABLE identity_totp (
    person_id         uuid        PRIMARY KEY REFERENCES identity_people (id) ON DELETE CASCADE,
    secret_ciphertext text        NOT NULL,
    confirmed_at      timestamptz,
    last_step         bigint,
    updated_at        timestamptz NOT NULL
);

CREATE TABLE identity_passkeys (
    credential_id bytea       PRIMARY KEY,
    person_id     uuid        NOT NULL REFERENCES identity_people (id) ON DELETE CASCADE,
    public_key    bytea       NOT NULL,
    sign_count    bigint      NOT NULL DEFAULT 0 CHECK (sign_count BETWEEN 0 AND 4294967295),
    name          text        NOT NULL DEFAULT '' CHECK (char_length(name) <= 100),
    created_at    timestamptz NOT NULL,
    last_used_at  timestamptz
);
CREATE INDEX identity_passkeys_person ON identity_passkeys (person_id);

-- In-flight WebAuthn ceremonies (auth-go's signed state), single-use and
-- short-lived. The browser holds only a random key in an HttpOnly
-- cookie; its SHA-256 is the primary key. Server-side so an intercepted
-- assertion can't be replayed against the same challenge.
CREATE TABLE identity_webauthn_ceremonies (
    key_hash   text        PRIMARY KEY CHECK (char_length(key_hash) = 64),
    purpose    text        NOT NULL CHECK (purpose IN ('sign_in', 'registration')),
    person_id  uuid        REFERENCES identity_people (id) ON DELETE CASCADE,
    state      bytea       NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX identity_webauthn_ceremonies_expiry ON identity_webauthn_ceremonies (expires_at);

-- Brute-force lockout counters, keyed by auth-go's LockoutKeyFromEmail
-- (a SHA-256 of the address, so no plaintext email is kept here).
CREATE TABLE identity_login_attempts (
    key           text        PRIMARY KEY CHECK (char_length(key) <= 255),
    failure_count integer     NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    locked_until  timestamptz,
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- ── memberships (tenant-owned) ─────────────────────────────────────

CREATE TABLE identity_members (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    -- NULL while the invitation is open.
    person_id  uuid        REFERENCES identity_people (id) ON DELETE CASCADE,
    email      text        NOT NULL CHECK (email = lower(email) AND char_length(email) BETWEEN 3 AND 254),
    roles      text[]      NOT NULL
                           CHECK (cardinality(roles) > 0
                                  AND roles <@ ARRAY['owner', 'admin', 'developer', 'translator', 'reviewer']),
    locales    text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(locales) <= 200),
    status     text        NOT NULL CHECK (status IN ('invited', 'active')),
    version    integer     NOT NULL CHECK (version > 0),
    created_by text        NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CHECK ((status = 'active') = (person_id IS NOT NULL)),
    UNIQUE (tenant_id, email),
    UNIQUE (tenant_id, person_id)
);
CREATE INDEX identity_members_person ON identity_members (person_id) WHERE person_id IS NOT NULL;
CREATE INDEX identity_members_invitations ON identity_members (email) WHERE status = 'invited';

-- ── API tokens (tenant-owned) ──────────────────────────────────────

CREATE TABLE identity_api_tokens (
    id           uuid        PRIMARY KEY,
    tenant_id    uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name         text        NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    -- SHA-256 of the whole glossa_api_… secret; the secret is never stored.
    token_hash   text        NOT NULL UNIQUE CHECK (char_length(token_hash) = 64),
    hint         text        NOT NULL,
    scopes       text[]      NOT NULL
                             CHECK (cardinality(scopes) > 0
                                    AND scopes <@ ARRAY['read', 'write', 'publish', 'admin']),
    created_by   text        NOT NULL,
    created_at   timestamptz NOT NULL,
    expires_at   timestamptz,
    last_used_at timestamptz,
    revoked_at   timestamptz,
    revoked_by   text,
    CHECK ((revoked_at IS NULL) = (revoked_by IS NULL))
);
CREATE INDEX identity_api_tokens_tenant ON identity_api_tokens (tenant_id, id);

-- ── row-level security ─────────────────────────────────────────────

-- Global tables: system scope only.
DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['identity_people', 'identity_sessions', 'identity_email_links',
                             'identity_totp', 'identity_passkeys', 'identity_webauthn_ceremonies',
                             'identity_login_attempts']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I TO glossa_system USING (true) WITH CHECK (true)',
                       t || '_system', t);
    END LOOP;
END
$$;

GRANT SELECT, INSERT, UPDATE ON identity_people TO glossa_system;
GRANT SELECT, INSERT, DELETE ON identity_sessions TO glossa_system;
GRANT SELECT, INSERT, UPDATE ON identity_email_links TO glossa_system;
GRANT SELECT, INSERT, UPDATE, DELETE ON identity_totp TO glossa_system;
GRANT SELECT, INSERT, UPDATE, DELETE ON identity_passkeys TO glossa_system;
GRANT SELECT, INSERT, DELETE ON identity_webauthn_ceremonies TO glossa_system;
GRANT SELECT, INSERT, UPDATE, DELETE ON identity_login_attempts TO glossa_system;

-- In tenant scope, a person's name and email are visible to the tenants
-- they are a member of, and nothing else about them is.
CREATE POLICY identity_people_tenant_members ON identity_people
    FOR SELECT
    USING (EXISTS (SELECT FROM identity_members m
                   WHERE m.person_id = identity_people.id
                     AND m.tenant_id = app_current_tenant()));
GRANT SELECT (id, email, display_name) ON identity_people TO glossa_app;

-- Tenant-owned tables: the standard isolation policy.
ALTER TABLE identity_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity_members FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_members_tenant_isolation ON identity_members
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());
GRANT SELECT, INSERT, UPDATE, DELETE ON identity_members TO glossa_app;

ALTER TABLE identity_api_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity_api_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_api_tokens_tenant_isolation ON identity_api_tokens
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());
GRANT SELECT, INSERT, UPDATE ON identity_api_tokens TO glossa_app;

-- Pre-tenant lookups (system scope).
--   memberships: which tenants a signed-in person may act in, and with
--   what roles; open invitations addressed to their verified email.
CREATE POLICY identity_members_system_select ON identity_members
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (id, tenant_id, person_id, email, roles, locales, status) ON identity_members TO glossa_system;

--   tokens: which tenant a bearer token belongs to, and its scopes.
CREATE POLICY identity_api_tokens_system_select ON identity_api_tokens
    FOR SELECT TO glossa_system USING (true);
CREATE POLICY identity_api_tokens_system_touch ON identity_api_tokens
    FOR UPDATE TO glossa_system USING (true) WITH CHECK (true);
GRANT SELECT (id, tenant_id, token_hash, scopes, expires_at, revoked_at, last_used_at)
    ON identity_api_tokens TO glossa_system;
GRANT UPDATE (last_used_at) ON identity_api_tokens TO glossa_system;

--   tenants: names and slugs for a person's tenant list (GET /v1/me).
CREATE POLICY tenants_system_select ON tenants
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT ON tenants TO glossa_system;
