package app

import "errors"

// Application errors. The HTTP adapter maps each to a problem code.
var (
	ErrUnauthenticated    = errors.New("identity: unauthenticated")
	ErrForbidden          = errors.New("identity: forbidden")
	ErrInvalidEmail       = errors.New("identity: invalid email")
	ErrWeakPassword       = errors.New("identity: password must be at least 12 characters")
	ErrLinkInvalid        = errors.New("identity: link is unknown, expired or used")
	ErrInvalidCredentials = errors.New("identity: invalid email or password")
	ErrEmailUnverified    = errors.New("identity: email address not verified")
	ErrTOTPRequired       = errors.New("identity: TOTP code required")
	ErrTOTPInvalid        = errors.New("identity: TOTP code invalid")
	ErrTOTPAlreadyEnabled = errors.New("identity: TOTP already enabled")
	ErrTOTPNotPending     = errors.New("identity: no TOTP enrollment to confirm")
	ErrTOTPNotEnabled     = errors.New("identity: TOTP not enabled")
	ErrAccountLocked      = errors.New("identity: account temporarily locked")
	ErrPasskeysDisabled   = errors.New("identity: passkeys are not configured")
	ErrEmailDisabled      = errors.New("identity: email is not configured")
	ErrNoPasskeys         = errors.New("identity: no passkeys for this account")
	ErrPasskeyInvalid     = errors.New("identity: passkey ceremony failed")
	ErrPersonOnly         = errors.New("identity: only a person can do this")
	ErrIdempotencyReuse   = errors.New("identity: idempotency key reused for a different request")
	ErrIdempotencyBusy    = errors.New("identity: a request with this idempotency key is in progress")
	ErrPreconditionFailed = errors.New("identity: resource changed since it was read")
)
