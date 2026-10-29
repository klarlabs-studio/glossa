package httpapi

import (
	"errors"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// problems maps Intelligence's errors to the codes documented in
// api/openapi.yaml. A code here is part of the contract: never rename
// one. An empty detail means the error's own text.
var problems = []struct {
	err    error
	status int
	code   problem.Code
	detail string
}{
	{app.ErrNotFound, 404, problem.CodeNotFound, "no such resource"},
	{app.ErrProjectNotFound, 404, problem.CodeNotFound, "no such project"},
	{app.ErrLocaleNotFound, 404, "locale_not_found", ""},
	{app.ErrProviderNameTaken, 409, "provider_name_taken", "a provider with this name exists"},
	{app.ErrProviderInUse, 409, "provider_in_use", ""},
	{app.ErrStaleVersion, 409, problem.CodeConflict, "the resource changed concurrently; retry"},
	{app.ErrPreconditionFailed, 412, problem.CodePreconditionFailed, "the resource changed; fetch it and retry with its new ETag"},
	{app.ErrPreconditionRequired, 428, problem.CodePreconditionRequired, "send If-Match with the resource's ETag"},
	{app.ErrIdempotencyReuse, 422, "idempotency_key_reused", "this Idempotency-Key was used for a different request"},
	{app.ErrAutoApproveIneligible, 422, "auto_approve_ineligible", ""},
	{app.ErrTranslationConflict, 409, "translation_conflict", ""},
	{app.ErrTranslationRejected, 422, "translation_rejected", ""},
	{app.ErrTooManyLocales, 400, "too_many_locales", ""},
	{app.ErrTooManyKeys, 400, "too_many_keys", ""},
	{app.ErrInvalidQuery, 400, "invalid_query", ""},
	{domain.ErrJobNotCancellable, 409, "job_not_cancellable", ""},
	{domain.ErrSuggestionDecided, 409, "suggestion_decided", ""},
	{domain.ErrSuggestionOutdated, 409, "suggestion_outdated", ""},
	{domain.ErrInvalidProvider, 400, "invalid_provider", ""},
	{domain.ErrInvalidRouting, 400, "invalid_routing_policy", ""},
	{domain.ErrInvalidPrices, 400, "invalid_prices", ""},
	{domain.ErrInvalidSettings, 400, "invalid_settings", ""},
	{domain.ErrInvalidNamespaceTags, 400, "invalid_namespace_tags", ""},
	{domain.ErrInvalidReviewPolicy, 400, "invalid_review_policy", ""},
	{idempotency.ErrInvalidKey, 400, "invalid_idempotency_key", ""},
	{bcp47.ErrInvalid, 400, "invalid_locale", ""},
	{mfcontent.ErrInvalidSyntax, 400, "invalid_syntax", ""},
	{mfcontent.ErrTooLong, 400, "message_too_long", ""},
}

// mapError turns Intelligence's errors into problem details; anything
// else (authorization, problems already, unexpected failures) passes
// through.
func mapError(err error) error {
	var invalid *mfcontent.InvalidError
	if errors.As(err, &invalid) {
		return problem.New(http.StatusBadRequest, "invalid_message", string(invalid.Code)+": "+invalid.Message)
	}
	for _, p := range problems {
		if errors.Is(err, p.err) {
			detail := p.detail
			if detail == "" {
				detail = err.Error()
			}
			return problem.New(p.status, p.code, detail)
		}
	}
	return err
}
