package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// codeInvalidProjectScope is a project scope that names something other
// than project ids, or too many of them.
const codeInvalidProjectScope problem.Code = "invalid_project_scope"

// codeAccessAndRestriction is a member PATCH that changes both access
// and restriction.
const codeAccessAndRestriction problem.Code = "access_and_restriction"

type mapping struct {
	status int
	code   problem.Code
	detail string
}

// problems maps application and domain errors to the codes documented in
// api/openapi.yaml. A code here is part of the contract: never rename one.
var problems = []struct {
	err error
	mapping
}{
	{app.ErrUnauthenticated, mapping{401, problem.CodeUnauthenticated, "authentication required"}},
	{authz.ErrUnauthenticated, mapping{401, problem.CodeUnauthenticated, "authentication required"}},
	{app.ErrInvalidCredentials, mapping{401, "invalid_credentials", "wrong email or password"}},
	{app.ErrTOTPRequired, mapping{401, "totp_required", "send totp_code from your authenticator app"}},
	{app.ErrLinkInvalid, mapping{401, "link_invalid", "the link is unknown, expired or already used"}},
	{app.ErrPasskeyInvalid, mapping{401, "passkey_invalid", "the passkey could not be verified"}},
	{app.ErrEmailUnverified, mapping{403, "email_unverified", "verify your email address with the link we sent"}},
	{app.ErrPersonOnly, mapping{403, "person_required", "only a signed-in person can do this, not an API token"}},
	{domain.ErrOwnerChangeForbidden, mapping{403, "owner_change_forbidden", "only an owner can grant, change or remove the owner role"}},
	{domain.ErrScopeExceedsGrant, mapping{403, "scope_exceeds_grant", "a token can't have scopes beyond what its creator may do"}},
	{app.ErrForbidden, mapping{403, problem.CodeForbidden, "no access to this tenant"}},
	{authz.ErrForbidden, mapping{403, problem.CodeForbidden, ""}},
	// A project outside the caller's scope, or a unit outside an
	// assigned member's assignments, is answered exactly as one that
	// does not exist (RFC 0006 §4.1, §3.3) — the same status, code and
	// detail every context gives its own not-found.
	{authz.ErrNotVisible, mapping{404, problem.CodeNotFound, "no such resource"}},
	{app.ErrNotFound, mapping{404, problem.CodeNotFound, "no such resource"}},
	{domain.ErrInvalidID, mapping{404, problem.CodeNotFound, "no such resource"}},
	{app.ErrPasskeysDisabled, mapping{404, "passkeys_disabled", "passkeys are not configured on this server"}},
	{app.ErrEmailDisabled, mapping{404, "email_disabled", "this server sends no email: sign in with a password or a passkey"}},
	{app.ErrNoPasskeys, mapping{404, "no_passkeys", "no passkeys are registered for this account"}},
	{app.ErrSlugTaken, mapping{409, "slug_taken", "that slug is taken"}},
	{app.ErrDuplicate, mapping{409, "already_member", "that address is already a member or invited"}},
	{app.ErrIdempotencyBusy, mapping{409, "idempotency_key_in_use", "a request with this Idempotency-Key is still in progress"}},
	{domain.ErrIndividualTenant, mapping{409, "individual_tenant", "an individual tenant has exactly one member"}},
	{domain.ErrLastOwner, mapping{409, "last_owner", "a tenant must keep at least one owner"}},
	{domain.ErrTokenRevoked, mapping{409, "token_revoked", "the token is already revoked"}},
	{app.ErrTOTPAlreadyEnabled, mapping{409, "totp_already_enabled", "TOTP is already on"}},
	{app.ErrTOTPNotPending, mapping{409, "totp_not_pending", "start an enrollment first"}},
	{app.ErrTOTPNotEnabled, mapping{409, "totp_not_enabled", "TOTP is not on"}},
	{app.ErrPreconditionFailed, mapping{412, problem.CodePreconditionFailed, "the resource changed; fetch it and retry with its new ETag"}},
	{app.ErrIdempotencyReuse, mapping{422, "idempotency_key_reused", "this Idempotency-Key was used for a different request"}},
	{app.ErrAccountLocked, mapping{429, "account_locked", "too many failed attempts; try again in 15 minutes"}},
	{app.ErrInvalidEmail, mapping{400, "invalid_email", "not a valid email address"}},
	{app.ErrWeakPassword, mapping{400, "weak_password", "use at least 12 characters"}},
	{app.ErrTOTPInvalid, mapping{400, "totp_invalid", "the code is wrong or was already used"}},
	{app.ErrInvalidIdempotencyKey, mapping{400, "invalid_idempotency_key", "Idempotency-Key must be 1-255 printable ASCII characters"}},
	{domain.ErrInvalidLocale, mapping{400, "invalid_locale", ""}},
	{domain.ErrInvalidRole, mapping{400, "invalid_role", ""}},
	{domain.ErrNoRoles, mapping{400, "invalid_role", "at least one role is required"}},
	{domain.ErrLocalesNeedLocaleRole, mapping{400, "locales_need_locale_role", "locales apply only to translators and reviewers"}},
	{domain.ErrInvalidScope, mapping{400, "invalid_scope", ""}},
	{domain.ErrInvalidDisplayName, mapping{400, "invalid_display_name", "at most 200 characters"}},
	{domain.ErrInvalidTokenName, mapping{400, "invalid_token_name", "1-100 characters"}},
	{domain.ErrInvalidTokenExpiry, mapping{400, "invalid_token_expiry", "expires_at must be in the future"}},
	{domain.ErrTooManyProjects, mapping{400, codeInvalidProjectScope, ""}},
	// Restrictions, groups and vendors (RFC 0006 §3.3, §4).
	{domain.ErrInvalidVisibility, mapping{400, "invalid_visibility", ""}},
	{domain.ErrVendorMemberVisibility, mapping{400, "vendor_member_visibility",
		"a vendor's member sees only their assignments: send visibility assigned"}},
	{domain.ErrAssignedVisibilityRole, mapping{400, "assigned_visibility_role",
		"a member who sees only their assignments can only be a translator"}},
	{domain.ErrOwnerProjectScoped, mapping{400, "owner_project_scoped",
		"an owner answers for every project and cannot be limited to some"}},
	{domain.ErrInvalidGroupName, mapping{400, "invalid_group_name", "1-100 characters"}},
	{domain.ErrGroupFull, mapping{409, "group_full", ""}},
	{domain.ErrNotInGroup, mapping{404, "not_in_group", "the member is not in the group"}},
	{domain.ErrMemberOfAnotherTenant, mapping{404, problem.CodeNotFound, "no such resource"}},
	{domain.ErrInvalidVendorName, mapping{400, "invalid_vendor_name", "1-100 characters"}},
	{domain.ErrInvalidVendorContact, mapping{400, "invalid_vendor_contact", "at most 200 characters"}},
	{domain.ErrVendorHasMembers, mapping{409, "vendor_has_members",
		"the vendor still has members: take them off it, or remove them, first"}},
	// In-context grants and preview origins (RFC 0004 §5.2).
	{domain.ErrOriginNotBound, mapping{401, "origin_not_bound",
		"this grant was minted for another origin"}},
	{domain.ErrGrantProjectMismatch, mapping{403, "grant_project_mismatch",
		"this grant was minted for another project"}},
	{domain.ErrPersonGrantOnly, mapping{403, "person_grant_only",
		"only a signed-in person can mint an in-context grant, not an API token"}},
	{domain.ErrOriginNotRegistered, mapping{403, "origin_not_registered",
		"that origin is not a registered preview origin of this project"}},
	{domain.ErrNoInContextPermissions, mapping{403, "no_in_context_permissions",
		"you may not edit this project's text in context"}},
	{domain.ErrOriginRegistered, mapping{409, "origin_registered",
		"that origin is already registered for this project"}},
	{domain.ErrTooManyPreviewOrigins, mapping{409, "too_many_preview_origins",
		"a project keeps at most 20 preview origins"}},
	{domain.ErrInvalidOrigin, mapping{400, "invalid_origin",
		"an origin is a scheme, host and port: https anywhere, http on localhost only"}},
	{domain.ErrInvalidOriginLabel, mapping{400, "invalid_origin_label", "at most 100 characters"}},
	// The GitHub Actions OIDC exchange (RFC 0004 §6.3).
	{domain.ErrInvalidIDToken, mapping{401, "invalid_id_token",
		"the GitHub Actions ID token did not verify: check the audience is glossa and that the job requests it fresh"}},
	{domain.ErrInvalidWorkflowRun, mapping{401, "invalid_id_token",
		"the ID token names no usable workflow run"}},
	{domain.ErrRepositoryNotConnected, mapping{403, "repository_not_connected",
		"no Glossa project is connected to this repository; connect it in Studio → Settings → GitHub"}},
	{domain.ErrAmbiguousProject, mapping{409, "ambiguous_project",
		"this repository feeds several projects: send project_id (or pass --project)"}},
	{domain.ErrGitHubOIDCUnavailable, mapping{503, "github_not_configured",
		"this deployment has no GitHub App, so it cannot exchange GitHub Actions ID tokens; use an API token"}},
	// Device sign-in (RFC 0006 §7.2): RFC 8628 §3.5's codes, unchanged.
	{app.ErrDeviceSignInUnavailable, mapping{503, "device_sign_in_unavailable",
		"this server does not offer device sign-in; use an API token"}},
	{app.ErrDeviceAuthorizationNotFound, mapping{404, "device_authorization_not_found",
		"no pending sign-in with this code: it may have expired or been decided already"}},
	{app.ErrAuthorizationPending, mapping{400, "authorization_pending", "the person has not approved the code yet"}},
	{app.ErrSlowDown, mapping{400, "slow_down", "poll more slowly: add five seconds to the interval"}},
	{app.ErrAccessDenied, mapping{400, "access_denied", "the person denied this sign-in"}},
	{app.ErrExpiredToken, mapping{400, "expired_token", "the device code is unknown, expired or already used; start again"}},
	{app.ErrInvalidClientName, mapping{400, "invalid_request", "client_name is 1-100 characters"}},
	{app.ErrDeviceRateLimited, mapping{429, "rate_limited", "too many device sign-in attempts; wait a minute and try again"}},
	{tenancy.ErrInvalidSlug, mapping{400, "invalid_slug", "lowercase letters, digits and inner hyphens, at most 63"}},
	{tenancy.ErrInvalidName, mapping{400, "invalid_name", "1-200 characters"}},
}

// toProblem turns an error from a handler into problem details. Unknown
// errors become a 500 whose detail reveals nothing; the caller logs them.
func toProblem(err error) (d *problem.Details, known bool) {
	if errors.As(err, &d) {
		return d, true
	}
	for _, p := range problems {
		if errors.Is(err, p.err) {
			detail := p.detail
			if detail == "" {
				detail = err.Error()
				var denied *authz.DeniedError
				if errors.As(err, &denied) {
					detail = "missing permission " + string(denied.Permission)
				}
			}
			return problem.New(p.status, p.code, detail), true
		}
	}
	return problem.New(http.StatusInternalServerError, problem.CodeInternal, "internal error"), false
}

// errorWriter writes problems for the generated server's three error
// hooks and logs what isn't a client error.
type errorWriter struct{ logger *slog.Logger }

func (e errorWriter) write(w http.ResponseWriter, r *http.Request, err error) {
	d, known := toProblem(err)
	if !known {
		e.logger.ErrorContext(r.Context(), "request failed", slog.Any("error", err), slog.String("route", r.Pattern))
	}
	if d.Status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Bearer realm="glossa"`)
	}
	problem.WriteDetails(w, d)
}

// ResponseError handles errors returned by handlers.
func (e errorWriter) ResponseError(w http.ResponseWriter, r *http.Request, err error) {
	e.write(w, r, err)
}

// RequestError handles bodies the generated server couldn't decode.
func (e errorWriter) RequestError(w http.ResponseWriter, _ *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) { // a body without Content-Length ran past the limit
		problem.WriteDetails(w, problem.New(http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge,
			fmt.Sprintf("request body exceeds %d bytes", tooLarge.Limit)))
		return
	}
	problem.WriteDetails(w, problem.New(http.StatusBadRequest, problem.CodeInvalidRequest, err.Error()))
}

// ParamError handles parameters the generated router couldn't bind. A
// missing If-Match is 428, per the concurrency convention.
func (e errorWriter) ParamError(w http.ResponseWriter, _ *http.Request, err error) {
	var missing *apiv1.RequiredHeaderError
	if errors.As(err, &missing) && missing.ParamName == "If-Match" {
		problem.WriteDetails(w, problem.New(http.StatusPreconditionRequired, problem.CodePreconditionRequired,
			"send If-Match with the resource's ETag"))
		return
	}
	problem.WriteDetails(w, problem.New(http.StatusBadRequest, problem.CodeInvalidRequest, err.Error()))
}
