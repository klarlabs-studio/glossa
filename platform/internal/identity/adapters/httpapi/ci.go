package httpapi

import (
	"context"
	"errors"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// CI's authentication without a stored secret (RFC 0004 §6.3).

// ExchangeGitHubOIDCToken turns a GitHub Actions ID token into a CI
// token. It is unauthenticated because the ID token is the credential:
// nothing here reads a caller, a header or a network address. The token
// is used once and never logged, stored or echoed back.
func (a *API) ExchangeGitHubOIDCToken(
	ctx context.Context, req apiv1.ExchangeGitHubOIDCTokenRequestObject,
) (apiv1.ExchangeGitHubOIDCTokenResponseObject, error) {
	var project string
	if req.Body.ProjectId != nil {
		project = *req.Body.ProjectId
	}
	m, err := a.svc.ExchangeGitHubOIDC(ctx, req.Body.IdToken, project)
	if err != nil {
		return nil, ambiguityDetails(err)
	}
	t := m.Token
	perms := make([]apiv1.CITokenPermissions, 0, 2)
	for _, p := range t.Permissions.Permissions() {
		perms = append(perms, apiv1.CITokenPermissions(p))
	}
	repo := t.Run.Repository
	return apiv1.ExchangeGitHubOIDCToken201JSONResponse(apiv1.CIToken{
		Token: m.Secret.String(), ExpiresAt: t.ExpiresAt, TenantId: t.TenantID.String(),
		ProjectId: t.ProjectID.String(), Permissions: perms,
		RepositoryId: t.Run.RepositoryID, Repository: &repo,
	}), nil
}

// ambiguityDetails turns an ambiguous repository into problem details
// that name the projects to choose between. It is the one refusal here
// that says more than "no": the run has already proved it is that
// repository, so which of its own projects exist is not news to it, and
// without the list there is nothing for CI to put in `--project`.
func ambiguityDetails(err error) error {
	var ambiguous *app.AmbiguousProjectError
	if !errors.As(err, &ambiguous) {
		return err
	}
	d, _ := toProblem(err)
	for _, c := range ambiguous.Projects {
		detail := c.Project.String()
		if c.Path != "" {
			detail += " (" + c.Path + ")"
		}
		d = d.WithErrors(problem.FieldError{Pointer: "/project_id", Detail: detail})
	}
	return d
}
