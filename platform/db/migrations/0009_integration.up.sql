-- 0009 — Integration (RFC 0003 §5–§6): import and export jobs over the
-- interchange converters, and each import's per-item results.
--
-- Both tables are tenant-owned under the standard isolation policy. The
-- files themselves (an import's upload, an export's output) live in
-- object storage under integration/…; a row names its object and when
-- retention deleted it. Nothing references another context's tables:
-- the jobs read and write Catalog, Localization and Knowledge through
-- their application services.
--
-- The job workers claim across tenants and the retention sweep finds
-- expired files across tenants, as the outbox relay does: the system
-- scope "integration.jobs" may SELECT and UPDATE integration_jobs —
-- nothing else, and never insert or delete. Each job then runs in its
-- own tenant's scope.

-- One import or export. state: awaiting_upload (imports) → queued →
-- running → succeeded | failed | cancelled; running jobs whose lease
-- (available_at) passed are claimed again.
CREATE TABLE integration_jobs (
    id               uuid        PRIMARY KEY,
    tenant_id        uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    direction        text        NOT NULL CHECK (direction IN ('import', 'export')),
    -- NULL: a tenant-wide translation memory or termbase.
    project_id       uuid,
    kind             text        NOT NULL CHECK (kind IN ('catalog', 'tm', 'termbase')),
    format           text        NOT NULL CHECK (format IN ('xliff', 'json', 'po', 'tmx', 'tbx')),
    mode             text        CHECK (mode IN ('dry_run', 'merge', 'overwrite')),
    options          jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(options) = 'object'),
    -- What the requester may do, as it was when they asked: the worker
    -- acts for them within it (locales they may import, review, manage).
    access           jsonb       NOT NULL CHECK (jsonb_typeof(access) = 'object'),
    state            text        NOT NULL CHECK (state IN ('awaiting_upload', 'queued', 'running', 'succeeded', 'failed', 'cancelled')),
    file_name        text        NOT NULL DEFAULT '' CHECK (char_length(file_name) <= 255),
    -- The object: an import's upload or an export's output.
    file_key         text        CHECK (char_length(file_key) <= 900),
    file_size        bigint      CHECK (file_size >= 0),
    file_sha256      text        CHECK (char_length(file_sha256) = 64),
    content_type     text,
    -- Imports: the digest of the file and every option that shapes the
    -- result; a later upload with the same one reuses this job's result.
    fingerprint      text        CHECK (char_length(fingerprint) = 64),
    reused_job_id    uuid,
    summary          jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(summary) = 'object'),
    total_items      integer     NOT NULL DEFAULT 0 CHECK (total_items >= 0),
    processed_items  integer     NOT NULL DEFAULT 0 CHECK (processed_items >= 0),
    failure_code     text,
    failure_message  text        CHECK (char_length(failure_message) <= 4000),
    cancel_requested boolean     NOT NULL DEFAULT false,
    attempts         integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts     integer     NOT NULL CHECK (max_attempts BETWEEN 1 AND 20),
    available_at     timestamptz NOT NULL,
    claim_token      uuid,
    created_by       text        NOT NULL,
    created_at       timestamptz NOT NULL,
    started_at       timestamptz,
    finished_at      timestamptz,
    updated_at       timestamptz NOT NULL,
    -- Retention: the files are deleted after expires_at.
    expires_at       timestamptz NOT NULL,
    files_deleted_at timestamptz,
    CHECK ((direction = 'import') = (mode IS NOT NULL))
);
CREATE INDEX integration_jobs_list ON integration_jobs (tenant_id, direction, created_at DESC, id DESC);
CREATE INDEX integration_jobs_project ON integration_jobs (tenant_id, project_id);
CREATE INDEX integration_jobs_claimable ON integration_jobs (available_at, id)
    WHERE state IN ('queued', 'running');
CREATE INDEX integration_jobs_expiring ON integration_jobs (expires_at)
    WHERE files_deleted_at IS NULL;
CREATE INDEX integration_jobs_fingerprint ON integration_jobs (tenant_id, fingerprint)
    WHERE state = 'succeeded' AND reused_job_id IS NULL;

-- An import's result per item, in file order (seq). A job that reuses
-- another's result has none of its own.
CREATE TABLE integration_job_items (
    job_id    uuid    NOT NULL REFERENCES integration_jobs (id) ON DELETE CASCADE,
    tenant_id uuid    NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    seq       integer NOT NULL CHECK (seq >= 0),
    kind      text    NOT NULL CHECK (kind IN ('message', 'translation', 'tm_unit', 'concept')),
    item_key  text    NOT NULL DEFAULT '' CHECK (char_length(item_key) <= 1000),
    locale    text    NOT NULL DEFAULT '' CHECK (char_length(locale) <= 35),
    status    text    NOT NULL CHECK (status IN ('created', 'updated', 'unchanged', 'conflict', 'invalid')),
    code      text    CHECK (char_length(code) <= 64),
    detail    text    CHECK (char_length(detail) <= 2000),
    line      integer CHECK (line > 0),
    col       integer CHECK (col > 0),
    PRIMARY KEY (job_id, seq)
);
CREATE INDEX integration_job_items_tenant ON integration_job_items (tenant_id);

-- ── row-level security ─────────────────────────────────────────────

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['integration_jobs', 'integration_job_items']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I USING (tenant_id = app_current_tenant())'
                       ' WITH CHECK (tenant_id = app_current_tenant())', t || '_tenant_isolation', t);
    END LOOP;
END
$$;

-- The job workers claim and the retention sweep finds expired files
-- across tenants (system scope "integration.jobs").
CREATE POLICY integration_jobs_system_select ON integration_jobs
    FOR SELECT TO glossa_system USING (true);
CREATE POLICY integration_jobs_system_update ON integration_jobs
    FOR UPDATE TO glossa_system USING (true) WITH CHECK (true);
GRANT SELECT, UPDATE ON integration_jobs TO glossa_system;

GRANT SELECT, INSERT, UPDATE, DELETE ON integration_jobs TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON integration_job_items TO glossa_app;
