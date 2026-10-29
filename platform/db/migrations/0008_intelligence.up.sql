-- 0008 — Intelligence (RFC 0003 §3, §7): tenant-owned AI provider
-- configuration (keys sealed), routing, prices, budgets with a spend
-- ledger, privacy settings, the job queue, suggestions and the
-- disclosure ledger.
--
-- Every table is tenant-owned under the standard isolation policy. None
-- references another context's tables: Intelligence learns about
-- messages, translations and locales from events and application ports.
--
-- The job workers claim across tenants, as the outbox relay does: the
-- system scope "intelligence.jobs" may SELECT and UPDATE
-- intelligence_jobs and SELECT intelligence_settings (the per-tenant
-- concurrency cap) — nothing else, and never insert or delete. Each job
-- then runs in its own tenant's scope.

-- ── configuration ──────────────────────────────────────────────────

-- A provider a tenant configured, with its own credentials. name is
-- what routing policies refer to ("anthropic", "mistral-eu"). The API
-- key is sealed (AES-256-GCM bound to tenant and provider id) and never
-- leaves the server.
CREATE TABLE intelligence_providers (
    id             uuid        PRIMARY KEY,
    tenant_id      uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    name           text        NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,62}$'),
    kind           text        NOT NULL CHECK (kind IN ('anthropic', 'openai_compatible', 'gemini')),
    base_url       text        NOT NULL DEFAULT '' CHECK (char_length(base_url) <= 500),
    -- Model allow-list; empty allows any model.
    models         text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(models) <= 50),
    enabled        boolean     NOT NULL DEFAULT true,
    api_key_sealed bytea       CHECK (octet_length(api_key_sealed) <= 4096),
    version        integer     NOT NULL CHECK (version > 0),
    created_by     text        NOT NULL,
    created_at     timestamptz NOT NULL,
    updated_by     text        NOT NULL,
    updated_at     timestamptz NOT NULL,
    UNIQUE (tenant_id, name)
);

-- Tenant-wide AI settings: consent to send text to providers (off until
-- someone turns it on, and who and when is kept), the job concurrency
-- cap and the monthly budget. No row means the defaults.
CREATE TABLE intelligence_settings (
    tenant_id                uuid        PRIMARY KEY REFERENCES tenants (id) ON DELETE CASCADE,
    provider_consent         boolean     NOT NULL DEFAULT false,
    consent_changed_by       text,
    consent_changed_at       timestamptz,
    max_concurrent_jobs      integer     NOT NULL DEFAULT 4 CHECK (max_concurrent_jobs BETWEEN 1 AND 64),
    -- 0 allows no spending at all: provider calls stop (hard stop).
    monthly_budget_micro_usd bigint      NOT NULL DEFAULT 0 CHECK (monthly_budget_micro_usd >= 0),
    -- Price overrides: {"<provider>/<model>": {input_per_mtok, …}}.
    prices                   jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(prices) = 'object'),
    version                  integer     NOT NULL CHECK (version > 0),
    updated_by               text        NOT NULL,
    updated_at               timestamptz NOT NULL
);

-- Per-project settings: namespace policy tags (sensitive, legal,
-- marketing), the locales auto-translate is on for, and review routing.
CREATE TABLE intelligence_project_settings (
    project_id             uuid        PRIMARY KEY,
    tenant_id              uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    namespace_tags         jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(namespace_tags) = 'object'),
    auto_translate_locales text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(auto_translate_locales) <= 200),
    review_policy          jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(review_policy) = 'object'),
    version                integer     NOT NULL CHECK (version > 0),
    updated_by             text        NOT NULL,
    updated_at             timestamptz NOT NULL
);
CREATE INDEX intelligence_project_settings_tenant ON intelligence_project_settings (tenant_id);

-- Routing policies: the tenant's (project_id NULL) and projects'.
CREATE TABLE intelligence_routing_policies (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id uuid,
    policy     jsonb       NOT NULL CHECK (jsonb_typeof(policy) = 'object'),
    version    integer     NOT NULL CHECK (version > 0),
    updated_by text        NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX intelligence_routing_policies_scope
    ON intelligence_routing_policies (tenant_id, project_id) NULLS NOT DISTINCT;

-- ── money ──────────────────────────────────────────────────────────

-- Every priced model call, append-only: the budget is the sum of the
-- current month's rows (UTC), so nothing is reset.
CREATE TABLE intelligence_spend (
    id                 uuid        PRIMARY KEY,
    tenant_id          uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    job_id             uuid,
    project_id         uuid,
    task               text        NOT NULL CHECK (char_length(task) BETWEEN 1 AND 40),
    provider           text        NOT NULL CHECK (char_length(provider) BETWEEN 1 AND 63),
    model              text        NOT NULL CHECK (char_length(model) BETWEEN 1 AND 200),
    input_tokens       bigint      NOT NULL CHECK (input_tokens >= 0),
    output_tokens      bigint      NOT NULL CHECK (output_tokens >= 0),
    cache_read_tokens  bigint      NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    cache_write_tokens bigint      NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0),
    cost_micro_usd     bigint      NOT NULL CHECK (cost_micro_usd >= 0),
    priced             boolean     NOT NULL,
    occurred_at        timestamptz NOT NULL
);
CREATE INDEX intelligence_spend_tenant_time ON intelligence_spend (tenant_id, occurred_at, id);

-- ── jobs ───────────────────────────────────────────────────────────

-- A fill request: many jobs, one per message and locale.
CREATE TABLE intelligence_fills (
    id            uuid        PRIMARY KEY,
    tenant_id     uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id    uuid        NOT NULL,
    trigger       text        NOT NULL CHECK (trigger IN ('fill', 'locale_added')),
    locales       text[]      NOT NULL CHECK (cardinality(locales) BETWEEN 1 AND 20),
    filter        jsonb       NOT NULL DEFAULT '{}'::jsonb,
    jobs_created  integer     NOT NULL DEFAULT 0,
    jobs_existing integer     NOT NULL DEFAULT 0,
    skipped       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    requested_by  text        NOT NULL,
    created_at    timestamptz NOT NULL
);
CREATE INDEX intelligence_fills_project ON intelligence_fills (tenant_id, project_id, created_at);

-- The queue. state: queued → running → succeeded | skipped | failed |
-- dead | cancelled; running jobs whose lease (available_at) passed are
-- claimed again. At most one job per (message, locale, source revision,
-- knowledge fingerprint): duplicate events find the existing one.
CREATE TABLE intelligence_jobs (
    id                    uuid        PRIMARY KEY,
    tenant_id             uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id            uuid        NOT NULL,
    message_id            uuid        NOT NULL,
    message_key           text        NOT NULL CHECK (char_length(message_key) BETWEEN 1 AND 200),
    namespace             text        NOT NULL CHECK (char_length(namespace) BETWEEN 1 AND 64),
    locale                text        NOT NULL CHECK (char_length(locale) BETWEEN 2 AND 35),
    source_revision       integer     NOT NULL CHECK (source_revision > 0),
    knowledge_fingerprint text        NOT NULL CHECK (char_length(knowledge_fingerprint) = 64),
    trigger               text        NOT NULL CHECK (trigger IN ('message_created', 'translation_outdated', 'locale_added', 'fill')),
    fill_id               uuid,
    state                 text        NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'skipped', 'failed', 'dead', 'cancelled')),
    attempts              integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts          integer     NOT NULL CHECK (max_attempts BETWEEN 1 AND 20),
    available_at          timestamptz NOT NULL,
    claim_token           uuid,
    failure_code          text,
    last_error            text        CHECK (char_length(last_error) <= 4000),
    suggestion_id         uuid,
    -- The agent's ledger: every tool result in order (RFC 0003 §7: what a
    -- provider received is exactly what the tools returned).
    audit                 jsonb,
    created_by            text        NOT NULL,
    created_at            timestamptz NOT NULL,
    started_at            timestamptz,
    finished_at           timestamptz,
    updated_at            timestamptz NOT NULL
);
CREATE UNIQUE INDEX intelligence_jobs_idempotency
    ON intelligence_jobs (tenant_id, message_id, locale, source_revision, knowledge_fingerprint);
CREATE INDEX intelligence_jobs_claimable ON intelligence_jobs (available_at, id)
    WHERE state IN ('queued', 'running');
CREATE INDEX intelligence_jobs_running ON intelligence_jobs (tenant_id) WHERE state = 'running';
CREATE INDEX intelligence_jobs_project ON intelligence_jobs (tenant_id, project_id, created_at DESC, id);
CREATE INDEX intelligence_jobs_fill ON intelligence_jobs (fill_id) WHERE fill_id IS NOT NULL;

-- ── suggestions ────────────────────────────────────────────────────

-- A job's result: a structurally valid translation with provenance,
-- confidence and its routed action. pending ones form the review queue
-- (ordered by score, then risk); accepting writes a translation
-- revision; auto_applied ones were approved by policy.
CREATE TABLE intelligence_suggestions (
    id                   uuid        PRIMARY KEY,
    tenant_id            uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    job_id               uuid        NOT NULL,
    project_id           uuid        NOT NULL,
    message_id           uuid        NOT NULL,
    message_key          text        NOT NULL,
    namespace            text        NOT NULL,
    locale               text        NOT NULL,
    source_revision      integer     NOT NULL CHECK (source_revision > 0),
    message_mf2          text        NOT NULL CHECK (octet_length(message_mf2) <= 100000),
    model                jsonb       NOT NULL CHECK (jsonb_typeof(model) = 'object'),
    findings             jsonb       NOT NULL DEFAULT '[]'::jsonb,
    term_findings        jsonb       NOT NULL DEFAULT '[]'::jsonb,
    provenance           jsonb       NOT NULL CHECK (jsonb_typeof(provenance) = 'object'),
    origin               text        NOT NULL CHECK (origin IN ('ai', 'translation_memory')),
    score                double precision NOT NULL CHECK (score >= 0 AND score <= 1),
    explanation          jsonb       NOT NULL DEFAULT '[]'::jsonb,
    action               text        NOT NULL CHECK (action IN ('auto_approve', 'approve_recommended', 'review_required')),
    -- Why the routed action differs from the policy's (an auto_approve
    -- the environments don't allow), if it does.
    action_note          text,
    risk_tags            text[]      NOT NULL DEFAULT '{}',
    calls                jsonb       NOT NULL DEFAULT '[]'::jsonb,
    usage                jsonb       NOT NULL DEFAULT '{}'::jsonb,
    cost_micro_usd       bigint      NOT NULL DEFAULT 0 CHECK (cost_micro_usd >= 0),
    status               text        NOT NULL CHECK (status IN ('pending', 'accepted', 'rejected', 'auto_applied', 'superseded')),
    translation_revision integer,
    decided_by           text,
    decided_at           timestamptz,
    -- accept/edit/reject details: the edit's structured diff, a reason.
    decision             jsonb,
    version              integer     NOT NULL CHECK (version > 0),
    created_at           timestamptz NOT NULL
);
CREATE INDEX intelligence_suggestions_queue
    ON intelligence_suggestions (tenant_id, project_id, score, cardinality(risk_tags) DESC, id)
    WHERE status = 'pending';
CREATE INDEX intelligence_suggestions_message ON intelligence_suggestions (tenant_id, message_id, locale);
CREATE INDEX intelligence_suggestions_list ON intelligence_suggestions (tenant_id, created_at DESC, id);
CREATE INDEX intelligence_suggestions_decided ON intelligence_suggestions (tenant_id, project_id, locale, decided_at)
    WHERE decided_at IS NOT NULL;

-- ── privacy ────────────────────────────────────────────────────────

-- Which provider and model saw which message, and exactly what they were
-- sent (RFC 0003 §7). Append-only.
CREATE TABLE intelligence_disclosures (
    id            uuid        PRIMARY KEY,
    tenant_id     uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    job_id        uuid        NOT NULL,
    project_id    uuid        NOT NULL,
    message_id    uuid        NOT NULL,
    locale        text        NOT NULL,
    task          text        NOT NULL,
    provider      text        NOT NULL,
    model         text        NOT NULL,
    system_sha256 text        NOT NULL,
    sent          jsonb       NOT NULL CHECK (jsonb_typeof(sent) = 'array'),
    occurred_at   timestamptz NOT NULL
);
CREATE INDEX intelligence_disclosures_list ON intelligence_disclosures (tenant_id, occurred_at DESC, id);
CREATE INDEX intelligence_disclosures_job ON intelligence_disclosures (job_id);
CREATE INDEX intelligence_disclosures_message ON intelligence_disclosures (tenant_id, message_id);

-- ── row-level security ─────────────────────────────────────────────

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['intelligence_providers', 'intelligence_settings', 'intelligence_project_settings',
                             'intelligence_routing_policies', 'intelligence_spend', 'intelligence_fills',
                             'intelligence_jobs', 'intelligence_suggestions', 'intelligence_disclosures']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I USING (tenant_id = app_current_tenant())'
                       ' WITH CHECK (tenant_id = app_current_tenant())', t || '_tenant_isolation', t);
    END LOOP;
END
$$;

-- The job workers claim across tenants (system scope
-- "intelligence.jobs"): read and update jobs, read the concurrency cap.
CREATE POLICY intelligence_jobs_system_select ON intelligence_jobs
    FOR SELECT TO glossa_system USING (true);
CREATE POLICY intelligence_jobs_system_update ON intelligence_jobs
    FOR UPDATE TO glossa_system USING (true) WITH CHECK (true);
CREATE POLICY intelligence_settings_system_select ON intelligence_settings
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT, UPDATE ON intelligence_jobs TO glossa_system;
GRANT SELECT ON intelligence_settings TO glossa_system;

GRANT SELECT, INSERT, UPDATE, DELETE ON intelligence_providers TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON intelligence_settings TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON intelligence_project_settings TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON intelligence_routing_policies TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON intelligence_fills TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON intelligence_jobs TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON intelligence_suggestions TO glossa_app;
-- Ledgers are append-only: no UPDATE. Spend is money history and
-- outlives projects; a deleted project's disclosures (they hold its
-- text) are erased with it (intelligence.drop_project).
GRANT SELECT, INSERT ON intelligence_spend TO glossa_app;
GRANT SELECT, INSERT, DELETE ON intelligence_disclosures TO glossa_app;
