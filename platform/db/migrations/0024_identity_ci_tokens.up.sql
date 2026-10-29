-- 0024 — CI authenticates with GitHub Actions OIDC (RFC 0004 §6.3).
--
-- A CI job holds no Glossa secret. It asks GitHub for an OIDC ID token
-- with audience 'glossa' and exchanges it at
-- POST /v1/auth/github-oidc-exchanges for a token of Glossa's own. Two
-- things carry that:
--
--   identity_ci_tokens — the minted tokens. Like API tokens, only the
--   SHA-256 of the secret is stored and the row is resolved in system
--   scope, because the tenant follows from the credential. Unlike API
--   tokens there is no person and no creator: the actor is the workflow
--   run, whose repository, ref, workflow file and run id are kept beside
--   the token as its audit trail. They are bound to one project, allow
--   exactly catalog.read and catalog.write, expire in 30 minutes and are
--   swept, never revoked — there is nothing long-lived to manage.
--
--   a system-scope read of integration_git_connections. The exchange has
--   no tenant when it starts: which tenant a run belongs to is precisely
--   what the ID token's repository_id proves, and that mapping lives in
--   Integration's connections. So the lookup happens as glossa_system,
--   before any tenant is known, exactly as the webhook inbox resolves an
--   installation to its tenant (0019).
--
-- The repository is matched on the numeric id and never on the name.
-- A name can be renamed, deleted and registered by a stranger; the
-- number cannot be taken over, so a policy keyed on it cannot be
-- inherited.

-- ── CI tokens (tenant-owned) ───────────────────────────────────────

CREATE TABLE identity_ci_tokens (
    id                  uuid        PRIMARY KEY,
    tenant_id           uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    -- Catalog owns projects; Identity keeps the id and nothing else, so
    -- there is no cross-context foreign key (as identity_in_context_grants).
    project_id          uuid        NOT NULL,
    -- SHA-256 of the whole glossa_ci_… secret; the secret is never stored.
    token_hash          text        NOT NULL UNIQUE CHECK (char_length(token_hash) = 64),
    -- The CI ceiling as {"catalog.read": [], "catalog.write": []}; an
    -- empty array means every locale, and a CI token is never
    -- locale-scoped. Stored rather than derived so a later change to the
    -- ceiling cannot widen a token already in a running job's memory.
    permissions         jsonb       NOT NULL CHECK (jsonb_typeof(permissions) = 'object'),

    -- The workflow run that presented the ID token: who acted, since no
    -- person did. Every column comes from claims GitHub signed.
    repository_id       bigint      NOT NULL CHECK (repository_id > 0),
    repository_owner_id bigint      NOT NULL DEFAULT 0 CHECK (repository_owner_id >= 0),
    -- 'owner/name' when the token was issued. A label for the operator;
    -- nothing is ever keyed on it.
    repository          text        NOT NULL DEFAULT '' CHECK (char_length(repository) <= 255),
    git_ref             text        NOT NULL DEFAULT '' CHECK (char_length(git_ref) <= 255),
    commit_sha          text        NOT NULL DEFAULT '' CHECK (char_length(commit_sha) <= 64),
    event_name          text        NOT NULL DEFAULT '' CHECK (char_length(event_name) <= 64),
    -- 'acme/shop/.github/workflows/ci.yml@refs/heads/main'.
    workflow_ref        text        NOT NULL DEFAULT '' CHECK (char_length(workflow_ref) <= 512),
    run_id              text        NOT NULL DEFAULT '' CHECK (char_length(run_id) <= 64),
    runner_environment  text        NOT NULL DEFAULT '' CHECK (char_length(runner_environment) <= 64),

    created_at          timestamptz NOT NULL,
    expires_at          timestamptz NOT NULL CHECK (expires_at > created_at),
    last_used_at        timestamptz
);
CREATE INDEX identity_ci_tokens_tenant ON identity_ci_tokens (tenant_id, project_id);
CREATE INDEX identity_ci_tokens_expiry ON identity_ci_tokens (expires_at);
-- "Which runs of this repository authenticated?", for an operator
-- reading an audit trail.
CREATE INDEX identity_ci_tokens_repository ON identity_ci_tokens (repository_id, created_at);

-- ── row-level security ─────────────────────────────────────────────

ALTER TABLE identity_ci_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity_ci_tokens FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_ci_tokens_tenant_isolation ON identity_ci_tokens
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());
GRANT SELECT, INSERT, DELETE ON identity_ci_tokens TO glossa_app;

-- Pre-tenant lookup (system scope): which tenant, project and
-- permissions a bearer CI token carries. Using one bumps last_used_at,
-- and nothing else; the sweep drops expired rows.
CREATE POLICY identity_ci_tokens_system_select ON identity_ci_tokens
    FOR SELECT TO glossa_system USING (true);
CREATE POLICY identity_ci_tokens_system_touch ON identity_ci_tokens
    FOR UPDATE TO glossa_system USING (true) WITH CHECK (true);
CREATE POLICY identity_ci_tokens_system_sweep ON identity_ci_tokens
    FOR DELETE TO glossa_system USING (true);
GRANT SELECT (id, tenant_id, project_id, token_hash, permissions, expires_at, last_used_at)
    ON identity_ci_tokens TO glossa_system;
GRANT UPDATE (last_used_at) ON identity_ci_tokens TO glossa_system;
GRANT DELETE ON identity_ci_tokens TO glossa_system;

-- ── resolving a repository before any tenant is known ──────────────

-- The exchange arrives with no tenant, like a webhook delivery: which
-- tenant a GitHub Actions run belongs to is what its repository_id
-- proves. The lookup reads the mapping and nothing else — no
-- repository_name, no created_by — so a verified run learns which of
-- its own projects it may act on and nothing about anyone else's.
CREATE POLICY integration_git_connections_system_select ON integration_git_connections
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (tenant_id, repository_id, project_id, application_id, path)
    ON integration_git_connections TO glossa_system;
