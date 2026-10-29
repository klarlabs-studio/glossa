-- 0039 — Quality: the two daily sweeps (RFC 0005 §2.2, §2.3, §13 wave 7).
--
-- Two things have been accumulating since 0027 with nothing to collect
-- them. A waiver with an `expires_at` stops standing on its date, but
-- nothing ever wrote that down: the domain computed it on every read
-- and the row went on looking like a waiver somebody might still
-- revoke. And check runs are kept 90 days (§2.2) — 0027 even created
-- the index for that sweep — but no sweep was ever written, so a busy
-- project's runs and their findings grew without bound.
--
-- `expires_at` stays optional (§15 question 4, decided by the owner).
-- Some waivers are genuinely permanent decisions — "Login is the German
-- term" does not expire — and forcing a date makes people pick one at
-- random. The sweep therefore expires the waivers that set a date, and
-- the dashboard keeps staleness visible rather than blocking it.
--
-- An expiry is *recorded*, never deleted, exactly the way revoking is:
-- the row stays, and gains the moment the sweep retired it.

ALTER TABLE quality_waivers ADD COLUMN expired_at timestamptz;
-- A waiver can only be expired if it had a date to expire on. Nothing
-- else may set this column: it is the sweep's record, not a second way
-- to revoke.
ALTER TABLE quality_waivers ADD CONSTRAINT quality_waivers_expired_was_dated
    CHECK (expired_at IS NULL OR expires_at IS NOT NULL);

-- The sweep's lookup: waivers past their date that nobody has recorded
-- yet. It replaces 0027's index, which could not skip the ones already
-- swept and would therefore have rescanned them every day forever.
DROP INDEX quality_waivers_expiry;
CREATE INDEX quality_waivers_expiry ON quality_waivers (expires_at)
    WHERE expires_at IS NOT NULL AND revoked_at IS NULL AND expired_at IS NULL;

-- ── system scope quality.sweep ──────────────────────────────────────
--
-- Both sweeps run per tenant, in the tenant's own scope. This opens
-- just enough to glossa_system (system scope "quality.sweep") for the
-- daily job to find which tenants have work: when a waiver expires, and
-- when a run started. No reason, no fingerprint, no ref, no code —
-- nothing a finding says about anybody's copy (§10).
--
-- What the job learns from quality_check_runs is deliberately an
-- over-approximation: the newest run of a ref is never swept, and this
-- scope cannot see which run that is because it cannot see project_id
-- or ref. Visiting a tenant that turns out to have nothing to sweep
-- costs one query; telling the system scope which branches exist would
-- cost rather more.

CREATE POLICY quality_waivers_system_select ON quality_waivers
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (tenant_id, expires_at, revoked_at, expired_at) ON quality_waivers TO glossa_system;

CREATE POLICY quality_check_runs_system_select ON quality_check_runs
    FOR SELECT TO glossa_system USING (true);
GRANT SELECT (tenant_id, started_at) ON quality_check_runs TO glossa_system;
