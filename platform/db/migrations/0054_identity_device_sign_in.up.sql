-- 0054 — Identity: device sign-in (RFC 0006 §7.2; §13 wave 5).
--
-- The OAuth 2.0 device authorization grant (RFC 8628) through which the
-- CLI signs a person in. Like sessions and sign-in links it belongs to a
-- person, not to a tenant, and exists before any tenant is known, so it
-- is one of Identity's global tables: no tenant_id, ENABLE + FORCE
-- row-level security, reachable only by glossa_system through the
-- kernel's system-scope unit of work (listed in the RLS guard test).
--
-- Nothing secret is stored: the device code (256 bits) and the user
-- code are kept as their SHA-256, and the session an approved device
-- receives is an ordinary row of identity_sessions — whose token_hash
-- is auth-go's SHA-256 of the raw token — marked kind = 'device'.

CREATE TABLE identity_device_authorizations (
    id               uuid        PRIMARY KEY,
    device_code_hash text        NOT NULL UNIQUE CHECK (char_length(device_code_hash) = 64),
    user_code_hash   text        NOT NULL CHECK (char_length(user_code_hash) = 64),
    client_name      text        NOT NULL CHECK (char_length(client_name) BETWEEN 1 AND 100),
    status           text        NOT NULL CHECK (status IN ('pending', 'approved', 'denied', 'redeemed')),
    -- Who decided it; NULL while pending.
    person_id        uuid        REFERENCES identity_people (id) ON DELETE CASCADE,
    interval_seconds integer     NOT NULL CHECK (interval_seconds BETWEEN 1 AND 300),
    requested_at     timestamptz NOT NULL,
    expires_at       timestamptz NOT NULL,
    last_polled_at   timestamptz,
    decided_at       timestamptz,
    redeemed_at      timestamptz,
    CHECK (expires_at > requested_at),
    CHECK ((status = 'pending') = (person_id IS NULL)),
    CHECK ((status = 'pending') = (decided_at IS NULL)),
    CHECK ((status = 'redeemed') = (redeemed_at IS NOT NULL))
);
-- A person types the user code; at most one pending authorization holds
-- a given code at a time (a collision on start draws again).
CREATE UNIQUE INDEX identity_device_authorizations_pending_user_code
    ON identity_device_authorizations (user_code_hash) WHERE status = 'pending';
CREATE INDEX identity_device_authorizations_expiry ON identity_device_authorizations (expires_at);

ALTER TABLE identity_device_authorizations ENABLE ROW LEVEL SECURITY;
ALTER TABLE identity_device_authorizations FORCE ROW LEVEL SECURITY;
CREATE POLICY identity_device_authorizations_system ON identity_device_authorizations
    TO glossa_system USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON identity_device_authorizations TO glossa_system;

-- A device's session is the person's session — same table, same
-- lifetime, ended by "sign out everywhere" — but it is presented as a
-- glossa_dev_ bearer and never as the cookie, and a cookie session is
-- never accepted as a bearer. The kind keeps the two apart.
ALTER TABLE identity_sessions
    ADD COLUMN kind text NOT NULL DEFAULT 'browser' CHECK (kind IN ('browser', 'device'));
