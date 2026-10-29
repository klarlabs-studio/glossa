package httpapi

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// WebhookPath reports whether a request is GitHub's webhook delivery
// (POST /v1/integrations/github/webhooks), whose body is read raw and
// capped by the verifier rather than by the API's default limit.
func WebhookPath(method, path string) bool {
	return method == http.MethodPost && path == "/v1/integrations/github/webhooks"
}

// github returns the GitHub service, or the problem that says this
// deployment has none. Every GitHub operation starts here, so a server
// without an App answers one clear code instead of a 500.
func (a *API) github() (*app.GitHubService, error) {
	if a.gh == nil {
		return nil, mapError(app.ErrGitHubNotConfigured)
	}
	return a.gh, nil
}

// ── the webhook endpoint ─────────────────────────────────────────────

// ReceiveGitHubWebhook implements apiv1.StrictServerInterface.
//
// The generated server hands the body over as an unread stream, which
// is what this endpoint needs: the signature is checked over the raw
// bytes, in constant time, before anything parses them.
func (a *API) ReceiveGitHubWebhook(ctx context.Context, req apiv1.ReceiveGitHubWebhookRequestObject) (apiv1.ReceiveGitHubWebhookResponseObject, error) {
	gh, err := a.github()
	if err != nil {
		return nil, err
	}
	h := http.Header{
		"X-Github-Event":      {req.Params.XGitHubEvent},
		"X-Github-Delivery":   {req.Params.XGitHubDelivery},
		"X-Hub-Signature-256": {req.Params.XHubSignature256},
	}
	accepted, err := gh.ReceiveWebhook(ctx, h, req.Body)
	if err != nil {
		return nil, mapError(err)
	}
	// A duplicate is a no-op and still 202: GitHub redelivers, and a
	// redelivery is not an error.
	return apiv1.ReceiveGitHubWebhook202JSONResponse{Accepted: accepted}, nil
}

// ── the install flow ─────────────────────────────────────────────────

// StartGitHubInstall implements apiv1.StrictServerInterface.
func (a *API) StartGitHubInstall(ctx context.Context, _ apiv1.StartGitHubInstallRequestObject) (apiv1.StartGitHubInstallResponseObject, error) {
	gh, err := a.github()
	if err != nil {
		return nil, err
	}
	in, err := gh.StartInstall(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.StartGitHubInstall201JSONResponse{
		State: in.State, InstallUrl: in.InstallURL, ExpiresAt: in.ExpiresAt,
	}, nil
}

// CompleteGitHubInstall implements apiv1.StrictServerInterface.
func (a *API) CompleteGitHubInstall(ctx context.Context, req apiv1.CompleteGitHubInstallRequestObject) (apiv1.CompleteGitHubInstallResponseObject, error) {
	gh, err := a.github()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, mapError(app.ErrInstallStateInvalid)
	}
	inst, err := gh.CompleteInstall(ctx, app.CompleteInstall{
		State: req.Body.State, Code: req.Body.Code, InstallationID: req.Body.InstallationId,
		SetupAction: deref(req.Body.SetupAction),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.CompleteGitHubInstall201JSONResponse{
		Body: toInstallation(app.InstallationView{Installation: inst}),
		Headers: apiv1.CompleteGitHubInstall201ResponseHeaders{
			Location: apiconv.Ptr(tenantPath(ctx, "/github/installations/"+inst.ID.String())),
		},
	}, nil
}

// ListGitHubInstallations implements apiv1.StrictServerInterface.
func (a *API) ListGitHubInstallations(ctx context.Context, req apiv1.ListGitHubInstallationsRequestObject) (apiv1.ListGitHubInstallationsResponseObject, error) {
	gh, err := a.github()
	if err != nil {
		return nil, err
	}
	pg, err := page(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	views, err := gh.Installations(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	// The list is short and comes back whole; the page is cut here so
	// the contract's pagination means what it says.
	items, next := pagination.Trim(views, pg, func(v app.InstallationView) string { return v.ID.String() })
	out := apiv1.ListGitHubInstallations200JSONResponse{
		Items: make([]apiv1.GitHubInstallation, len(items)), NextPageToken: next,
	}
	for i, v := range items {
		out.Items[i] = toInstallation(v)
	}
	return out, nil
}

// ForgetGitHubInstallation implements apiv1.StrictServerInterface.
func (a *API) ForgetGitHubInstallation(ctx context.Context, req apiv1.ForgetGitHubInstallationRequestObject) (apiv1.ForgetGitHubInstallationResponseObject, error) {
	gh, err := a.github()
	if err != nil {
		return nil, err
	}
	id, err := pathID(req.Installation)
	if err != nil {
		return nil, err
	}
	if err := gh.ForgetInstallation(ctx, id); err != nil {
		return nil, mapError(err)
	}
	return apiv1.ForgetGitHubInstallation204Response{}, nil
}

// ── Git connections ──────────────────────────────────────────────────

// CreateGitConnection implements apiv1.StrictServerInterface.
func (a *API) CreateGitConnection(ctx context.Context, req apiv1.CreateGitConnectionRequestObject) (apiv1.CreateGitConnectionResponseObject, error) {
	gh, err := a.github()
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, mapError(domain.ErrInvalidConnection)
	}
	in, err := connectionInput(req.Body.ProjectId, req.Body.ApplicationId, req.Body.RepositoryId,
		req.Body.DefaultBranch, req.Body.Path)
	if err != nil {
		return nil, err
	}
	installation, err := pathID(req.Body.InstallationId)
	if err != nil {
		return nil, err
	}
	c, err := gh.Connect(ctx, app.ConnectRepository{Installation: installation, ConnectionInput: in})
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.CreateGitConnection201JSONResponse{
		Body: toGitConnection(c),
		Headers: apiv1.CreateGitConnection201ResponseHeaders{
			Location: apiconv.Ptr(tenantPath(ctx, "/github/connections/"+c.ID.String())),
		},
	}, nil
}

// ListGitConnections implements apiv1.StrictServerInterface.
func (a *API) ListGitConnections(ctx context.Context, req apiv1.ListGitConnectionsRequestObject) (apiv1.ListGitConnectionsResponseObject, error) {
	gh, err := a.github()
	if err != nil {
		return nil, err
	}
	pg, err := page(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	var f app.ConnectionFilter
	if f.Installation, err = optionalID("installation", req.Params.Installation); err != nil {
		return nil, err
	}
	if f.Project, err = optionalID("project", req.Params.Project); err != nil {
		return nil, err
	}
	conns, err := gh.Connections(ctx, f)
	if err != nil {
		return nil, mapError(err)
	}
	items, next := pagination.Trim(conns, pg, func(c domain.GitConnection) string { return c.ID.String() })
	out := apiv1.ListGitConnections200JSONResponse{
		Items: make([]apiv1.GitConnection, len(items)), NextPageToken: next,
	}
	for i, c := range items {
		out.Items[i] = toGitConnection(c)
	}
	return out, nil
}

// GetGitConnection implements apiv1.StrictServerInterface.
func (a *API) GetGitConnection(ctx context.Context, req apiv1.GetGitConnectionRequestObject) (apiv1.GetGitConnectionResponseObject, error) {
	gh, err := a.github()
	if err != nil {
		return nil, err
	}
	id, err := pathID(req.Connection)
	if err != nil {
		return nil, err
	}
	c, err := gh.Connection(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetGitConnection200JSONResponse{
		Body:    toGitConnection(c),
		Headers: apiv1.GetGitConnection200ResponseHeaders{ETag: apiconv.ETag(connectionVersion)},
	}, nil
}

// UpdateGitConnection implements apiv1.StrictServerInterface.
func (a *API) UpdateGitConnection(ctx context.Context, req apiv1.UpdateGitConnectionRequestObject) (apiv1.UpdateGitConnectionResponseObject, error) {
	gh, err := a.github()
	if err != nil {
		return nil, err
	}
	id, err := pathID(req.Connection)
	if err != nil {
		return nil, err
	}
	if _, err := apiconv.IfMatch(req.Params.IfMatch); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, mapError(domain.ErrInvalidConnection)
	}
	in, err := connectionInput(req.Body.ProjectId, req.Body.ApplicationId, 0, &req.Body.DefaultBranch, req.Body.Path)
	if err != nil {
		return nil, err
	}
	c, err := gh.ChangeConnection(ctx, id, in)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.UpdateGitConnection200JSONResponse{
		Body:    toGitConnection(c),
		Headers: apiv1.UpdateGitConnection200ResponseHeaders{ETag: apiconv.ETag(connectionVersion)},
	}, nil
}

// DeleteGitConnection implements apiv1.StrictServerInterface.
func (a *API) DeleteGitConnection(ctx context.Context, req apiv1.DeleteGitConnectionRequestObject) (apiv1.DeleteGitConnectionResponseObject, error) {
	gh, err := a.github()
	if err != nil {
		return nil, err
	}
	id, err := pathID(req.Connection)
	if err != nil {
		return nil, err
	}
	if err := gh.Disconnect(ctx, id); err != nil {
		return nil, mapError(err)
	}
	return apiv1.DeleteGitConnection204Response{}, nil
}

// ── converters ───────────────────────────────────────────────────────

// connectionVersion is a Git connection's ETag. A connection has no
// history and no concurrent editor worth arbitrating, so the contract
// pins it at 0 and If-Match only has to be an ETag this API issued.
const connectionVersion = 0

func connectionInput(project, application apiv1.Id, repository int64, branch, path *string) (domain.ConnectionInput, error) {
	projectID, err := pathID(project)
	if err != nil {
		return domain.ConnectionInput{}, mapError(domain.ErrInvalidConnection)
	}
	applicationID, err := pathID(application)
	if err != nil {
		return domain.ConnectionInput{}, mapError(domain.ErrInvalidConnection)
	}
	return domain.ConnectionInput{
		RepositoryID: repository, ProjectID: projectID, ApplicationID: applicationID,
		DefaultBranch: strings.TrimSpace(deref(branch)), Path: deref(path),
	}, nil
}

func toInstallation(v app.InstallationView) apiv1.GitHubInstallation {
	out := apiv1.GitHubInstallation{
		Id:                      v.ID.String(),
		InstallationId:          v.GitHubID,
		AccountLogin:            v.AccountLogin,
		AccountType:             apiv1.GitHubInstallationAccountType(v.AccountType),
		State:                   apiv1.GitHubInstallationState(v.State),
		ConnectedBy:             nonEmpty(v.ConnectedBy),
		ConnectedAt:             v.ConnectedAt,
		RepositoriesUnavailable: v.RepositoriesUnavailable || !v.Usable(),
	}
	if len(v.Repositories) > 0 {
		repos := make([]apiv1.GitHubRepository, len(v.Repositories))
		for i, r := range v.Repositories {
			repos[i] = apiv1.GitHubRepository{
				RepositoryId: r.ID, Name: r.Name, FullName: r.FullName,
				Private: r.Private, DefaultBranch: r.DefaultBranch,
			}
		}
		slices.SortFunc(repos, func(a, b apiv1.GitHubRepository) int { return strings.Compare(a.FullName, b.FullName) })
		out.Repositories = &repos
	}
	return out
}

func toGitConnection(c domain.GitConnection) apiv1.GitConnection {
	return apiv1.GitConnection{
		Id:             c.ID.String(),
		InstallationId: c.InstallationID.String(),
		RepositoryId:   c.RepositoryID,
		RepositoryName: c.RepositoryName,
		ProjectId:      c.ProjectID.String(),
		ApplicationId:  c.ApplicationID.String(),
		DefaultBranch:  c.DefaultBranch,
		Path:           c.Path,
		CreatedBy:      nonEmpty(c.CreatedBy),
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      apiconv.Ptr(c.UpdatedAt),
		Version:        connectionVersion,
	}
}
