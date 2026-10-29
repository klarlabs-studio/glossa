-- 0031 — The check policy's version history (RFC 0005 §4.3, §13 wave 3).
--
-- 0029 made the policy a document and kept **one** version behind it:
-- `settings.check_policy.previous`, which is what a pull request inside
-- its grace period grades against. That is the evaluator's history, and
-- it is deliberately one version deep — two graces at once would mean a
-- check that cannot say which policy it used.
--
-- It is not a *record*. "Who tightened terminology to error, and when?"
-- has no answer in one slot, and a policy is an organizational decision
-- about a project (§4.2): the decision is worth keeping long after the
-- version that carried it stops grading anything.
--
-- So the live document stays exactly where it is — Catalog owns the
-- project's settings, the PR check and `glossa check` read it there,
-- and nothing about grading moves — and Quality appends one row per
-- saved version here: the document as it was stored, who wrote it, and
-- when. The table is append-only; a version is history, and history is
-- not edited. DELETE is retention and erasing a deleted project's
-- quality, exactly as it is for runs and findings (0027).
--
-- Quality keys the row by Catalog's project id without a foreign key
-- (RFC 0002 §4): it learns about projects through a port. The document
-- is checked only for being an object here — what a policy may *say* is
-- Catalog's constraint on the column that grades (0029) and the
-- domain's on the way in. Storage bounding the same vocabulary twice
-- would mean two places to change when a layer is added.

CREATE TABLE quality_policy_versions (
    id         uuid        PRIMARY KEY,
    tenant_id  uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id uuid        NOT NULL,
    -- The document's monotonic version (checkpolicy.Policy.Version).
    -- Version 0 is the pre-M4 policy, which nobody saved through this
    -- table, so the first row a project gets is version 1.
    version    integer     NOT NULL CHECK (version >= 0),
    -- The document as it was stored, without its `previous`: the
    -- history is this table, not a chain inside one row.
    document   jsonb       NOT NULL CHECK (jsonb_typeof(document) = 'object'),
    created_by text        NOT NULL,
    created_at timestamptz NOT NULL,
    -- One row per version of a project's policy. It is also what keeps
    -- the version monotonic under concurrency: two saves racing for the
    -- same number cannot both land.
    UNIQUE (project_id, version)
);
CREATE INDEX quality_policy_versions_tenant ON quality_policy_versions (tenant_id);
-- The history, newest first, and reading one version by number.
CREATE INDEX quality_policy_versions_project ON quality_policy_versions (project_id, version DESC);

ALTER TABLE quality_policy_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE quality_policy_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY quality_policy_versions_tenant_isolation ON quality_policy_versions
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

-- Append-only: a saved version is history, like a finding, so it gets
-- no UPDATE. DELETE is retention and erasing a deleted project.
GRANT SELECT, INSERT, DELETE ON quality_policy_versions TO glossa_app;
