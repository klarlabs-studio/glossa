-- 0053 — Audit: export jobs (RFC 0006 §6.2, wave 5).
--
-- One row per glossa.audit/v1 export of a range of the tenant's chain.
-- The worker writes the export's two objects — entries.jsonl and its
-- signed manifest.json — to object storage under
-- audit/v1/<tenant>/exports/<job>/, which glossa-edge never serves, and
-- records here what it wrote: the chain segment (first and last
-- sequence, first_prev_hash, last_hash), the entry count, the key that
-- signed it, and each object's key, SHA-256 and size. Retention deletes
-- the objects after expires_at (GLOSSA_AUDIT_EXPORT_RETENTION); the row
-- stays, as M2's export jobs do.
--
-- A job holds no entry and no text: it names a range.
--
-- The range: a time range (occurred_from, occurred_to) whose sequences
-- are set when the job has run, or a sequence range set when the job is
-- made. Exactly one.
--
-- The workers claim across tenants and the retention sweep finds
-- expired objects across tenants (system scope "audit.export_jobs"),
-- as Integration's do: it may SELECT and UPDATE this table, nothing
-- else, never insert or delete. Each job then runs in its own tenant's
-- scope. audit_entries is untouched: still INSERT and SELECT only.

CREATE TABLE audit_export_jobs (
    id                uuid        PRIMARY KEY,
    tenant_id         uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    state             text        NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed')),
    occurred_from     timestamptz,
    occurred_to       timestamptz,
    first_sequence    bigint      CHECK (first_sequence >= 1),
    last_sequence     bigint      CHECK (last_sequence >= 0),
    entry_count       bigint      NOT NULL DEFAULT 0 CHECK (entry_count >= 0),
    first_prev_hash   text        CHECK (first_prev_hash ~ '^[0-9a-f]{64}$'),
    last_hash         text        CHECK (last_hash ~ '^[0-9a-f]{64}$'),
    key_id            text        CHECK (key_id ~ '^[A-Za-z0-9._-]{1,64}$'),
    entries_key       text        CHECK (char_length(entries_key) <= 900),
    entries_sha256    text        CHECK (entries_sha256 ~ '^[0-9a-f]{64}$'),
    entries_bytes     bigint      CHECK (entries_bytes >= 0),
    manifest_key      text        CHECK (char_length(manifest_key) <= 900),
    manifest_sha256   text        CHECK (manifest_sha256 ~ '^[0-9a-f]{64}$'),
    manifest_bytes    bigint      CHECK (manifest_bytes >= 0),
    failure_code      text        CHECK (char_length(failure_code) <= 64),
    failure_message   text        CHECK (char_length(failure_message) <= 4000),
    attempts          integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts      integer     NOT NULL CHECK (max_attempts BETWEEN 1 AND 20),
    available_at      timestamptz NOT NULL,
    claim_token       uuid,
    created_by        text        NOT NULL CHECK (char_length(created_by) BETWEEN 1 AND 200),
    created_at        timestamptz NOT NULL,
    started_at        timestamptz,
    finished_at       timestamptz,
    updated_at        timestamptz NOT NULL,
    expires_at        timestamptz NOT NULL,
    files_deleted_at  timestamptz,
    CHECK ((occurred_from IS NULL) = (occurred_to IS NULL)),
    CHECK (occurred_from IS NULL OR occurred_from < occurred_to),
    -- A sequence range is set when the job is made; a time range's
    -- sequences only once it has run.
    CHECK (occurred_from IS NOT NULL OR (first_sequence IS NOT NULL AND last_sequence IS NOT NULL)),
    CHECK (last_sequence IS NULL OR first_sequence IS NULL OR last_sequence >= first_sequence - 1),
    CHECK (state <> 'succeeded' OR (entries_key IS NOT NULL AND manifest_key IS NOT NULL AND key_id IS NOT NULL
                                    AND first_sequence IS NOT NULL AND last_sequence IS NOT NULL))
);
CREATE INDEX audit_export_jobs_list ON audit_export_jobs (tenant_id, created_at DESC, id DESC);
CREATE INDEX audit_export_jobs_claimable ON audit_export_jobs (available_at, id)
    WHERE state IN ('queued', 'running');
CREATE INDEX audit_export_jobs_expiring ON audit_export_jobs (expires_at)
    WHERE files_deleted_at IS NULL;

ALTER TABLE audit_export_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_export_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY audit_export_jobs_tenant_isolation ON audit_export_jobs
    USING (tenant_id = app_current_tenant())
    WITH CHECK (tenant_id = app_current_tenant());
-- A job is never deleted by the application: it is the record that an
-- export was made (its objects expire, the row does not).
GRANT SELECT, INSERT, UPDATE ON audit_export_jobs TO glossa_app;

-- The workers claim and the retention sweep finds expired objects
-- across tenants (system scope "audit.export_jobs").
CREATE POLICY audit_export_jobs_system_select ON audit_export_jobs
    FOR SELECT TO glossa_system USING (true);
CREATE POLICY audit_export_jobs_system_update ON audit_export_jobs
    FOR UPDATE TO glossa_system USING (true) WITH CHECK (true);
GRANT SELECT, UPDATE ON audit_export_jobs TO glossa_system;

