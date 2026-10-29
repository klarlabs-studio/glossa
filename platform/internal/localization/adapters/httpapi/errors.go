package httpapi

import (
	"errors"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// problems maps Localization's errors to the codes documented in
// api/openapi.yaml. A code here is part of the contract: never rename
// one. An empty detail means the error's own text.
var problems = []struct {
	err    error
	status int
	code   problem.Code
	detail string
}{
	{app.ErrNotFound, 404, problem.CodeNotFound, "no such resource"},
	{app.ErrLocaleNotFound, 404, "locale_not_found", "the project has no such locale"},
	{domain.ErrSourceLocale, 409, "source_locale", ""},
	{app.ErrLocaleInFallback, 409, "locale_in_fallback", "remove the locale from the fallback graph first"},
	{app.ErrConcurrentWrite, 409, "translation_conflict", "another request created this translation first; fetch it and retry with its ETag"},
	{app.ErrStaleVersion, 409, problem.CodeConflict, "the resource changed concurrently; retry"},
	{domain.ErrTransition, 409, "invalid_transition", ""},
	{app.ErrPreconditionFailed, 412, problem.CodePreconditionFailed, "the resource changed; fetch it and retry with its new ETag"},
	{app.ErrPreconditionRequired, 428, problem.CodePreconditionRequired, "send If-Match with the resource's ETag"},
	{domain.ErrReviewForbidden, 403, "review_forbidden", ""},
	{domain.ErrOwnText, 403, "own_text", "the author of a text cannot approve or reject it; ask another reviewer"},
	{app.ErrTooManyItems, 400, "too_many_items", "send 1 to 500 items"},
	{app.ErrLocaleCount, 400, "too_many_locales", "list 1 to 20 locales"},
	{app.ErrKeyCount, 400, "too_many_keys", "name at most 50 keys"},
	{app.ErrInvalidMessageState, 400, "invalid_message_state", ""},
	{bcp47.ErrInvalid, 400, "invalid_locale", ""},
	{mfcontent.ErrInvalidSyntax, 400, "invalid_syntax", ""},
	{mfcontent.ErrTooLong, 400, "message_too_long", ""},
	{domain.ErrInvalidReviewState, 400, "invalid_state", ""},
	{domain.ErrWriteCannotReject, 400, "write_cannot_reject", ""},
	{domain.ErrInvalidOrigin, 400, "invalid_origin", ""},
	{domain.ErrInvalidOriginInfo, 400, "invalid_origin_detail", ""},
	{domain.ErrInvalidSourceRev, 400, "invalid_source_revision", ""},
}

// mapError turns Localization's errors into problem details; anything
// else (authorization, unexpected failures) passes through.
func mapError(err error) error {
	var (
		invalid  *mfcontent.InvalidError
		fallback *domain.FallbackError
	)
	switch {
	case errors.As(err, &invalid):
		return problem.New(http.StatusBadRequest, "invalid_message", string(invalid.Code)+": "+invalid.Message)
	case errors.As(err, &fallback):
		return problem.New(http.StatusBadRequest, problem.Code(fallback.Code), fallback.Locale+": "+fallback.Detail)
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
