-- 0038 — Linguistic-QA jobs (RFC 0005 §3.8, §13 wave 6).
--
-- The linguistic layer is the one layer that needs a model, so it is a
-- **job** and never a check: `glossa check` never calls an AI provider
-- (§14 decision 2). This table is that job — what it covers, where it
-- stands, and what became of it.
--
-- It is Quality's table and not Intelligence's because what the job
-- produces is *findings*: tenant data in Quality's own tables, under
-- forced RLS, that never goes to the edge (§10). Intelligence owns the
-- model call, is reached through an application port, and hands back a
-- `batch` handle recorded here so a read can follow the work and a
-- cancel can stop it. Nothing here duplicates Intelligence's queue: the
-- consent, the routing and the per-tenant budget stay exactly where M2
-- put them.
--
-- The row keys its project by Catalog's id without a foreign key
-- (RFC 0002 §4): Quality learns about projects through a port, as its
-- runs, findings and waivers already do (0027).
--
-- `check_run_id` is what makes recording idempotent. A job's findings
-- land in exactly one check run; a job that already names one never
-- records a second, so following the same finished job twice cannot
-- double a project's linguistic findings.

CREATE TABLE quality_linguistic_jobs (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id uuid        NOT NULL,
    -- The branch or environment the review is of. A finding lives in a
    -- check run and a check run is always of a ref, so a job names one.
    ref        text        NOT NULL CHECK (ref <> '' AND length(ref) <= 255),
    -- The scope: the locales reviewed, and the slice of the catalog
    -- covered. A document rather than columns because it is read whole
    -- and never selected on — the job is looked up by id, or listed for
    -- a project.
    scope      jsonb       NOT NULL CHECK (jsonb_typeof(scope) = 'object'),
    state      text        NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    -- Intelligence's handle for the work; empty until it is handed over.
    batch      text        NOT NULL DEFAULT '',
    -- The run the findings were recorded in; NULL while there are none.
    check_run_id uuid,
    findings          integer NOT NULL DEFAULT 0 CHECK (findings >= 0),
    -- How many messages were left out because their namespace is tagged
    -- `sensitive` (RFC 0003 §7). Counted and named, never silently
    -- dropped: a reviewer has to know the layer did not look there.
    skipped_sensitive integer NOT NULL DEFAULT 0 CHECK (skipped_sensitive >= 0),
    reviewed          integer NOT NULL DEFAULT 0 CHECK (reviewed >= 0),
    failure_code text        NOT NULL DEFAULT '',
    last_error   text        NOT NULL DEFAULT '',
    created_by   text        NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    started_at   timestamptz,
    finished_at  timestamptz,
    -- A job that finished says why it ended where it did, and a job
    -- that has not finished cannot claim a failure.
    CONSTRAINT quality_linguistic_jobs_failure CHECK (
        (state = 'failed' AND failure_code <> '') OR (state <> 'failed' AND failure_code = '')
    )
);
CREATE INDEX quality_linguistic_jobs_tenant ON quality_linguistic_jobs (tenant_id);
-- A project's jobs, newest first: the list the API and Studio page.
CREATE INDEX quality_linguistic_jobs_project ON quality_linguistic_jobs (project_id, created_at DESC, id DESC);

ALTER TABLE quality_linguistic_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE quality_linguistic_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY quality_linguistic_jobs_tenant_isolation ON quality_linguistic_jobs
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

-- A job moves through its states, so unlike a finding it is updated.
-- DELETE is retention and erasing a deleted project's quality.
GRANT SELECT, INSERT, UPDATE, DELETE ON quality_linguistic_jobs TO glossa_app;
