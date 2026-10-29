package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres/identitysql"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// restriction rebuilds a member's stored project scope, vendor and
// visibility.
func restriction(projects []uuid.UUID, vendor uuid.NullUUID, visibility string) (domain.Restriction, error) {
	ps, err := projectScope(projects)
	if err != nil {
		return domain.Restriction{}, err
	}
	v, err := domain.ParseVisibility(visibility)
	if err != nil {
		return domain.Restriction{}, err
	}
	return domain.Restriction{Projects: ps, Vendor: domain.VendorID(vendor.UUID), Visibility: v}, nil
}

func projectScope(ids []uuid.UUID) (domain.ProjectScope, error) {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = id.String()
	}
	return domain.ParseProjectScope(s)
}

// fkViolation reports whether err is a foreign-key violation of constraint.
func fkViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == constraint
}

func (s *tenantStore) CountVendorMembers(ctx context.Context, id domain.VendorID) (int, error) {
	n, err := s.q.CountVendorMembers(ctx, nullUUID(id.UUID()))
	return int(n), storeError(err)
}

func (s *tenantStore) InsertVendor(ctx context.Context, v domain.Vendor, by domain.Actor) (bool, error) {
	n, err := s.q.InsertVendor(ctx, identitysql.InsertVendorParams{
		ID: v.ID.UUID(), Name: v.Name, Contact: v.Contact, Locales: v.LocaleStrings(),
		Version: int32(v.Version), CreatedBy: by.String(), CreatedAt: v.CreatedAt, //nolint:gosec // versions stay tiny
	})
	return n == 1, storeError(err)
}

func (s *tenantStore) Vendor(ctx context.Context, id domain.VendorID) (domain.Vendor, error) {
	row, err := s.q.GetVendor(ctx, id.UUID())
	if err != nil {
		return domain.Vendor{}, storeError(err)
	}
	return vendor(row)
}

func (s *tenantStore) LockVendor(ctx context.Context, id domain.VendorID) (domain.Vendor, error) {
	row, err := s.q.LockVendor(ctx, id.UUID())
	if err != nil {
		return domain.Vendor{}, storeError(err)
	}
	return vendor(row)
}

func vendor(row identitysql.IdentityVendor) (domain.Vendor, error) {
	locales, err := domain.ParseLocaleList(row.Locales)
	if err != nil {
		return domain.Vendor{}, fmt.Errorf("identity: stored locales of vendor %s: %w", row.ID, err)
	}
	return domain.Vendor{
		ID: domain.VendorID(row.ID), TenantID: tenancy.ID(row.TenantID), Name: row.Name, Contact: row.Contact,
		Locales: locales, Version: int(row.Version), CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}, nil
}

func (s *tenantStore) Vendors(ctx context.Context, after domain.VendorID, limit int) ([]domain.Vendor, error) {
	rows, err := s.q.ListVendors(ctx, identitysql.ListVendorsParams{After: after.UUID(), MaxRows: int32(limit)}) //nolint:gosec // bounded by pagination
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Vendor, 0, len(rows))
	for _, r := range rows {
		v, err := vendor(r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *tenantStore) UpdateVendor(ctx context.Context, v domain.Vendor) error {
	n, err := s.q.UpdateVendor(ctx, identitysql.UpdateVendorParams{
		ID: v.ID.UUID(), Name: v.Name, Contact: v.Contact, Locales: v.LocaleStrings(),
		Version: int32(v.Version), UpdatedAt: v.UpdatedAt, //nolint:gosec // versions stay tiny
	})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrStaleVersion
	}
	return nil
}

func (s *tenantStore) DeleteVendor(ctx context.Context, id domain.VendorID) error {
	n, err := s.q.DeleteVendor(ctx, id.UUID())
	if fkViolation(err, "identity_members_vendor_fkey") {
		return domain.ErrVendorHasMembers
	}
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (s *tenantStore) InsertGroup(ctx context.Context, g domain.Group, by domain.Actor) (bool, error) {
	n, err := s.q.InsertGroup(ctx, identitysql.InsertGroupParams{
		ID: g.ID.UUID(), Name: g.Name, Version: int32(g.Version), CreatedBy: by.String(), CreatedAt: g.CreatedAt, //nolint:gosec // versions stay tiny
	})
	return n == 1, storeError(err)
}

func (s *tenantStore) Group(ctx context.Context, id domain.GroupID) (domain.Group, error) {
	row, err := s.q.GetGroup(ctx, id.UUID())
	if err != nil {
		return domain.Group{}, storeError(err)
	}
	return s.group(ctx, row)
}

func (s *tenantStore) LockGroup(ctx context.Context, id domain.GroupID) (domain.Group, error) {
	row, err := s.q.LockGroup(ctx, id.UUID())
	if err != nil {
		return domain.Group{}, storeError(err)
	}
	return s.group(ctx, row)
}

func (s *tenantStore) group(ctx context.Context, row identitysql.IdentityGroup) (domain.Group, error) {
	ids, err := s.q.GroupMemberIDs(ctx, row.ID)
	if err != nil {
		return domain.Group{}, storeError(err)
	}
	g := domain.Group{
		ID: domain.GroupID(row.ID), TenantID: tenancy.ID(row.TenantID), Name: row.Name,
		Version: int(row.Version), CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
	for _, id := range ids {
		g.Members = append(g.Members, domain.MemberID(id))
	}
	return g, nil
}

// Groups lists groups after the cursor, each with its members. A page
// is at most a hundred groups, so one member query per group is bounded.
func (s *tenantStore) Groups(ctx context.Context, after domain.GroupID, limit int) ([]domain.Group, error) {
	rows, err := s.q.ListGroups(ctx, identitysql.ListGroupsParams{After: after.UUID(), MaxRows: int32(limit)}) //nolint:gosec // bounded by pagination
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Group, 0, len(rows))
	for _, r := range rows {
		g, err := s.group(ctx, r)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

func (s *tenantStore) UpdateGroup(ctx context.Context, g domain.Group) error {
	n, err := s.q.UpdateGroup(ctx, identitysql.UpdateGroupParams{
		ID: g.ID.UUID(), Name: g.Name, Version: int32(g.Version), UpdatedAt: g.UpdatedAt, //nolint:gosec // versions stay tiny
	})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrStaleVersion
	}
	return nil
}

func (s *tenantStore) AddGroupMember(ctx context.Context, g domain.Group, member domain.MemberID, by domain.Actor) error {
	if err := s.UpdateGroup(ctx, g); err != nil {
		return err
	}
	return storeError(s.q.InsertGroupMember(ctx, identitysql.InsertGroupMemberParams{
		GroupID: g.ID.UUID(), MemberID: member.UUID(), AddedBy: by.String(), AddedAt: g.UpdatedAt,
	}))
}

func (s *tenantStore) RemoveGroupMember(ctx context.Context, g domain.Group, member domain.MemberID) error {
	if err := s.UpdateGroup(ctx, g); err != nil {
		return err
	}
	n, err := s.q.DeleteGroupMember(ctx, identitysql.DeleteGroupMemberParams{GroupID: g.ID.UUID(), MemberID: member.UUID()})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return domain.ErrNotInGroup
	}
	return nil
}

func (s *tenantStore) DeleteGroup(ctx context.Context, id domain.GroupID) error {
	n, err := s.q.DeleteGroup(ctx, id.UUID())
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}
