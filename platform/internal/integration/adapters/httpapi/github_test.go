package httpapi

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

func TestWebhookPath(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodPost, "/v1/integrations/github/webhooks", true},
		{http.MethodGet, "/v1/integrations/github/webhooks", false},
		{http.MethodPost, "/v1/integrations/github/webhooks/", false},
		{http.MethodPost, "/v1/integrations/github/webhook", false},
		{http.MethodPost, "/v1/tenants/t1/github/installations", false},
	} {
		if got := WebhookPath(tc.method, tc.path); got != tc.want {
			t.Errorf("WebhookPath(%s %s) = %t", tc.method, tc.path, got)
		}
	}
}

// TestGitHubOperationsSayWhenThereIsNoApp pins the answer of a
// deployment that has registered no GitHub App: every operation says so
// with one code and a 503, instead of failing as an internal error
// (RFC 0004 §6.1, and the configuration is optional by design).
func TestGitHubOperationsSayWhenThereIsNoApp(t *testing.T) {
	a := New(nil, nil)
	ctx := context.Background()
	calls := map[string]func() error{
		"receiveGitHubWebhook": func() error {
			_, err := a.ReceiveGitHubWebhook(ctx, apiv1.ReceiveGitHubWebhookRequestObject{})
			return err
		},
		"startGitHubInstall": func() error {
			_, err := a.StartGitHubInstall(ctx, apiv1.StartGitHubInstallRequestObject{})
			return err
		},
		"completeGitHubInstall": func() error {
			_, err := a.CompleteGitHubInstall(ctx, apiv1.CompleteGitHubInstallRequestObject{})
			return err
		},
		"listGitHubInstallations": func() error {
			_, err := a.ListGitHubInstallations(ctx, apiv1.ListGitHubInstallationsRequestObject{})
			return err
		},
		"forgetGitHubInstallation": func() error {
			_, err := a.ForgetGitHubInstallation(ctx, apiv1.ForgetGitHubInstallationRequestObject{})
			return err
		},
		"createGitConnection": func() error {
			_, err := a.CreateGitConnection(ctx, apiv1.CreateGitConnectionRequestObject{})
			return err
		},
		"listGitConnections": func() error {
			_, err := a.ListGitConnections(ctx, apiv1.ListGitConnectionsRequestObject{})
			return err
		},
		"getGitConnection": func() error {
			_, err := a.GetGitConnection(ctx, apiv1.GetGitConnectionRequestObject{})
			return err
		},
		"updateGitConnection": func() error {
			_, err := a.UpdateGitConnection(ctx, apiv1.UpdateGitConnectionRequestObject{})
			return err
		},
		"deleteGitConnection": func() error {
			_, err := a.DeleteGitConnection(ctx, apiv1.DeleteGitConnectionRequestObject{})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			var d *problem.Details
			if err := call(); !errors.As(err, &d) {
				t.Fatalf("err = %v, want problem details", err)
			} else if d.Status != http.StatusServiceUnavailable || d.Code != "github_not_configured" {
				t.Fatalf("problem = %d %s", d.Status, d.Code)
			}
		})
	}
}

// TestGitHubErrorsMapToTheDocumentedCodes pins the codes openapi.yaml
// publishes. A published code is never renamed.
func TestGitHubErrorsMapToTheDocumentedCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"not configured":       {app.ErrGitHubNotConfigured, http.StatusServiceUnavailable, "github_not_configured"},
		"unknown state":        {app.ErrInstallStateInvalid, http.StatusBadRequest, "invalid_install_state"},
		"someone else's state": {app.ErrInstallStateNotYours, http.StatusForbidden, "install_state_not_yours"},
		"not their install":    {app.ErrInstallationNotVisible, http.StatusForbidden, "installation_not_visible"},
		"already claimed":      {app.ErrInstallationClaimed, http.StatusConflict, "installation_already_claimed"},
		"connection exists":    {app.ErrConnectionExists, http.StatusConflict, "connection_exists"},
		"revoked":              {domain.ErrInstallationRevoked, http.StatusConflict, "installation_revoked"},
		"no such application":  {app.ErrApplicationNotFound, http.StatusNotFound, "application_not_found"},
		"unseen repository":    {app.ErrRepositoryNotVisible, http.StatusNotFound, "repository_not_visible"},
		"invalid connection":   {domain.ErrInvalidConnection, http.StatusBadRequest, "invalid_connection"},
		"bad signature":        {app.ErrWebhookSignature, http.StatusUnauthorized, "invalid_webhook"},
		"missing headers":      {app.ErrWebhookHeaders, http.StatusUnauthorized, "invalid_webhook"},
		"oversized delivery":   {app.ErrWebhookTooLarge, http.StatusRequestEntityTooLarge, "webhook_too_large"},
		"github down":          {app.ErrGitHubUnavailable, http.StatusServiceUnavailable, "github_unavailable"},
		"rate limited":         {app.ErrGitHubRateLimited, http.StatusServiceUnavailable, "github_unavailable"},
		"bad oauth code":       {app.ErrOAuthCodeRejected, http.StatusBadRequest, "invalid_install_state"},
	} {
		t.Run(name, func(t *testing.T) {
			var d *problem.Details
			if err := mapError(tc.err); !errors.As(err, &d) {
				t.Fatalf("err = %v, want problem details", err)
			} else if d.Status != tc.status || string(d.Code) != tc.code {
				t.Fatalf("problem = %d %s, want %d %s", d.Status, d.Code, tc.status, tc.code)
			}
		})
	}
	// An unverified delivery says nothing about why, so an attacker
	// learns nothing from the answer.
	var d *problem.Details
	if err := mapError(app.ErrWebhookSignature); errors.As(err, &d) {
		if got := d.Detail; got != "the delivery could not be verified" {
			t.Fatalf("detail = %q; it must not describe the failure", got)
		}
	}
	// An error nothing maps passes through untouched, so an unexpected
	// failure is a 500 with no invented code.
	unknown := errors.New("something else")
	if got := mapError(unknown); !errors.Is(got, unknown) {
		t.Fatalf("an unmapped error was mapped: %v", got)
	}
}
