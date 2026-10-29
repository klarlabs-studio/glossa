-- 0036 — a deployment remembers that it went out against the policy,
-- and why.
--
-- RFC 0005 §4.1: publishing to an environment whose completeness
-- requirement is not met fails with `policy_not_met`, "and it can be
-- overridden with an explicit reason, which is audited — a hard block
-- with no escape hatch gets routed around by disabling the check".
--
-- The reason has nowhere else to live. A release is immutable and shared
-- by every environment that ever served it, so an override of one
-- environment's gate is not a property of it; `note` is the publisher's
-- description of the release, and overwriting it would lose what they
-- wrote. `release_deployments` is already the audit trail of pointer
-- moves — who pointed what where, when and by which action — and the
-- override is exactly one more fact about one of those moves.
--
-- The CHECK is the whole point of the feature rather than tidiness: an
-- override nobody has to justify is indistinguishable from the check not
-- existing, so the database refuses a forced deployment with no reason
-- and a reason on a deployment that forced nothing. Every row written
-- before this migration is `false, ''`, which satisfies it.
ALTER TABLE release_deployments
    ADD COLUMN forced       boolean NOT NULL DEFAULT false,
    ADD COLUMN force_reason text    NOT NULL DEFAULT ''
        CHECK (char_length(force_reason) <= 1000);

ALTER TABLE release_deployments
    ADD CONSTRAINT release_deployments_forced_has_reason
        CHECK (forced = (force_reason <> ''));
