package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// problems maps Context's errors to the codes documented in
// api/openapi.yaml. A code here is part of the contract: never rename
// one. An empty detail means the error's own text.
var problems = []struct {
	err    error
	status int
	code   problem.Code
	detail string
}{
	{app.ErrProjectNotFound, http.StatusNotFound, problem.CodeNotFound, "no such project"},
	{app.ErrMessageNotFound, http.StatusNotFound, problem.CodeNotFound, "no such message"},
	{app.ErrBuildNotFound, http.StatusNotFound, problem.CodeNotFound, "no such build"},
	{app.ErrCaptureNotFound, http.StatusNotFound, problem.CodeNotFound, "no such capture"},
	{app.ErrNotFound, http.StatusNotFound, problem.CodeNotFound, "no such resource"},
	{app.ErrStorageUnavailable, http.StatusServiceUnavailable, "storage_unavailable", ""},
	{app.ErrApplicationNotFound, http.StatusBadRequest, "unknown_application", "the project has no application with that slug"},
	{app.ErrRateLimited, http.StatusTooManyRequests, problem.CodeRateLimited, ""},
	{app.ErrInvalidQuery, http.StatusBadRequest, "invalid_query", ""},
	{domain.ErrUploadTooLarge, http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge, ""},
	{domain.ErrTooManyUsages, http.StatusBadRequest, "too_many_usages", ""},
	{domain.ErrInvalidUpload, http.StatusBadRequest, "invalid_usages", ""},
	{domain.ErrManifestTooLarge, http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge, ""},
	{domain.ErrTooManyCaptures, http.StatusBadRequest, "too_many_captures", ""},
	{domain.ErrTooManyRegions, http.StatusBadRequest, "too_many_regions", ""},
	{domain.ErrTooManyFindings, http.StatusBadRequest, "too_many_findings", ""},
	{domain.ErrInvalidCaptures, http.StatusBadRequest, "invalid_captures", ""},
	{domain.ErrImageTooLarge, http.StatusRequestEntityTooLarge, "image_too_large", ""},
	// 413 like image_too_large: what the request carries is too much for
	// what may be stored. The detail says how far past the quota it is.
	{app.ErrStorageQuotaExceeded, http.StatusRequestEntityTooLarge, "storage_quota_exceeded", ""},
	{domain.ErrInvalidImage, http.StatusBadRequest, "invalid_image", ""},
	{domain.ErrInvalidSource, http.StatusBadRequest, "invalid_source", ""},
	{domain.ErrInvalidBranch, http.StatusBadRequest, "invalid_branch", ""},
}

// mapError turns Context's errors into problem details; anything else
// (authorization, unexpected failures) passes through.
func mapError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) { // a capture upload ran past its body limit
		return problem.New(http.StatusRequestEntityTooLarge, problem.CodePayloadTooLarge,
			fmt.Sprintf("request body exceeds %d bytes", tooLarge.Limit))
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
