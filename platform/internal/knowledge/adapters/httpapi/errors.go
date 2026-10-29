package httpapi

import (
	"errors"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// problems maps Knowledge's errors to the codes documented in
// api/openapi.yaml. A code here is part of the contract: never rename
// one. An empty detail means the error's own text.
var problems = []struct {
	err    error
	status int
	code   problem.Code
	detail string
}{
	{app.ErrNotFound, 404, problem.CodeNotFound, "no such resource"},
	{app.ErrProjectNotFound, 400, "unknown_project", "project_id names no project of this tenant"},
	{app.ErrStyleGuideExists, 409, "style_guide_exists", "this scope has a style guide; replace it instead"},
	{app.ErrStaleVersion, 409, problem.CodeConflict, "the resource changed concurrently; retry"},
	{app.ErrPreconditionFailed, 412, problem.CodePreconditionFailed, "the resource changed; fetch it and retry with its new ETag"},
	{app.ErrPreconditionRequired, 428, problem.CodePreconditionRequired, "send If-Match with the resource's ETag"},
	{app.ErrIdempotencyReuse, 422, "idempotency_key_reused", "this Idempotency-Key was used for a different request"},
	{app.ErrInvalidQuery, 400, "invalid_query", ""},
	{app.ErrLocaleCount, 400, "too_many_locales", "check 1 to 20 locales"},
	{app.ErrKeyCount, 400, "too_many_keys", "name at most 50 keys"},
	{app.ErrInvalidState, 400, "invalid_state", ""},
	{domain.ErrInvalidConcept, 400, "invalid_concept", ""},
	{domain.ErrInvalidTermStatus, 400, "invalid_term_status", ""},
	{domain.ErrInvalidPartOfSpeech, 400, "invalid_part_of_speech", ""},
	{domain.ErrDuplicateTerm, 400, "duplicate_term", ""},
	{domain.ErrInvalidTerm, 400, "invalid_term", ""},
	{domain.ErrNamespaceScope, 400, "namespace_needs_project", ""},
	{domain.ErrInvalidStyleRule, 400, "invalid_style_rule", ""},
	{domain.ErrInvalidStyleGuide, 400, "invalid_style_guide", ""},
	{idempotency.ErrInvalidKey, 400, "invalid_idempotency_key", ""},
	{bcp47.ErrInvalid, 400, "invalid_locale", ""},
	{mfcontent.ErrInvalidSyntax, 400, "invalid_syntax", ""},
	{mfcontent.ErrTooLong, 400, "message_too_long", ""},
}

// mapError turns Knowledge's errors into problem details; anything else
// (authorization, unexpected failures) passes through.
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
