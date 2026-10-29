// Package httpapi is the Context context's HTTP edge: its operations of
// the generated /v1 strict server — usage uploads (builds), a message's
// current usages, the usages on a route, in a component or in a file,
// and the unused messages. The composition root embeds API next to the
// other contexts' handlers; Identity's Guard has authenticated the
// caller and resolved the tenant before any of these run.
package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/jcs"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// API serves Context's operations.
type API struct{ svc *app.Service }

// New returns the API.
func New(svc *app.Service) *API { return &API{svc: svc} }

// UploadPath reports whether a request is a usage upload
// (POST /v1/tenants/{tenant}/projects/{project}/context-builds), whose
// body may reach 20 MB: the composition root lifts the body limit there.
func UploadPath(method, path string) bool {
	s := strings.Split(path, "/")
	return method == http.MethodPost && len(s) == 7 && s[0] == "" && s[1] == "v1" && s[2] == "tenants" && s[3] != "" &&
		s[4] == "projects" && s[5] != "" && s[6] == "context-builds"
}

// projectID parses a project ID; a malformed one is simply not found.
func projectID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, mapError(app.ErrProjectNotFound)
	}
	return id, nil
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// ── builds ──────────────────────────────────────────────────────────

func toBuild(b app.BuildRecord) apiv1.ContextBuild {
	return apiv1.ContextBuild{
		Id: b.ID.String(), ApplicationId: b.ApplicationID.String(), Commit: b.Commit.String(), Branch: b.Branch.String(),
		OnDefaultBranch: b.OnDefaultBranch, Source: apiv1.ContextSource(b.Source),
		Tool: apiv1.UsagesTool{Name: b.Tool.Name, Version: b.Tool.Version}, Digest: b.Digest.String(),
		Usages: b.UsageCount, UnknownKeys: b.UnknownKeys, CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt,
	}
}

// CreateContextBuild ingests a glossa.usages/v1 document. The generated
// server decoded the body into the contract's shape, which drops the
// members the schema doesn't define; the domain validates and digests
// its RFC 8785 canonical form.
func (a *API) CreateContextBuild(ctx context.Context, req apiv1.CreateContextBuildRequestObject) (apiv1.CreateContextBuildResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	doc, err := jcs.Marshal(req.Body)
	if err != nil {
		return nil, err
	}
	out, err := a.svc.IngestUsages(ctx, app.IngestUsages{Project: project, Source: string(req.Params.Source), Document: doc})
	if err != nil {
		return nil, mapError(err)
	}
	b := toBuild(app.BuildRecord{Build: out.Build, UnknownKeys: out.UnknownKeys})
	if out.Replayed {
		return apiv1.CreateContextBuild200JSONResponse{
			Body: b, Headers: apiv1.CreateContextBuild200ResponseHeaders{IdempotentReplayed: apiconv.Ptr("true")},
		}, nil
	}
	return apiv1.CreateContextBuild201JSONResponse(b), nil
}

func (a *API) ListContextBuilds(ctx context.Context, req apiv1.ListContextBuildsRequestObject) (apiv1.ListContextBuildsResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	builds, next, err := a.svc.ListBuilds(ctx, project, app.BuildQuery{Application: deref(req.Params.Application)}, page)
	if err != nil {
		return nil, mapError(err)
	}
	out := apiv1.ListContextBuilds200JSONResponse{Items: make([]apiv1.ContextBuild, len(builds)), NextPageToken: next}
	for i, b := range builds {
		out.Items[i] = toBuild(b)
	}
	return out, nil
}

// ── usages ──────────────────────────────────────────────────────────

func toUsage(u app.UsageView) apiv1.ContextUsage {
	out := apiv1.ContextUsage{
		Key: u.Key, File: u.File, Line: u.Line, Kind: apiv1.UsageKind(u.Kind), BuildId: u.BuildID.String(),
		ApplicationId: u.ApplicationID.String(), Commit: u.Commit.String(), Branch: u.Branch.String(),
		OnDefaultBranch: u.OnDefaultBranch, Source: apiv1.ContextSource(u.Source),
	}
	if u.MessageID != nil {
		out.MessageId = apiconv.Ptr(u.MessageID.String())
	}
	if u.Column > 0 {
		out.Column = apiconv.Ptr(u.Column)
	}
	if u.Component != "" {
		out.Component = apiconv.Ptr(u.Component)
	}
	if u.Route != "" {
		out.Route = apiconv.Ptr(u.Route)
	}
	return out
}

func toUsages(us []app.UsageView) []apiv1.ContextUsage {
	out := make([]apiv1.ContextUsage, len(us))
	for i, u := range us {
		out[i] = toUsage(u)
	}
	return out
}

func (a *API) ListMessageUsages(ctx context.Context, req apiv1.ListMessageUsagesRequestObject) (apiv1.ListMessageUsagesResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	got, err := a.svc.UsagesOfKey(ctx, project, req.Message, app.UsageQuery{
		Branch: deref(req.Params.Branch), Limit: deref(req.Params.Limit),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.ListMessageUsages200JSONResponse{
		MessageId: got.MessageID.String(), Key: req.Message, Usages: toUsages(got.Usages), Truncated: got.Truncated,
	}, nil
}

func (a *API) ListUsages(ctx context.Context, req apiv1.ListUsagesRequestObject) (apiv1.ListUsagesResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	p := req.Params
	page, err := pagination.Parse(p.PageSize, p.PageToken)
	if err != nil {
		return nil, err
	}
	got, err := a.svc.ListUsages(ctx, project, deref(p.Branch), app.UsageFilter{
		Route: deref(p.Route), Component: deref(p.Component), File: deref(p.File),
	}, page)
	if err != nil {
		return nil, mapError(err)
	}
	return apiv1.ListUsages200JSONResponse{Items: toUsages(got.Usages), NextPageToken: got.Next}, nil
}

// ListUnusedMessages pages through the unused messages by key; the
// service computes them for the whole project (one read of its active
// messages and of the current builds' used IDs).
func (a *API) ListUnusedMessages(ctx context.Context, req apiv1.ListUnusedMessagesRequestObject) (apiv1.ListUnusedMessagesResponseObject, error) {
	project, err := projectID(req.Project)
	if err != nil {
		return nil, err
	}
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	unused, err := a.svc.UnusedMessages(ctx, project, deref(req.Params.Branch))
	if err != nil {
		return nil, mapError(err)
	}
	var rows []app.MessageRef
	for _, m := range unused.Messages {
		if m.Key > page.After && len(rows) < page.Limit() {
			rows = append(rows, m)
		}
	}
	items, next := pagination.Trim(rows, page, func(m app.MessageRef) string { return m.Key })
	out := apiv1.ListUnusedMessages200JSONResponse{
		Items: make([]apiv1.UnusedMessage, len(items)), NextPageToken: next, CurrentBuilds: unused.CurrentBuilds,
		ActiveMessages: unused.Active, UnusedMessages: len(unused.Messages),
	}
	for i, m := range items {
		out.Items[i] = apiv1.UnusedMessage{Id: m.ID.String(), Key: m.Key}
	}
	return out, nil
}
