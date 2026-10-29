-- 0020 — the Glossa PR check and its sticky comment (RFC 0004 §6.4).
--
-- One row per pull request Glossa reports on. The row is three things at
-- once, and that is deliberate:
--
--   * The **queue**. `catalog.branch.pushed`, `context.build.ingested`,
--     a translation revised on the branch and `check_run.rerequested`
--     all make the row due; a worker claims it FOR UPDATE SKIP LOCKED,
--     renders the report and calls GitHub. Claiming the row is what
--     serializes the work for a pull request, so two jobs can never race
--     the one sticky comment — the comment's id lives on this row, and
--     only its claimer may write it.
--   * The **check runs**. A repository can feed several projects, one
--     per monorepo path, so a pull request carries one check run per Git
--     connection. `runs` maps a connection's id to the check run GitHub
--     made for this head SHA, the annotations already appended to it,
--     and what that project's CI has uploaded. GitHub *appends*
--     annotations rather than replacing them, so each one is recorded
--     when it is sent and never sent to the same run twice.
--   * The **deadline**. `requested_at` is when the pull-request event
--     arrived: it is both the §11 latency baseline and the start of the
--     thirty minutes after which a commit with no Glossa CI run is
--     completed `neutral`.
--
-- The head SHA is a column rather than a key, because a `synchronize`
-- moves the pull request to a new commit: the row follows it, the check
-- runs are made afresh for the new SHA, and the sticky comment stays the
-- one comment it always was. A rerequested check clears `runs` for the
-- same reason — a rerun is a new check run, so its annotation ledger
-- starts empty.

CREATE TABLE integration_github_checks (
    id              uuid        PRIMARY KEY,
    tenant_id       uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    -- GitHub's installation ID: the token, circuit and target scope.
    installation_id bigint      NOT NULL CHECK (installation_id > 0),
    repository_id   bigint      NOT NULL CHECK (repository_id > 0),
    pull_request    integer     NOT NULL CHECK (pull_request > 0),
    branch          text        NOT NULL CHECK (char_length(branch) BETWEEN 1 AND 255),
    head_sha        text        NOT NULL CHECK (head_sha ~ '^[0-9a-f]{40}$'),
    -- GitHub's id for the one sticky comment (0 until it is written).
    comment_id      bigint      NOT NULL DEFAULT 0 CHECK (comment_id >= 0),
    -- Per Git connection: {"check_run_id": 1, "annotations": ["…"],
    -- "pushed": true, "usages": true, "conclusion": "success"}.
    runs            jsonb       NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(runs) = 'object'),
    state           text        NOT NULL CHECK (state IN ('queued', 'completed')),
    conclusion      text        NOT NULL DEFAULT '' CHECK (conclusion IN ('', 'success', 'failure', 'neutral')),
    attempts        integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    -- Why the last attempt failed, for the operator. Never GitHub content.
    failure         text        NOT NULL DEFAULT '' CHECK (char_length(failure) <= 500),
    claim_token     uuid,
    -- When the pull-request event arrived: the latency baseline and the
    -- start of the thirty-minute wait for CI.
    requested_at    timestamptz NOT NULL,
    available_at    timestamptz NOT NULL,
    completed_at    timestamptz,
    updated_at      timestamptz NOT NULL
);

-- One check row per pull request, so the sticky comment has one owner.
-- The index is global rather than per tenant, as the installation index
-- is: a repository belongs to one installation, and an installation to
-- one tenant.
CREATE UNIQUE INDEX integration_github_checks_pr
    ON integration_github_checks (repository_id, pull_request);
CREATE INDEX integration_github_checks_tenant ON integration_github_checks (tenant_id);
CREATE INDEX integration_github_checks_claimable
    ON integration_github_checks (available_at, id) WHERE state = 'queued';
-- The timeout sweep looks for queued rows past their deadline.
CREATE INDEX integration_github_checks_requested ON integration_github_checks (requested_at);

ALTER TABLE integration_github_checks ENABLE ROW LEVEL SECURITY;
ALTER TABLE integration_github_checks FORCE ROW LEVEL SECURITY;
CREATE POLICY integration_github_checks_tenant_isolation ON integration_github_checks
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

-- The check worker is a queue worker across tenants, like the webhook
-- inbox: it claims the oldest due row whatever tenant it belongs to and
-- only then enters that tenant's scope to render the report. So the
-- queue itself is the system scope's, under "integration.github".
CREATE POLICY integration_github_checks_system ON integration_github_checks
    TO glossa_system USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON integration_github_checks TO glossa_system;
-- A tenant reads its own check rows (Studio shows what the check said).
GRANT SELECT ON integration_github_checks TO glossa_app;
