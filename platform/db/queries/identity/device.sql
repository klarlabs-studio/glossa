-- Device sign-in (RFC 0006 §7.2, migration 0054). System scope only:
-- a device authorization belongs to a person, not to a tenant.

-- name: InsertDeviceAuthorization :exec
INSERT INTO identity_device_authorizations (id, device_code_hash, user_code_hash, client_name, status,
                                            interval_seconds, requested_at, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(device_code_hash), sqlc.arg(user_code_hash), sqlc.arg(client_name), 'pending',
        sqlc.arg(interval_seconds), sqlc.arg(requested_at), sqlc.arg(expires_at));

-- name: PurgeDeviceAuthorizations :execrows
-- Expired authorizations have nothing left to say; the audit trail
-- holds what was decided.
DELETE FROM identity_device_authorizations WHERE expires_at < sqlc.arg(before);

-- name: LockPendingDeviceAuthorizationByUserCode :one
SELECT * FROM identity_device_authorizations
WHERE user_code_hash = sqlc.arg(user_code_hash) AND status = 'pending' AND expires_at > sqlc.arg(now)
FOR UPDATE;

-- name: LockDeviceAuthorizationByDeviceCode :one
SELECT * FROM identity_device_authorizations
WHERE device_code_hash = sqlc.arg(device_code_hash)
FOR UPDATE;

-- name: WithdrawDeviceApprovals :exec
-- Signing out everywhere: an approved device that has not taken its
-- session yet never gets one.
UPDATE identity_device_authorizations SET status = 'denied'
WHERE person_id = sqlc.arg(person_id) AND status = 'approved';

-- name: UpdateDeviceAuthorization :exec
UPDATE identity_device_authorizations
SET status = sqlc.arg(status), person_id = sqlc.narg(person_id), interval_seconds = sqlc.arg(interval_seconds),
    last_polled_at = sqlc.narg(last_polled_at), decided_at = sqlc.narg(decided_at),
    redeemed_at = sqlc.narg(redeemed_at)
WHERE id = sqlc.arg(id);
