// Package errs is glossa-api's typed error registry. Every error
// path in the HTTP layer should pick one of these (or compose with
// WithMessage / WithParam) rather than hand-rolling `gin.H{"error":
// "literal"}`. The wire envelope is defined by
// go.klarlabs.de/glossa/apierr — three audiences (logs,
// curl, i18n clients) in one shape.
package errs

import (
	"net/http"

	"go.klarlabs.de/glossa/apierr"
)

// ─── Authentication / authorization ────────────────────────────────

var (
	AuthInvalidCredentials = apierr.New(
		"auth_invalid_credentials",
		"errors.auth.invalid_credentials",
		"Invalid email or password",
		http.StatusUnauthorized,
	)
	AuthInvalidToken = apierr.New(
		"auth_invalid_token",
		"errors.auth.invalid_token",
		"Invalid or expired token",
		http.StatusUnauthorized,
	)
	AuthInvalidAPIKey = apierr.New(
		"auth_invalid_api_key",
		"errors.auth.invalid_api_key",
		"Invalid API key",
		http.StatusUnauthorized,
	)
	AuthAdminRequired = apierr.New(
		"auth_admin_required",
		"errors.auth.admin_required",
		"Admin role required",
		http.StatusForbidden,
	)
	AuthScopeRequiresAPIKey = apierr.New(
		"auth_scope_requires_api_key",
		"errors.auth.scope_requires_api_key",
		"Scope check requires API-key auth",
		http.StatusForbidden,
	)
)

// ─── Resource lookups ──────────────────────────────────────────────

var (
	ProjectNotFound = apierr.New(
		"project_not_found",
		"errors.project.not_found",
		"Project not found",
		http.StatusNotFound,
	)
	LocaleNotFound = apierr.New(
		"locale_not_found",
		"errors.locale.not_found",
		"Locale not found",
		http.StatusNotFound,
	)
	LocaleNotFoundForProject = apierr.New(
		"locale_not_found_for_project",
		"errors.locale.not_found_for_project",
		"Locale not found for this project",
		http.StatusNotFound,
	)
	KeyNotFoundForProject = apierr.New(
		"key_not_found_for_project",
		"errors.translation_key.not_found_for_project",
		"Key not found for this project",
		http.StatusNotFound,
	)
	UserNotFound = apierr.New(
		"user_not_found",
		"errors.user.not_found",
		"User not found",
		http.StatusNotFound,
	)
	AIProviderNotFound = apierr.New(
		"ai_provider_not_found",
		"errors.ai_provider.not_found",
		"AI provider not found",
		http.StatusNotFound,
	)
)

// ─── Validation ────────────────────────────────────────────────────

var (
	ValidationInvalidID = apierr.New(
		"validation_invalid_id",
		"errors.validation.invalid_id",
		"Invalid ID",
		http.StatusBadRequest,
	)
	ValidationInvalidUserID = apierr.New(
		"validation_invalid_user_id",
		"errors.validation.invalid_user_id",
		"Invalid user ID",
		http.StatusBadRequest,
	)
	ValidationInvalidLocaleID = apierr.New(
		"validation_invalid_locale_id",
		"errors.validation.invalid_locale_id",
		"Invalid locale ID",
		http.StatusBadRequest,
	)
	ValidationTenantIDNotUUID = apierr.New(
		"validation_tenant_id_not_uuid",
		"errors.validation.tenant_id_not_uuid",
		"tenantId must be a UUID",
		http.StatusBadRequest,
	)
	ValidationAPIKeyRequired = apierr.New(
		"validation_api_key_required",
		"errors.validation.api_key_required",
		"apiKey is required",
		http.StatusBadRequest,
	)
)

// ─── Feature gates / configuration ─────────────────────────────────

var (
	AITranslationDisabled = apierr.New(
		"ai_translation_disabled",
		"errors.ai.translation_disabled",
		"AI translation is disabled — configure a provider first",
		http.StatusServiceUnavailable,
	)
)

// ─── Business rules ────────────────────────────────────────────────

var (
	UserLastAdminConflict = apierr.New(
		"user_last_admin_conflict",
		"errors.user.last_admin_conflict",
		"Refuse to delete the last admin",
		http.StatusConflict,
	)
	TranslatorOutOfScopeLocale = apierr.New(
		"translator_out_of_scope_locale",
		"errors.translator.out_of_scope_locale",
		"Translator not scoped to this locale",
		http.StatusForbidden,
	)
)

// ─── Generic catch-alls ────────────────────────────────────────────

// BadRequestFromErr preserves the 400 status while wrapping the
// underlying cause into the canonical envelope. Use when the error
// originates from a value-object constructor or JSON binding that's
// already produced a usable message string.
func BadRequestFromErr(err error) *apierr.Error {
	return apierr.New(
		"bad_request",
		"errors.bad_request",
		"Bad request",
		http.StatusBadRequest,
	).Wrap(err)
}

// UnprocessableFromErr preserves 422 — used when domain validation
// inside a use case rejects an otherwise well-formed request.
func UnprocessableFromErr(err error) *apierr.Error {
	return apierr.New(
		"unprocessable_entity",
		"errors.unprocessable",
		"Unprocessable entity",
		http.StatusUnprocessableEntity,
	).Wrap(err)
}

// InternalFromErr is the 500 wrapper. Carries the underlying cause
// in Message for log readability while keeping the wire shape stable.
func InternalFromErr(err error) *apierr.Error {
	return apierr.New(
		"internal_error",
		"errors.internal",
		"Internal server error",
		http.StatusInternalServerError,
	).Wrap(err)
}
