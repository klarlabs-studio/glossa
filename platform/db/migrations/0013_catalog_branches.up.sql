-- 0013 — Catalog: the branch overlay (RFC 0004 §4.1).
--
-- A branch proposes new messages (state 'proposed', owned by the
-- branches that propose them) and changes to live messages' source
-- (source proposals, never source revisions: the append-only log only
-- records merged history). The default branch's push activates both.
--
-- Localization's projection of the catalog carries the message state,
-- so it learns the new one too: proposed messages are translated like
-- any other.

ALTER TABLE catalog_messages DROP CONSTRAINT catalog_messages_state_check;
ALTER TABLE catalog_messages ADD CONSTRAINT catalog_messages_state_check
    CHECK (state IN ('active', 'proposed', 'obsolete'));

ALTER TABLE localization_messages DROP CONSTRAINT localization_messages_state_check;
ALTER TABLE localization_messages ADD CONSTRAINT localization_messages_state_check
    CHECK (state IN ('active', 'proposed', 'obsolete'));

-- ── branches ───────────────────────────────────────────────────────
-- name is the Git branch name, unique per project. closed_at is set
-- exactly when the branch is closed or merged; the sweep obsoletes a
-- closed branch's proposed messages 14 days after it. removed_keys are
-- the live keys the last complete push no longer had (reported only).

CREATE TABLE catalog_branches (
    id           uuid        PRIMARY KEY,
    tenant_id    uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id   uuid        NOT NULL REFERENCES catalog_projects (id) ON DELETE CASCADE,
    name         text        NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 255),
    pr_number    integer     CHECK (pr_number > 0),
    head_commit  text        NOT NULL DEFAULT '' CHECK (head_commit = '' OR head_commit ~ '^[0-9a-f]{7,64}$'),
    state        text        NOT NULL CHECK (state IN ('open', 'merged', 'closed')),
    preview_url  text        NOT NULL DEFAULT '' CHECK (octet_length(preview_url) <= 2000),
    closed_at    timestamptz,
    removed_keys text[]      NOT NULL DEFAULT '{}',
    version      integer     NOT NULL CHECK (version > 0),
    created_by   text        NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    UNIQUE (project_id, name),
    CHECK ((state = 'open') = (closed_at IS NULL))
);
CREATE INDEX catalog_branches_tenant ON catalog_branches (tenant_id);
-- The sweep reads closed and merged branches by when they closed.
CREATE INDEX catalog_branches_closed ON catalog_branches (closed_at) WHERE closed_at IS NOT NULL;

-- ── proposals ──────────────────────────────────────────────────────
-- What one branch says about one key: a new key (the branch owns the
-- key's proposed message) or a source change to a live message against
-- base_revision. The pushed source is kept per branch, so two branches
-- proposing different source for one new key are a key_conflict for
-- both.

CREATE TABLE catalog_proposals (
    tenant_id     uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    branch_id     uuid        NOT NULL REFERENCES catalog_branches (id) ON DELETE CASCADE,
    key           text        NOT NULL CHECK (char_length(key) <= 200
                                              AND key ~ '^[a-z0-9_-]+(\.[a-z0-9_-]+)*$'),
    message_id    uuid        NOT NULL REFERENCES catalog_messages (id) ON DELETE CASCADE,
    kind          text        NOT NULL CHECK (kind IN ('new_key', 'source_change')),
    syntax        text        NOT NULL CHECK (syntax IN ('mf1', 'mf2')),
    text          text        NOT NULL CHECK (octet_length(text) <= 20000),
    model         jsonb       NOT NULL CHECK (jsonb_typeof(model) = 'object'),
    base_revision integer     CHECK (base_revision > 0),
    author        text        NOT NULL,
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    PRIMARY KEY (branch_id, key),
    CHECK ((kind = 'source_change') = (base_revision IS NOT NULL))
);
CREATE INDEX catalog_proposals_tenant ON catalog_proposals (tenant_id);
-- Every branch's proposal for a message: conflicts, activation, sweep.
CREATE INDEX catalog_proposals_message ON catalog_proposals (message_id);

-- ── row-level security ─────────────────────────────────────────────

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['catalog_branches', 'catalog_proposals']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I USING (tenant_id = app_current_tenant())'
                       ' WITH CHECK (tenant_id = app_current_tenant())', t || '_tenant_isolation', t);
    END LOOP;
END
$$;

-- Branches are history: closed and merged, never deleted by the app (a
-- project's deletion cascades as the owner).
GRANT SELECT, INSERT, UPDATE ON catalog_branches TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON catalog_proposals TO glossa_app;
