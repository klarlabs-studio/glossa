package httpapi

import (
	"net/http"

	"errors"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// problems maps Integration's errors to the codes documented in
// api/openapi.yaml. A code here is part of the contract: never rename
// one. An empty detail means the error's own text.
var problems = []struct {
	err    error
	status int
	code   problem.Code
	detail string
}{
	{app.ErrNotFound, http.StatusNotFound, problem.CodeNotFound, "no such job"},
	{app.ErrProjectNotFound, http.StatusNotFound, problem.CodeNotFound, "no such project"},
	{app.ErrLocaleNotFound, http.StatusNotFound, "locale_not_found", ""},
	{domain.ErrLocaleNotInProject, http.StatusNotFound, "locale_not_found", ""},
	{app.ErrIdempotencyReuse, http.StatusUnprocessableEntity, "idempotency_key_reused", "this Idempotency-Key was used for a different request"},
	{app.ErrUploadTooLarge, http.StatusRequestEntityTooLarge, "file_too_large", ""},
	{app.ErrEmptyUpload, http.StatusBadRequest, "empty_file", ""},
	{app.ErrUploadInterrupted, http.StatusBadRequest, "upload_interrupted", ""},
	{app.ErrStorage, http.StatusServiceUnavailable, "storage_unavailable", "file storage is unavailable; retry"},
	{app.ErrInvalidQuery, http.StatusBadRequest, "invalid_query", ""},
	{domain.ErrInvalidFormat, http.StatusBadRequest, "invalid_format", ""},
	{domain.ErrNotExportable, http.StatusBadRequest, "invalid_format", "gettext PO is import only"},
	{domain.ErrInvalidMode, http.StatusBadRequest, "invalid_mode", ""},
	{domain.ErrInvalidOptions, http.StatusBadRequest, "invalid_options", ""},
	{domain.ErrProjectRequired, http.StatusBadRequest, "project_required", ""},
	{domain.ErrNotCancellable, http.StatusConflict, "job_not_cancellable", ""},
	{domain.ErrUploadNotExpected, http.StatusConflict, "upload_not_expected", ""},
	{domain.ErrNotReady, http.StatusConflict, "export_not_ready", ""},
	{domain.ErrFileExpired, http.StatusGone, "file_expired", ""},
	{idempotency.ErrInvalidKey, http.StatusBadRequest, "invalid_idempotency_key", ""},

	// GitHub (RFC 0004 §6). github_not_configured is the answer of a
	// deployment that has registered no App: the endpoints exist, and
	// they say so plainly.
	{app.ErrGitHubNotConfigured, http.StatusServiceUnavailable, "github_not_configured", "this deployment has no GitHub App configured"},
	{app.ErrInstallStateInvalid, http.StatusBadRequest, "invalid_install_state", ""},
	{app.ErrInstallStateNotYours, http.StatusForbidden, "install_state_not_yours", ""},
	{app.ErrInstallationNotVisible, http.StatusForbidden, "installation_not_visible", "your GitHub account cannot see that installation"},
	{app.ErrInstallationClaimed, http.StatusConflict, "installation_already_claimed", ""},
	{app.ErrConnectionExists, http.StatusConflict, "connection_exists", ""},
	{domain.ErrInstallationRevoked, http.StatusConflict, "installation_revoked", ""},
	{app.ErrApplicationNotFound, http.StatusNotFound, "application_not_found", ""},
	{app.ErrRepositoryNotVisible, http.StatusNotFound, "repository_not_visible", ""},
	{domain.ErrInvalidConnection, http.StatusBadRequest, "invalid_connection", ""},
	{domain.ErrInvalidInstallation, http.StatusBadRequest, "invalid_connection", ""},
	// A delivery that does not verify is answered without detail, so an
	// attacker learns nothing from the answer.
	{app.ErrWebhookSignature, http.StatusUnauthorized, "invalid_webhook", "the delivery could not be verified"},
	{app.ErrWebhookHeaders, http.StatusUnauthorized, "invalid_webhook", "the delivery could not be verified"},
	{app.ErrWebhookTooLarge, http.StatusRequestEntityTooLarge, "webhook_too_large", ""},
	// GitHub itself failing is not the caller's fault; the install flow
	// is safe to try again.
	{app.ErrGitHubUnavailable, http.StatusServiceUnavailable, "github_unavailable", "GitHub is unavailable; try again"},
	{app.ErrGitHubRateLimited, http.StatusServiceUnavailable, "github_unavailable", "GitHub rate-limited us; try again shortly"},
	{app.ErrGitHubBusy, http.StatusServiceUnavailable, "github_unavailable", "too many GitHub calls are in flight; try again shortly"},
	{app.ErrOAuthCodeRejected, http.StatusBadRequest, "invalid_install_state", "GitHub refused the authorization code"},
	{app.ErrGitHubRejected, http.StatusBadRequest, "invalid_connection", ""},
}

// mapError turns Integration's errors into problem details; anything
// else (authorization, problems already, unexpected failures) passes
// through.
func mapError(err error) error {
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
