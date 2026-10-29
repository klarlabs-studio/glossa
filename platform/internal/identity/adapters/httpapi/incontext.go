package httpapi

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The in-product editor's authorization (RFC 0004 §5.2): where the
// editor may run, and the grants Studio's popup mints for it.

func projectRef(s string) (domain.ProjectRef, error) { return domain.ParseProjectRef(s) }

func toPreviewOrigin(o domain.PreviewOrigin) apiv1.PreviewOrigin {
	return apiv1.PreviewOrigin{
		Id: o.ID.String(), Origin: o.Origin.String(), Label: o.Label,
		Development: o.Origin.Development(), CreatedBy: o.CreatedBy.String(), CreatedAt: o.CreatedAt,
	}
}

func (a *API) ListPreviewOrigins(
	ctx context.Context, req apiv1.ListPreviewOriginsRequestObject,
) (apiv1.ListPreviewOriginsResponseObject, error) {
	project, err := projectRef(req.Project)
	if err != nil {
		return nil, err
	}
	os, err := a.svc.ListPreviewOrigins(ctx, project)
	if err != nil {
		return nil, err
	}
	out := apiv1.PreviewOriginList{Items: make([]apiv1.PreviewOrigin, len(os))}
	for i, o := range os {
		out.Items[i] = toPreviewOrigin(o)
	}
	return apiv1.ListPreviewOrigins200JSONResponse(out), nil
}

func (a *API) RegisterPreviewOrigin(
	ctx context.Context, req apiv1.RegisterPreviewOriginRequestObject,
) (apiv1.RegisterPreviewOriginResponseObject, error) {
	project, err := projectRef(req.Project)
	if err != nil {
		return nil, err
	}
	var key, label string
	if req.Params.IdempotencyKey != nil {
		key = *req.Params.IdempotencyKey
	}
	if req.Body.Label != nil {
		label = *req.Body.Label
	}
	o, replayed, err := a.svc.RegisterPreviewOrigin(ctx, project, req.Body.Origin, label, key)
	if err != nil {
		return nil, err
	}
	t, _ := tenancy.FromContext(ctx)
	loc := "/v1/tenants/" + t.String() + "/projects/" + project.String() + "/preview-origins/" + o.ID.String()
	h := apiv1.RegisterPreviewOrigin201ResponseHeaders{Location: &loc}
	if replayed {
		yes := "true"
		h.IdempotentReplayed = &yes
	}
	return apiv1.RegisterPreviewOrigin201JSONResponse{Body: toPreviewOrigin(o), Headers: h}, nil
}

func (a *API) UnregisterPreviewOrigin(
	ctx context.Context, req apiv1.UnregisterPreviewOriginRequestObject,
) (apiv1.UnregisterPreviewOriginResponseObject, error) {
	project, err := projectRef(req.Project)
	if err != nil {
		return nil, err
	}
	id, err := domain.ParsePreviewOriginID(req.PreviewOrigin)
	if err != nil {
		return nil, err
	}
	if err := a.svc.UnregisterPreviewOrigin(ctx, project, id); err != nil {
		return nil, err
	}
	return apiv1.UnregisterPreviewOrigin204Response{}, nil
}

// CreateInContextGrant mints the editor's credential. The secret is in
// the response and nowhere else: Studio's popup posts it to the origin
// that asked and forgets it.
func (a *API) CreateInContextGrant(
	ctx context.Context, req apiv1.CreateInContextGrantRequestObject,
) (apiv1.CreateInContextGrantResponseObject, error) {
	project, err := projectRef(req.Project)
	if err != nil {
		return nil, err
	}
	m, err := a.svc.MintInContextGrant(ctx, project, req.Body.Origin)
	if err != nil {
		return nil, err
	}
	return apiv1.CreateInContextGrant201JSONResponse(apiv1.InContextGrant{
		Token: m.Secret.String(), ExpiresAt: m.Grant.ExpiresAt, ProjectId: m.Grant.ProjectID.String(),
		Origin: m.Grant.Origin.String(), PersonId: m.Grant.Person.String(),
		Permissions: toGrantPermissions(m.Grant.Permissions),
	}), nil
}

// toGrantPermissions renders a grant as the list Studio shows before
// minting: each permission with the locales it holds for, sorted so the
// popup reads the same way twice.
func toGrantPermissions(g domain.Grant) []apiv1.InContextPermission {
	perms := g.Permissions()
	out := make([]apiv1.InContextPermission, len(perms))
	for i, p := range perms {
		out[i] = apiv1.InContextPermission{Permission: apiv1.InContextPermissionPermission(p)}
		if scope, ok := g.Locales(p); ok && !scope.All() {
			locales := scope.Strings()
			out[i].Locales = &locales
		}
	}
	return out
}
