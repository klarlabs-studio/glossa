package httpapi

import (
	"errors"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// problems maps Catalog's errors to the codes documented in
// api/openapi.yaml. A code here is part of the contract: never rename
// one. An empty detail means the error's own text.
var problems = []struct {
	err    error
	status int
	code   problem.Code
	detail string
}{
	{app.ErrNotFound, 404, problem.CodeNotFound, "no such resource"},
	{domain.ErrInvalidID, 404, problem.CodeNotFound, "no such resource"},
	{app.ErrSlugTaken, 409, "slug_taken", "that slug is taken"},
	{app.ErrKeyTaken, 409, "key_taken", "a message with that key exists"},
	{app.ErrIdempotencyBusy, 409, "idempotency_key_in_use", "a request with this Idempotency-Key is still in progress"},
	{app.ErrStaleVersion, 409, problem.CodeConflict, "the resource changed concurrently; retry"},
	{domain.ErrMessageObsolete, 409, "message_obsolete", "the message is obsolete; push it again to reactivate it"},
	{app.ErrPreconditionFailed, 412, problem.CodePreconditionFailed, "the resource changed; fetch it and retry with its new ETag"},
	{app.ErrIdempotencyReuse, 422, "idempotency_key_reused", "this Idempotency-Key was used for a different request"},
	{app.ErrTooManyItems, 400, "too_many_items", "send 1 to 500 items"},
	{app.ErrTooManyBranchItems, 400, "too_many_branch_items", "a branch push holds 1 to 10000 items"},
	{app.ErrInvalidBranchState, 400, "invalid_branch_state", "state is open, merged or closed"},
	{domain.ErrBranchMerged, 409, "branch_merged", "the branch is merged; it takes no more pushes"},
	{domain.ErrBranchTransition, 409, problem.CodeConflict, ""},
	{domain.ErrInvalidPush, 400, "invalid_push", ""},
	{domain.ErrInvalidPreviewURL, 400, "invalid_preview_url", ""},
	{app.ErrCoverageFilter, 400, "coverage_filter_conflict", "use missing_in or outdated_in, not both"},
	{app.ErrNoCoverage, 503, problem.CodeUnavailable, "translation coverage is not available"},
	{idempotency.ErrInvalidKey, 400, "invalid_idempotency_key", ""},
	{checkpolicy.ErrInvalidSeverity, 400, "invalid_check_policy", ""},
	{checkpolicy.ErrUnknownLocale, 400, "invalid_check_policy", ""},
	{bcp47.ErrInvalid, 400, "invalid_locale", ""},
	{mfcontent.ErrInvalidSyntax, 400, "invalid_syntax", ""},
	{mfcontent.ErrTooLong, 400, "message_too_long", ""},
	{domain.ErrInvalidSlug, 400, "invalid_slug", ""},
	{domain.ErrInvalidBranchName, 400, "invalid_branch", ""},
	{domain.ErrInvalidName, 400, "invalid_name", ""},
	{domain.ErrInvalidPlatform, 400, "invalid_platform", ""},
	{domain.ErrInvalidKey, 400, "invalid_message_key", ""},
	{domain.ErrInvalidNamespace, 400, "invalid_namespace", ""},
	{domain.ErrInvalidDescription, 400, "invalid_details", ""},
	{domain.ErrInvalidMaxLength, 400, "invalid_details", ""},
}

// mapError turns Catalog's errors into problem details. Anything else
// (authorization, unexpected failures) passes through to the shared
// error hook.
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
