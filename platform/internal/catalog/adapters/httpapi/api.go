// Package httpapi is Catalog's HTTP edge: its operations of the
// generated /v1 strict server (projects, applications, messages). The
// composition root embeds API next to the other contexts' handlers;
// Identity's Guard has authenticated the caller and resolved the tenant
// before any of these run.
package httpapi

import (
	"context"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// API serves Catalog's operations.
type API struct{ svc *app.Service }

// New returns the API.
func New(svc *app.Service) *API { return &API{svc: svc} }

func projectPath(ctx context.Context, project domain.ProjectID, sub string) string {
	t, _ := tenancy.FromContext(ctx)
	return "/v1/tenants/" + t.String() + "/projects/" + project.String() + sub
}

func projectID(s string) (domain.ProjectID, error) {
	id, err := domain.ParseProjectID(s)
	return id, mapError(err)
}

// ── projects ────────────────────────────────────────────────────────

func toProject(p domain.Project) apiv1.Project {
	return apiv1.Project{
		Id: p.ID.String(), Slug: string(p.Slug), Name: p.Name, SourceLocale: p.SourceLocale.String(),
		Settings: apiv1.ProjectSettings{
			DefaultSyntax: apiv1.Syntax(p.Settings.DefaultSyntax), ReviewRequired: p.Settings.ReviewRequired,
			DefaultBranch: apiconv.Ptr(string(p.Settings.DefaultBranch)),
			CheckPolicy:   apiconv.Ptr(toCheckPolicy(p.Settings.Policy())),
		},
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// toCheckPolicy renders the policy the wire's way: the three modes of
// require_complete spelled out, where the domain has nil for "all" and
// an empty slice for "none".
func toCheckPolicy(p checkpolicy.Policy) apiv1.CheckPolicy {
	out := apiv1.CheckPolicy{
		RequireComplete:     apiv1.CheckPolicyRequireCompleteAll,
		FailOn:              apiv1.CheckPolicyFailOn(orDefault(p.FailOn, checkpolicy.Error)),
		MissingTranslations: apiv1.CheckPolicyMissingTranslations(orDefault(p.MissingTranslations, checkpolicy.Error)),
		Locales:             apiconv.Ptr([]apiv1.Locale{}),
	}
	switch {
	case p.RequireComplete == nil:
	case len(p.RequireComplete) == 0:
		out.RequireComplete = apiv1.CheckPolicyRequireCompleteNone
	default:
		out.RequireComplete = apiv1.CheckPolicyRequireCompleteListed
		out.Locales = apiconv.Ptr(append([]apiv1.Locale{}, p.RequireComplete...))
	}
	return out
}

func orDefault(s, fallback checkpolicy.Severity) checkpolicy.Severity {
	if s == "" {
		return fallback
	}
	return s
}

// fromCheckPolicy reads a policy from a request. `listed` with no
// locales is `none`: an empty list of locales that must be complete is
// exactly no locale having to be.
func fromCheckPolicy(p *apiv1.CheckPolicy) (*checkpolicy.Policy, error) {
	if p == nil {
		return nil, nil
	}
	out := checkpolicy.Policy{
		FailOn: checkpolicy.Severity(p.FailOn), MissingTranslations: checkpolicy.Severity(p.MissingTranslations),
	}
	switch p.RequireComplete {
	case apiv1.CheckPolicyRequireCompleteAll:
	case apiv1.CheckPolicyRequireCompleteNone:
		out.RequireComplete = []string{}
	case apiv1.CheckPolicyRequireCompleteListed:
		out.RequireComplete = []string{}
		if p.Locales != nil {
			for _, l := range *p.Locales {
				tag, err := bcp47.Parse(l)
				if err != nil {
					return nil, mapError(err)
				}
				out.RequireComplete = append(out.RequireComplete, tag.String())
			}
		}
	default:
		return nil, problem.New(http.StatusBadRequest, "invalid_check_policy",
			"require_complete is all, listed or none")
	}
	return &out, nil
}

// fromSettings reads settings from a request. An absent default_branch
// or check_policy keeps the project's (a new project gets main and the
// default policy); an empty default branch is invalid.
func fromSettings(s *apiv1.ProjectSettings) (*domain.Settings, error) {
	if s == nil {
		return nil, nil
	}
	out := &domain.Settings{DefaultSyntax: mfcontent.Syntax(s.DefaultSyntax), ReviewRequired: s.ReviewRequired}
	if s.DefaultBranch != nil {
		b, err := domain.ParseBranchName(*s.DefaultBranch)
		if err != nil {
			return nil, mapError(err)
		}
		out.DefaultBranch = b
	}
	policy, err := fromCheckPolicy(s.CheckPolicy)
	if err != nil {
		return nil, err
	}
	out.CheckPolicy = policy
	return out, nil
}

func (a *API) ListProjects(ctx context.Context, req apiv1.ListProjectsRequestObject) (apiv1.ListProjectsResponseObject, error) {
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	ps, next, err := a.svc.ListProjects(ctx, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListProjects200JSONResponse{Items: make([]apiv1.Project, len(ps)), NextPageToken: next}
	for i, p := range ps {
		out.Items[i] = toProject(p)
	}
	return out, nil
}

func (a *API) CreateProject(ctx context.Context, req apiv1.CreateProjectRequestObject) (apiv1.CreateProjectResponseObject, error) {
	var key string
	if req.Params.IdempotencyKey != nil {
		key = *req.Params.IdempotencyKey
	}
	settings, err := fromSettings(req.Body.Settings)
	if err != nil {
		return nil, err
	}
	p, replayed, err := a.svc.CreateProject(ctx, app.NewProject{
		Slug: req.Body.Slug, Name: req.Body.Name, SourceLocale: req.Body.SourceLocale, Settings: settings,
	}, key)
	if err != nil {
		return nil, mapError(err)
	}
	h := apiv1.CreateProject201ResponseHeaders{ETag: apiconv.ETag(p.Version), Location: apiconv.Ptr(projectPath(ctx, p.ID, ""))}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return apiv1.CreateProject201JSONResponse{Body: toProject(p), Headers: h}, nil
}

func (a *API) GetProject(ctx context.Context, req apiv1.GetProjectRequestObject) (apiv1.GetProjectResponseObject, error) {
	id, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	p, err := a.svc.GetProject(ctx, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetProject200JSONResponse{Body: toProject(p), Headers: apiv1.GetProject200ResponseHeaders{ETag: apiconv.ETag(p.Version)}}, nil
}

func (a *API) UpdateProject(ctx context.Context, req apiv1.UpdateProjectRequestObject) (apiv1.UpdateProjectResponseObject, error) {
	id, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.IfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	settings, err := fromSettings(req.Body.Settings)
	if err != nil {
		return nil, err
	}
	c := domain.ProjectChange{Name: req.Body.Name, Settings: settings}
	if req.Body.Slug != nil {
		slug, err := domain.ParseSlug(*req.Body.Slug)
		if err != nil {
			return nil, mapError(err)
		}
		c.Slug = &slug
	}
	p, err := a.svc.UpdateProject(ctx, id, ifMatch, c)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.UpdateProject200JSONResponse{Body: toProject(p), Headers: apiv1.UpdateProject200ResponseHeaders{ETag: apiconv.ETag(p.Version)}}, nil
}

func (a *API) DeleteProject(ctx context.Context, req apiv1.DeleteProjectRequestObject) (apiv1.DeleteProjectResponseObject, error) {
	id, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := a.svc.DeleteProject(ctx, id, ifMatch); err != nil {
		return nil, mapError(err)
	}
	return apiv1.DeleteProject204Response{}, nil
}

// ── applications ────────────────────────────────────────────────────

func toApplication(x domain.Application) apiv1.Application {
	return apiv1.Application{
		Id: x.ID.String(), Slug: string(x.Slug), Name: x.Name, Platform: apiv1.Platform(x.Platform),
		CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt,
	}
}

func applicationID(s string) (domain.ApplicationID, error) {
	id, err := domain.ParseApplicationID(s)
	return id, mapError(err)
}

func (a *API) ListApplications(ctx context.Context, req apiv1.ListApplicationsRequestObject) (apiv1.ListApplicationsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	xs, next, err := a.svc.ListApplications(ctx, project, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListApplications200JSONResponse{Items: make([]apiv1.Application, len(xs)), NextPageToken: next}
	for i, x := range xs {
		out.Items[i] = toApplication(x)
	}
	return out, nil
}

func (a *API) CreateApplication(ctx context.Context, req apiv1.CreateApplicationRequestObject) (apiv1.CreateApplicationResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	var key string
	if req.Params.IdempotencyKey != nil {
		key = *req.Params.IdempotencyKey
	}
	x, replayed, err := a.svc.CreateApplication(ctx, project, app.NewApplication{
		Slug: req.Body.Slug, Name: req.Body.Name, Platform: string(req.Body.Platform),
	}, key)
	if err != nil {
		return nil, mapError(err)
	}
	h := apiv1.CreateApplication201ResponseHeaders{
		ETag: apiconv.ETag(x.Version), Location: apiconv.Ptr(projectPath(ctx, project, "/applications/"+x.ID.String())),
	}
	if replayed {
		h.IdempotentReplayed = apiconv.Ptr("true")
	}
	return apiv1.CreateApplication201JSONResponse{Body: toApplication(x), Headers: h}, nil
}

func (a *API) GetApplication(ctx context.Context, req apiv1.GetApplicationRequestObject) (apiv1.GetApplicationResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := applicationID(req.Application)
	if err != nil {
		return nil, err
	}
	x, err := a.svc.GetApplication(ctx, project, id)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.GetApplication200JSONResponse{Body: toApplication(x), Headers: apiv1.GetApplication200ResponseHeaders{ETag: apiconv.ETag(x.Version)}}, nil
}

func (a *API) UpdateApplication(ctx context.Context, req apiv1.UpdateApplicationRequestObject) (apiv1.UpdateApplicationResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := applicationID(req.Application)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.IfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	c := domain.ApplicationChange{Name: req.Body.Name}
	if req.Body.Slug != nil {
		slug, err := domain.ParseSlug(*req.Body.Slug)
		if err != nil {
			return nil, mapError(err)
		}
		c.Slug = &slug
	}
	if req.Body.Platform != nil {
		p := domain.Platform(*req.Body.Platform)
		c.Platform = &p
	}
	x, err := a.svc.UpdateApplication(ctx, project, id, ifMatch, c)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.UpdateApplication200JSONResponse{Body: toApplication(x), Headers: apiv1.UpdateApplication200ResponseHeaders{ETag: apiconv.ETag(x.Version)}}, nil
}

func (a *API) DeleteApplication(ctx context.Context, req apiv1.DeleteApplicationRequestObject) (apiv1.DeleteApplicationResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := applicationID(req.Application)
	if err != nil {
		return nil, err
	}
	ifMatch, err := apiconv.OptionalIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if err := a.svc.DeleteApplication(ctx, project, id, ifMatch); err != nil {
		return nil, mapError(err)
	}
	return apiv1.DeleteApplication204Response{}, nil
}
