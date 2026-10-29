-- People and their credentials are global: these queries run in system
-- scope (db.SystemTx), except where noted.

-- name: InsertPerson :exec
INSERT INTO identity_people (id, email, display_name, individual_tenant_id, password_hash,
                             email_verified_at, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(email), sqlc.arg(display_name), sqlc.arg(individual_tenant_id),
        sqlc.narg(password_hash), sqlc.narg(email_verified_at), sqlc.arg(created_at), sqlc.arg(created_at));

-- name: GetPersonByEmail :one
SELECT p.*, (t.confirmed_at IS NOT NULL)::boolean AS totp_enabled
FROM identity_people p
LEFT JOIN identity_totp t ON t.person_id = p.id
WHERE p.email = sqlc.arg(email);

-- name: GetPersonByID :one
SELECT p.*, (t.confirmed_at IS NOT NULL)::boolean AS totp_enabled
FROM identity_people p
LEFT JOIN identity_totp t ON t.person_id = p.id
WHERE p.id = sqlc.arg(id);

-- name: MarkEmailVerified :exec
UPDATE identity_people
SET email_verified_at = coalesce(email_verified_at, sqlc.arg(at)), updated_at = sqlc.arg(at)
WHERE id = sqlc.arg(id);

-- name: SetPasswordHash :exec
UPDATE identity_people
SET password_hash = sqlc.arg(password_hash), updated_at = sqlc.arg(at)
WHERE id = sqlc.arg(id);

-- Sessions come in two kinds (0054): a browser's, sent as the cookie,
-- and a device's, sent as a glossa_dev_ bearer. Each is found and ended
-- only as its own kind; signing out everywhere ends both.

-- name: InsertSession :exec
INSERT INTO identity_sessions (token_hash, person_id, created_at, expires_at, kind)
VALUES (sqlc.arg(token_hash), sqlc.arg(person_id), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.arg(kind));

-- name: GetSession :one
SELECT token_hash, person_id, created_at, expires_at FROM identity_sessions
WHERE token_hash = sqlc.arg(token_hash) AND kind = sqlc.arg(kind);

-- name: DeleteSession :execrows
DELETE FROM identity_sessions WHERE token_hash = sqlc.arg(token_hash) AND kind = sqlc.arg(kind);

-- name: DeleteSessionsOfPerson :exec
DELETE FROM identity_sessions WHERE person_id = sqlc.arg(person_id);

-- name: InsertEmailLink :exec
INSERT INTO identity_email_links (hash, purpose, email, expires_at)
VALUES (sqlc.arg(hash), sqlc.arg(purpose), sqlc.arg(email), sqlc.arg(expires_at));

-- name: GetEmailLink :one
SELECT * FROM identity_email_links WHERE hash = sqlc.arg(hash) AND purpose = sqlc.arg(purpose);

-- name: ConsumeEmailLink :execrows
UPDATE identity_email_links SET consumed = true
WHERE hash = sqlc.arg(hash) AND purpose = sqlc.arg(purpose) AND NOT consumed;

-- name: InvalidateEmailLinks :exec
UPDATE identity_email_links SET consumed = true
WHERE email = sqlc.arg(email) AND purpose = sqlc.arg(purpose) AND NOT consumed;

-- name: UpsertPendingTOTP :execrows
-- Replaces a pending secret; never a confirmed one.
INSERT INTO identity_totp (person_id, secret_ciphertext, updated_at)
VALUES (sqlc.arg(person_id), sqlc.arg(secret_ciphertext), sqlc.arg(at))
ON CONFLICT (person_id) DO UPDATE
SET secret_ciphertext = EXCLUDED.secret_ciphertext, last_step = NULL, updated_at = EXCLUDED.updated_at
WHERE identity_totp.confirmed_at IS NULL;

-- name: GetTOTP :one
SELECT * FROM identity_totp WHERE person_id = sqlc.arg(person_id);

-- name: ConfirmTOTP :execrows
UPDATE identity_totp SET confirmed_at = sqlc.arg(at), updated_at = sqlc.arg(at)
WHERE person_id = sqlc.arg(person_id) AND confirmed_at IS NULL;

-- name: DeleteTOTP :execrows
DELETE FROM identity_totp WHERE person_id = sqlc.arg(person_id);

-- name: ConsumeTOTPStep :execrows
UPDATE identity_totp SET last_step = sqlc.arg(step)
WHERE person_id = sqlc.arg(person_id) AND (last_step IS NULL OR last_step < sqlc.arg(step));

-- name: InsertPasskey :exec
INSERT INTO identity_passkeys (credential_id, person_id, public_key, sign_count, name, created_at)
VALUES (sqlc.arg(credential_id), sqlc.arg(person_id), sqlc.arg(public_key), sqlc.arg(sign_count),
        sqlc.arg(name), sqlc.arg(created_at));

-- name: ListPasskeysOfPerson :many
SELECT * FROM identity_passkeys WHERE person_id = sqlc.arg(person_id) ORDER BY created_at;

-- name: GetPasskey :one
SELECT * FROM identity_passkeys WHERE credential_id = sqlc.arg(credential_id);

-- name: UpdatePasskeySignCount :execrows
UPDATE identity_passkeys SET sign_count = sqlc.arg(sign_count), last_used_at = sqlc.arg(at)
WHERE credential_id = sqlc.arg(credential_id);

-- name: DeletePasskey :execrows
DELETE FROM identity_passkeys WHERE credential_id = sqlc.arg(credential_id);

-- name: PagePasskeysOfPerson :many
-- Oldest first; keyset on (created_at, credential_id).
SELECT credential_id, name, created_at, last_used_at FROM identity_passkeys
WHERE person_id = sqlc.arg(person_id)
  AND (created_at, credential_id) > (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::bytea)
ORDER BY created_at, credential_id
LIMIT sqlc.arg(max_rows);

-- name: DeletePasskeyOfPerson :execrows
DELETE FROM identity_passkeys
WHERE credential_id = sqlc.arg(credential_id) AND person_id = sqlc.arg(person_id);

-- name: GetLoginAttempt :one
SELECT * FROM identity_login_attempts WHERE key = sqlc.arg(key);

-- name: SaveLoginAttempt :exec
INSERT INTO identity_login_attempts (key, failure_count, locked_until, updated_at)
VALUES (sqlc.arg(key), sqlc.arg(failure_count), sqlc.narg(locked_until), now())
ON CONFLICT (key) DO UPDATE
SET failure_count = EXCLUDED.failure_count, locked_until = EXCLUDED.locked_until, updated_at = now();

-- name: DeleteLoginAttempt :exec
DELETE FROM identity_login_attempts WHERE key = sqlc.arg(key);

-- name: RecordLoginFailure :one
-- One statement, so concurrent failures can't lose increments: an expired
-- lock restarts the count, and reaching max_failures sets the lock.
INSERT INTO identity_login_attempts AS a (key, failure_count, locked_until, updated_at)
VALUES (sqlc.arg(key), 1,
        CASE WHEN 1 >= sqlc.arg(max_failures)::integer
             THEN sqlc.arg(now)::timestamptz + make_interval(secs => sqlc.arg(lock_seconds)::double precision) END,
        sqlc.arg(now)::timestamptz)
ON CONFLICT (key) DO UPDATE
SET failure_count = CASE WHEN a.locked_until IS NOT NULL AND a.locked_until <= sqlc.arg(now)::timestamptz
                         THEN 1 ELSE a.failure_count + 1 END,
    locked_until = CASE
        WHEN (CASE WHEN a.locked_until IS NOT NULL AND a.locked_until <= sqlc.arg(now)::timestamptz
                   THEN 1 ELSE a.failure_count + 1 END) >= sqlc.arg(max_failures)::integer
        THEN sqlc.arg(now)::timestamptz + make_interval(secs => sqlc.arg(lock_seconds)::double precision)
        WHEN a.locked_until IS NOT NULL AND a.locked_until <= sqlc.arg(now)::timestamptz THEN NULL
        ELSE a.locked_until END,
    updated_at = sqlc.arg(now)::timestamptz
RETURNING failure_count, locked_until;

-- name: InsertCeremony :exec
INSERT INTO identity_webauthn_ceremonies (key_hash, purpose, person_id, state, expires_at)
VALUES (sqlc.arg(key_hash), sqlc.arg(purpose), sqlc.narg(person_id), sqlc.arg(state), sqlc.arg(expires_at));

-- name: TakeCeremony :one
-- Single use: the row is gone whether or not the ceremony then succeeds.
DELETE FROM identity_webauthn_ceremonies
WHERE key_hash = sqlc.arg(key_hash) AND purpose = sqlc.arg(purpose)
RETURNING person_id, state, expires_at;
