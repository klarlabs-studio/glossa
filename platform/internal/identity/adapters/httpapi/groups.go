package httpapi

import (
	"context"
	"errors"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// Groups and vendors (RFC 0006 §3.3, §4.3): who people are, so that
// assignments and approvals can name them without naming persons.

func toGroup(g domain.Group) apiv1.Group {
	members := make([]string, len(g.Members))
	for i, m := range g.Members {
		members[i] = m.String()
	}
	return apiv1.Group{Id: g.ID.String(), Name: g.Name, Members: members, CreatedAt: g.CreatedAt.UTC(), UpdatedAt: g.UpdatedAt.UTC()}
}

func toVendor(v domain.Vendor) apiv1.Vendor {
	out := apiv1.Vendor{Id: v.ID.String(), Name: v.Name, Locales: v.LocaleStrings(), CreatedAt: v.CreatedAt.UTC(), UpdatedAt: v.UpdatedAt.UTC()}
	if v.Contact != "" {
		out.Contact = ptr(v.Contact)
	}
	return out
}

func groupID(s string) (domain.GroupID, error) { return domain.ParseGroupID(s) }

// ── groups ──────────────────────────────────────────────────────────

func (a *API) ListGroups(ctx context.Context, req apiv1.ListGroupsRequestObject) (apiv1.ListGroupsResponseObject, error) {
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	items, next, err := a.svc.ListGroups(ctx, page)
	if err != nil {
		return nil, err
	}
	out := apiv1.ListGroups200JSONResponse{Items: make([]apiv1.Group, len(items)), NextPageToken: next}
	for i, g := range items {
		out.Items[i] = toGroup(g)
	}
	return out, nil
}

func (a *API) CreateGroup(ctx context.Context, req apiv1.CreateGroupRequestObject) (apiv1.CreateGroupResponseObject, error) {
	g, replayed, err := a.svc.CreateGroup(ctx, req.Body.Name, deref(req.Params.IdempotencyKey))
	if err != nil {
		return nil, err
	}
	return apiv1.CreateGroup201JSONResponse{Body: toGroup(g), Headers: apiv1.CreateGroup201ResponseHeaders{
		ETag: ptr(etag(g.Version)), Location: ptr(tenantPath(ctx, "/groups/"+g.ID.String())),
		IdempotentReplayed: replayedHeader(replayed),
	}}, nil
}

func (a *API) GetGroup(ctx context.Context, req apiv1.GetGroupRequestObject) (apiv1.GetGroupResponseObject, error) {
	id, err := groupID(req.Group)
	if err != nil {
		return nil, err
	}
	g, err := a.svc.GetGroup(ctx, id)
	if err != nil {
		return nil, err
	}
	return apiv1.GetGroup200JSONResponse{Body: toGroup(g), Headers: apiv1.GetGroup200ResponseHeaders{ETag: ptr(etag(g.Version))}}, nil
}

func (a *API) RenameGroup(ctx context.Context, req apiv1.RenameGroupRequestObject) (apiv1.RenameGroupResponseObject, error) {
	id, err := groupID(req.Group)
	if err != nil {
		return nil, err
	}
	version, err := parseETag(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	g, err := a.svc.RenameGroup(ctx, id, version, req.Body.Name)
	if err != nil {
		return nil, err
	}
	return apiv1.RenameGroup200JSONResponse{Body: toGroup(g), Headers: apiv1.RenameGroup200ResponseHeaders{ETag: ptr(etag(g.Version))}}, nil
}

func (a *API) DeleteGroup(ctx context.Context, req apiv1.DeleteGroupRequestObject) (apiv1.DeleteGroupResponseObject, error) {
	id, err := groupID(req.Group)
	if err != nil {
		return nil, err
	}
	if err := a.svc.DeleteGroup(ctx, id); err != nil {
		return nil, err
	}
	return apiv1.DeleteGroup204Response{}, nil
}

// PutGroupMember is idempotent: a member already in the group is
// answered with the group as it is.
func (a *API) PutGroupMember(ctx context.Context, req apiv1.PutGroupMemberRequestObject) (apiv1.PutGroupMemberResponseObject, error) {
	id, err := groupID(req.Group)
	if err != nil {
		return nil, err
	}
	member, err := domain.ParseMemberID(req.Member)
	if err != nil {
		return nil, err
	}
	g, err := a.svc.AddGroupMember(ctx, id, member)
	if errors.Is(err, domain.ErrAlreadyInGroup) {
		g, err = a.svc.GetGroup(ctx, id)
	}
	if err != nil {
		return nil, err
	}
	return apiv1.PutGroupMember200JSONResponse{Body: toGroup(g), Headers: apiv1.PutGroupMember200ResponseHeaders{ETag: ptr(etag(g.Version))}}, nil
}

func (a *API) RemoveGroupMember(ctx context.Context, req apiv1.RemoveGroupMemberRequestObject) (apiv1.RemoveGroupMemberResponseObject, error) {
	id, err := groupID(req.Group)
	if err != nil {
		return nil, err
	}
	member, err := domain.ParseMemberID(req.Member)
	if err != nil {
		return nil, err
	}
	if _, err := a.svc.RemoveGroupMember(ctx, id, member); err != nil {
		return nil, err
	}
	return apiv1.RemoveGroupMember204Response{}, nil
}

// ── vendors ─────────────────────────────────────────────────────────

func (a *API) ListVendors(ctx context.Context, req apiv1.ListVendorsRequestObject) (apiv1.ListVendorsResponseObject, error) {
	page, err := pagination.Parse(req.Params.PageSize, req.Params.PageToken)
	if err != nil {
		return nil, err
	}
	items, next, err := a.svc.ListVendors(ctx, page)
	if err != nil {
		return nil, err
	}
	out := apiv1.ListVendors200JSONResponse{Items: make([]apiv1.Vendor, len(items)), NextPageToken: next}
	for i, v := range items {
		out.Items[i] = toVendor(v)
	}
	return out, nil
}

func (a *API) CreateVendor(ctx context.Context, req apiv1.CreateVendorRequestObject) (apiv1.CreateVendorResponseObject, error) {
	in := app.VendorInput{Name: req.Body.Name, Contact: deref(req.Body.Contact)}
	if req.Body.Locales != nil {
		in.Locales = *req.Body.Locales
	}
	v, replayed, err := a.svc.CreateVendor(ctx, in, deref(req.Params.IdempotencyKey))
	if err != nil {
		return nil, err
	}
	return apiv1.CreateVendor201JSONResponse{Body: toVendor(v), Headers: apiv1.CreateVendor201ResponseHeaders{
		ETag: ptr(etag(v.Version)), Location: ptr(tenantPath(ctx, "/vendors/"+v.ID.String())),
		IdempotentReplayed: replayedHeader(replayed),
	}}, nil
}

func (a *API) GetVendor(ctx context.Context, req apiv1.GetVendorRequestObject) (apiv1.GetVendorResponseObject, error) {
	id, err := domain.ParseVendorID(req.Vendor)
	if err != nil {
		return nil, err
	}
	v, err := a.svc.GetVendor(ctx, id)
	if err != nil {
		return nil, err
	}
	return apiv1.GetVendor200JSONResponse{Body: toVendor(v), Headers: apiv1.GetVendor200ResponseHeaders{ETag: ptr(etag(v.Version))}}, nil
}

// UpdateVendor merges the body into the vendor it read; If-Match makes
// the merge safe, since UpdateVendor refuses a vendor that moved on.
func (a *API) UpdateVendor(ctx context.Context, req apiv1.UpdateVendorRequestObject) (apiv1.UpdateVendorResponseObject, error) {
	id, err := domain.ParseVendorID(req.Vendor)
	if err != nil {
		return nil, err
	}
	version, err := parseETag(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	cur, err := a.svc.GetVendor(ctx, id)
	if err != nil {
		return nil, err
	}
	in := app.VendorInput{Name: cur.Name, Contact: cur.Contact, Locales: cur.LocaleStrings()}
	if req.Body.Name != nil {
		in.Name = *req.Body.Name
	}
	if req.Body.Contact != nil {
		in.Contact = *req.Body.Contact
	}
	if req.Body.Locales != nil {
		in.Locales = *req.Body.Locales
	}
	v, err := a.svc.UpdateVendor(ctx, id, version, in)
	if err != nil {
		return nil, err
	}
	return apiv1.UpdateVendor200JSONResponse{Body: toVendor(v), Headers: apiv1.UpdateVendor200ResponseHeaders{ETag: ptr(etag(v.Version))}}, nil
}

func (a *API) DeleteVendor(ctx context.Context, req apiv1.DeleteVendorRequestObject) (apiv1.DeleteVendorResponseObject, error) {
	id, err := domain.ParseVendorID(req.Vendor)
	if err != nil {
		return nil, err
	}
	if err := a.svc.DeleteVendor(ctx, id); err != nil {
		return nil, err
	}
	return apiv1.DeleteVendor204Response{}, nil
}
