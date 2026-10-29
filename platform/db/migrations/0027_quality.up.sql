-- 0027 — Quality (RFC 0005 §2): check runs, their findings, and the
-- waivers that accept a finding.
--
-- RFC 0002 §4 reserved the context and named its aggregates; this is
-- where it gets storage. Before M4 nothing stored a finding: terminology
-- QA was a stateless query, max_length lived in translations.warnings,
-- and integration_github_checks held the pull request's queue row and no
-- findings at all — so nothing had a history and a dashboard had nothing
-- to read.
--
-- Every table is tenant-owned under the standard isolation policy and
-- keyed by other contexts' project, message and capture IDs without
-- foreign keys (RFC 0002 §4): Quality learns about them through ports.
-- Within the context a finding belongs to its run and goes with it —
-- runs are kept 90 days (§2.2), findings live as long as their run.
--
-- A finding row is immutable: a run is one evaluation, and the next
-- evaluation writes new rows rather than editing the last one's. What
-- survives a run is the fingerprint (§2.1), which is what a waiver
-- names.

-- ── waivers ─────────────────────────────────────────────────────────
--
-- A waiver accepts a finding, forever or until a date, everywhere in the
-- project or on one branch. The reason is required and non-empty here as
-- well as in the domain, because storage bounds what can be stored: a
-- suppression nobody had to justify is technical debt with no paper
-- trail (§2.3, §14 decision 5).
--
-- source_revision is the source the waiver was made against. The waiver
-- dies when the source moves past it — the German somebody waived is not
-- the German that now ships — which the domain decides on read, so the
-- finding can come back and say why.

CREATE TABLE quality_waivers (
    id              uuid        PRIMARY KEY,
    tenant_id       uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id      uuid        NOT NULL,
    -- The finding this accepts (domain.Fingerprint).
    fingerprint     text        NOT NULL CHECK (fingerprint ~ '^f_[0-9a-f]{16}$'),
    reason          text        NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 1 AND 1000),
    scope           text        NOT NULL CHECK (scope IN ('project', 'branch')),
    -- The branch, for a branch-scoped waiver; '' for a project one.
    ref             text        NOT NULL DEFAULT '' CHECK (char_length(ref) <= 255),
    source_revision integer     NOT NULL CHECK (source_revision >= 0),
    created_by      text        NOT NULL,
    created_at      timestamptz NOT NULL,
    -- When the daily sweep retires it; NULL never.
    expires_at      timestamptz,
    -- When a person took it back; NULL while it stands.
    revoked_at      timestamptz,
    CHECK (scope = 'project' OR ref <> '')
);
CREATE INDEX quality_waivers_tenant ON quality_waivers (tenant_id);
-- One live waiver per finding and reach; revoking frees the slot.
CREATE UNIQUE INDEX quality_waivers_live
    ON quality_waivers (project_id, fingerprint, scope, ref) WHERE revoked_at IS NULL;
-- Listing and filtering a project's waivers, newest first, and the
-- dashboard's "waivers older than 90 days".
CREATE INDEX quality_waivers_project ON quality_waivers (project_id, created_at DESC, id DESC);
-- The daily expiry sweep.
CREATE INDEX quality_waivers_expiry ON quality_waivers (expires_at) WHERE expires_at IS NOT NULL AND revoked_at IS NULL;

-- ── check runs ──────────────────────────────────────────────────────
--
-- One evaluation of one ref against one policy version. The pull-request
-- check, Studio, the dashboard and `glossa findings` read one row rather
-- than four recomputations of the same thing (§2.2 rule 3).
--
-- layers lists what the run actually computed, so a reader can tell
-- "clean" from "not looked at": a layer the policy switched off is not
-- in the list.

CREATE TABLE quality_check_runs (
    id             uuid        PRIMARY KEY,
    tenant_id      uuid        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id     uuid        NOT NULL,
    -- What was checked: a branch, or an environment name.
    ref            text        NOT NULL CHECK (char_length(ref) BETWEEN 1 AND 255),
    trigger        text        NOT NULL CHECK (trigger IN ('cli', 'pull_request', 'write', 'capture', 'api')),
    -- The policy version the run graded itself against, so it can say
    -- which it used when two are live at once (§4.3). 0 while the policy
    -- document is still the two-field kernel policy.
    policy_version integer     NOT NULL DEFAULT 0 CHECK (policy_version >= 0),
    layers         text[]      NOT NULL,
    errors         integer     NOT NULL DEFAULT 0 CHECK (errors >= 0),
    warnings       integer     NOT NULL DEFAULT 0 CHECK (warnings >= 0),
    -- Waived is counted on its own and is never part of the other two:
    -- the number a dashboard shows has to be true.
    waived         integer     NOT NULL DEFAULT 0 CHECK (waived >= 0),
    -- NULL while the run is in flight.
    conclusion     text        CHECK (conclusion IN ('success', 'failure', 'neutral')),
    created_by     text        NOT NULL,
    started_at     timestamptz NOT NULL,
    completed_at   timestamptz,
    CHECK (completed_at IS NULL OR completed_at >= started_at),
    CHECK ((conclusion IS NULL) = (completed_at IS NULL))
);
CREATE INDEX quality_check_runs_tenant ON quality_check_runs (tenant_id);
-- The latest run of a ref, which is what every surface reads.
CREATE INDEX quality_check_runs_ref ON quality_check_runs (project_id, ref, started_at DESC, id DESC);
-- The 90-day retention sweep.
CREATE INDEX quality_check_runs_retention ON quality_check_runs (started_at);

-- ── findings ────────────────────────────────────────────────────────
--
-- One shape for every layer (§2.1). The locus columns are everything
-- that locates a finding: the catalog's message and locale from the
-- layer, and file, line, route, component, capture and region filled in
-- from Context at report time — which is what turns almost every finding
-- into a GitHub annotation instead of only the unknown keys.
--
-- explanation is the prose for a person; message_id and message_key are
-- the catalog message the finding is about. evidence is what the layer
-- measured and fix is the hint it can offer, both free-form per code and
-- therefore jsonb.

CREATE TABLE quality_findings (
    id                   uuid    PRIMARY KEY,
    tenant_id            uuid    NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    run_id               uuid    NOT NULL REFERENCES quality_check_runs (id) ON DELETE CASCADE,
    project_id           uuid    NOT NULL,
    fingerprint          text    NOT NULL CHECK (fingerprint ~ '^f_[0-9a-f]{16}$'),
    layer                text    NOT NULL CHECK (layer IN ('structure', 'parity', 'completeness',
                                                           'terminology', 'style', 'length', 'locale',
                                                           'source', 'visual', 'linguistic')),
    code                 text    NOT NULL CHECK (code ~ '^[a-z][a-z0-9_-]{0,63}$'),
    severity             text    NOT NULL CHECK (severity IN ('error', 'warning', 'waived')),
    -- locus ─ the catalog
    message_id           uuid,
    message_key          text    NOT NULL DEFAULT '' CHECK (char_length(message_key) <= 200),
    locale               text    NOT NULL DEFAULT '' CHECK (char_length(locale) <= 35),
    namespace            text    NOT NULL DEFAULT '' CHECK (char_length(namespace) <= 200),
    translation_revision uuid,
    -- locus ─ Context, at report time
    file                 text    NOT NULL DEFAULT '' CHECK (char_length(file) <= 1024),
    line                 integer CHECK (line >= 1),
    col                  integer CHECK (col >= 1),
    route                text    NOT NULL DEFAULT '' CHECK (char_length(route) <= 500),
    component            text    NOT NULL DEFAULT '' CHECK (char_length(component) <= 200),
    capture_id           uuid,
    region               text    NOT NULL DEFAULT '' CHECK (char_length(region) <= 64),
    -- locus ─ the offending words, in bytes
    span_side            text    CHECK (span_side IN ('source', 'target')),
    span_start           integer CHECK (span_start >= 0),
    span_end             integer CHECK (span_end >= 0),
    -- what was found
    explanation          text    NOT NULL CHECK (char_length(explanation) <= 4000),
    subject              text    NOT NULL DEFAULT '' CHECK (char_length(subject) <= 500),
    -- The MessageFormat kernel's Detail: what qualifies the code.
    detail               text    NOT NULL DEFAULT '' CHECK (char_length(detail) <= 500),
    evidence             jsonb,
    fix                  jsonb,
    -- The source revision the finding was computed against; a waiver
    -- dies when it changes.
    source_revision      integer CHECK (source_revision >= 0),
    -- The waiver that accepted it, when severity is 'waived'.
    waiver_id            uuid    REFERENCES quality_waivers (id) ON DELETE SET NULL,
    CHECK ((severity = 'waived') = (waiver_id IS NOT NULL)),
    CHECK (num_nulls(span_side, span_start, span_end) IN (0, 3)),
    CHECK (span_end IS NULL OR span_end >= span_start),
    CHECK (evidence IS NULL OR jsonb_typeof(evidence) = 'object'),
    CHECK (fix IS NULL OR jsonb_typeof(fix) = 'object')
);
CREATE INDEX quality_findings_tenant ON quality_findings (tenant_id);
-- A run's findings, grouped the way every report groups them.
CREATE INDEX quality_findings_run ON quality_findings (run_id, layer, severity, locale);
-- The list and its filters (§13 wave 2): a project's findings by layer,
-- severity and locale, newest run first.
CREATE INDEX quality_findings_project ON quality_findings (project_id, layer, severity, locale, run_id);
-- One finding's history, and matching a waiver to the findings it
-- accepts.
CREATE INDEX quality_findings_fingerprint ON quality_findings (project_id, fingerprint, run_id);
-- What is wrong with this message, for Studio and the overlay.
CREATE INDEX quality_findings_message ON quality_findings (message_id, locale) WHERE message_id IS NOT NULL;
-- Findings by capture and region (§13 wave 4).
CREATE INDEX quality_findings_capture ON quality_findings (capture_id) WHERE capture_id IS NOT NULL;

-- ── row-level security ─────────────────────────────────────────────

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['quality_waivers', 'quality_check_runs', 'quality_findings']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY %I ON %I USING (tenant_id = app_current_tenant())'
                       ' WITH CHECK (tenant_id = app_current_tenant())', t || '_tenant_isolation', t);
    END LOOP;
END
$$;

-- A run is completed after it is opened, and a waiver is revoked; a
-- finding is immutable, like a usage, so it gets no UPDATE. DELETE is
-- retention, and erasing a deleted project's quality.
GRANT SELECT, INSERT, UPDATE, DELETE ON quality_waivers TO glossa_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON quality_check_runs TO glossa_app;
GRANT SELECT, INSERT, DELETE ON quality_findings TO glossa_app;
