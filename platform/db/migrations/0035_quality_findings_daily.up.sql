-- 0035 — Quality: findings by layer per day (RFC 0005 §8).
--
-- M4 adds no time-series store: the seven numbers of the quality
-- summary are computed from the owning context's own tables on read,
-- cached for a minute, and `chronos` and the Insights context stay for
-- M5 (§14 decision 7). The one trend worth having now is findings by
-- layer per day, and it lives here because Quality owns the data and
-- the rollup is three columns: a day, a layer, and how many distinct
-- findings that layer reported on it.
--
-- The count is over *distinct fingerprints*, not rows. A project whose
-- pull-request check runs forty times a day would otherwise report the
-- same clipped Japanese button forty times and call it a trend; the
-- fingerprint is the finding's identity across runs and surfaces
-- (§2.1), so counting it once a day is the number a person means.
--
-- A layer appears on a day only if a run that day actually ran it — the
-- run's `layers` column says which did. That is deliberate: a missing
-- row means "not looked at" and a row with 0 means "looked at and
-- clean", and a dashboard that showed 0 for the first would be lying
-- about the second. A day with no run has no rows at all.
--
-- The row is derived: it is recomputed from quality_findings whenever a
-- run of that day completes, and dropping the table would cost the
-- trend and nothing else. The findings stay the record.

CREATE TABLE quality_findings_daily (
    tenant_id  uuid    NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    project_id uuid    NOT NULL,
    -- The UTC day the runs started on.
    day        date    NOT NULL,
    layer      text    NOT NULL CHECK (layer IN ('structure', 'parity', 'completeness',
                                                 'terminology', 'style', 'length', 'locale',
                                                 'source', 'visual', 'linguistic')),
    -- Distinct fingerprints that layer reported that day, the waived
    -- included: a waived finding is still computed and still counted
    -- (§2.3), and a trend that fell when somebody wrote a reason would
    -- be measuring the writing of reasons.
    findings   integer NOT NULL CHECK (findings >= 0),
    PRIMARY KEY (project_id, day, layer)
);
CREATE INDEX quality_findings_daily_tenant ON quality_findings_daily (tenant_id);

ALTER TABLE quality_findings_daily ENABLE ROW LEVEL SECURITY;
ALTER TABLE quality_findings_daily FORCE ROW LEVEL SECURITY;
CREATE POLICY quality_findings_daily_tenant_isolation ON quality_findings_daily
    USING (tenant_id = app_current_tenant()) WITH CHECK (tenant_id = app_current_tenant());

-- The rollup is an upsert (a later run of the same day restates that
-- day), and DELETE is retention and erasing a deleted project's
-- quality.
GRANT SELECT, INSERT, UPDATE, DELETE ON quality_findings_daily TO glossa_app;
