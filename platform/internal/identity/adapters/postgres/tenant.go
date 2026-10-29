package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres/identitysql"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy/tenantpg"
)

// tenantStore implements app.TenantStore on a tenant-scoped transaction.
type tenantStore struct {
	tx *db.TenantTx
	q  *identitysql.Queries
}

var _ app.TenantStore = (*tenantStore)(nil)

func (s *tenantStore) CreateTenant(ctx context.Context, t tenancy.Tenant) (tenancy.Tenant, error) {
	t, err := tenantpg.Insert(ctx, s.tx, t)
	return t, storeError(err)
}

func (s *tenantStore) CurrentTenant(ctx context.Context) (tenancy.Tenant, error) {
	t, err := tenantpg.Current(ctx, s.tx)
	if errors.Is(err, tenantpg.ErrNotFound) {
		return tenancy.Tenant{}, app.ErrNotFound
	}
	return t, err
}

func (s *tenantStore) InsertMember(ctx context.Context, m domain.Member, by domain.Actor) (bool, error) {
	n, err := s.q.InsertMember(ctx, identitysql.InsertMemberParams{
		ID:         m.ID.UUID(),
		PersonID:   nullUUID(m.PersonID.UUID()),
		Email:      m.Email.String(),
		Roles:      m.Roles.Strings(),
		Locales:    m.Locales.Strings(),
		Projects:   m.Restriction.Projects.UUIDs(),
		VendorID:   nullUUID(m.Restriction.Vendor.UUID()),
		Visibility: string(m.Restriction.Visibility),
		Status:     string(m.Status),
		Version:    int32(m.Version), //nolint:gosec // versions stay tiny
		CreatedBy:  by.String(),
		CreatedAt:  m.CreatedAt,
	})
	return n == 1, storeError(err)
}

func (s *tenantStore) Member(ctx context.Context, id domain.MemberID) (app.MemberView, error) {
	row, err := s.q.GetMember(ctx, id.UUID())
	if err != nil {
		return app.MemberView{}, storeError(err)
	}
	m, err := member(identitysql.IdentityMember{
		ID: row.ID, TenantID: row.TenantID, PersonID: row.PersonID, Email: row.Email, Roles: row.Roles,
		Locales: row.Locales, Status: row.Status, Version: row.Version, CreatedBy: row.CreatedBy,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Projects: row.Projects, VendorID: row.VendorID, Visibility: row.Visibility,
	})
	return app.MemberView{Member: m, DisplayName: row.DisplayName}, err
}

func member(row identitysql.IdentityMember) (domain.Member, error) {
	email, err := authgo.NewEmail(row.Email)
	if err != nil {
		return domain.Member{}, fmt.Errorf("identity: stored member email %q: %w", row.Email, err)
	}
	roles, locales, err := access(row.Roles, row.Locales)
	if err != nil {
		return domain.Member{}, err
	}
	r, err := restriction(row.Projects, row.VendorID, row.Visibility)
	if err != nil {
		return domain.Member{}, fmt.Errorf("identity: stored restriction of member %s: %w", row.ID, err)
	}
	return domain.Member{
		ID:          domain.MemberID(row.ID),
		TenantID:    tenancy.ID(row.TenantID),
		PersonID:    domain.PersonID(row.PersonID.UUID),
		Email:       email,
		Roles:       roles,
		Locales:     locales,
		Restriction: r,
		Status:      domain.MemberStatus(row.Status),
		Version:     int(row.Version),
		CreatedAt:   row.CreatedAt.UTC(),
		UpdatedAt:   row.UpdatedAt.UTC(),
	}, nil
}

func (s *tenantStore) LockMember(ctx context.Context, id domain.MemberID) (domain.Member, error) {
	row, err := s.q.LockMember(ctx, id.UUID())
	if err != nil {
		return domain.Member{}, storeError(err)
	}
	return member(row)
}

func (s *tenantStore) LockActiveOwners(ctx context.Context) (int, error) {
	ids, err := s.q.LockActiveOwners(ctx)
	return len(ids), storeError(err)
}

func (s *tenantStore) Members(ctx context.Context, after domain.MemberID, limit int) ([]app.MemberView, error) {
	rows, err := s.q.ListMembers(ctx, identitysql.ListMembersParams{After: after.UUID(), MaxRows: int32(limit)}) //nolint:gosec // bounded by pagination
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.MemberView, 0, len(rows))
	for _, r := range rows {
		m, err := member(identitysql.IdentityMember{
			ID: r.ID, TenantID: r.TenantID, PersonID: r.PersonID, Email: r.Email, Roles: r.Roles,
			Locales: r.Locales, Status: r.Status, Version: r.Version, CreatedBy: r.CreatedBy,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			Projects: r.Projects, VendorID: r.VendorID, Visibility: r.Visibility,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, app.MemberView{Member: m, DisplayName: r.DisplayName})
	}
	return out, nil
}

func (s *tenantStore) UpdateMemberAccess(ctx context.Context, m domain.Member) error {
	n, err := s.q.UpdateMemberAccess(ctx, identitysql.UpdateMemberAccessParams{
		ID: m.ID.UUID(), Roles: m.Roles.Strings(), Locales: m.Locales.Strings(),
		Version: int32(m.Version), UpdatedAt: m.UpdatedAt, //nolint:gosec // versions stay tiny
	})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrStaleVersion
	}
	return nil
}

func (s *tenantStore) UpdateMemberRestriction(ctx context.Context, m domain.Member) error {
	n, err := s.q.UpdateMemberRestriction(ctx, identitysql.UpdateMemberRestrictionParams{
		ID: m.ID.UUID(), Projects: m.Restriction.Projects.UUIDs(), VendorID: nullUUID(m.Restriction.Vendor.UUID()),
		Visibility: string(m.Restriction.Visibility), Version: int32(m.Version), UpdatedAt: m.UpdatedAt, //nolint:gosec // versions stay tiny
	})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrStaleVersion
	}
	return nil
}

func (s *tenantStore) ActivateMember(ctx context.Context, m domain.Member) error {
	n, err := s.q.ActivateMember(ctx, identitysql.ActivateMemberParams{
		ID: m.ID.UUID(), PersonID: nullUUID(m.PersonID.UUID()), UpdatedAt: m.UpdatedAt,
	})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrStaleVersion
	}
	return nil
}

func (s *tenantStore) DeleteMember(ctx context.Context, id domain.MemberID) error {
	n, err := s.q.DeleteMember(ctx, id.UUID())
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (s *tenantStore) InsertToken(ctx context.Context, t domain.APIToken) (bool, error) {
	n, err := s.q.InsertToken(ctx, identitysql.InsertTokenParams{
		ID: t.ID.UUID(), Name: t.Name, TokenHash: t.Hash, Hint: t.Hint, Scopes: t.Scopes.Strings(),
		Projects: t.Projects.UUIDs(), CreatedBy: t.CreatedBy.String(), CreatedAt: t.CreatedAt, ExpiresAt: timestamptz(t.ExpiresAt),
	})
	return n == 1, storeError(err)
}

func (s *tenantStore) Token(ctx context.Context, id domain.TokenID) (domain.APIToken, error) {
	row, err := s.q.GetToken(ctx, id.UUID())
	if err != nil {
		return domain.APIToken{}, storeError(err)
	}
	return apiToken(row)
}

func apiToken(row identitysql.IdentityApiToken) (domain.APIToken, error) {
	scopes, err := domain.ParseScopes(row.Scopes)
	if err != nil {
		return domain.APIToken{}, fmt.Errorf("identity: stored scopes of token %s: %w", row.ID, err)
	}
	by, err := domain.ParseActor(row.CreatedBy)
	if err != nil {
		return domain.APIToken{}, fmt.Errorf("identity: stored creator of token %s: %w", row.ID, err)
	}
	projects, err := projectScope(row.Projects)
	if err != nil {
		return domain.APIToken{}, fmt.Errorf("identity: stored projects of token %s: %w", row.ID, err)
	}
	return domain.APIToken{
		ID: domain.TokenID(row.ID), TenantID: tenancy.ID(row.TenantID), Name: row.Name, Scopes: scopes, Projects: projects,
		Hint: row.Hint, Hash: row.TokenHash, CreatedBy: by, CreatedAt: row.CreatedAt.UTC(),
		ExpiresAt: timePtr(row.ExpiresAt), LastUsedAt: timePtr(row.LastUsedAt), RevokedAt: timePtr(row.RevokedAt),
	}, nil
}

func (s *tenantStore) Tokens(ctx context.Context, after domain.TokenID, limit int) ([]domain.APIToken, error) {
	rows, err := s.q.ListTokens(ctx, identitysql.ListTokensParams{After: after.UUID(), MaxRows: int32(limit)}) //nolint:gosec // bounded by pagination
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.APIToken, 0, len(rows))
	for _, r := range rows {
		t, err := apiToken(r)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *tenantStore) RevokeToken(ctx context.Context, t domain.APIToken, by domain.Actor) error {
	n, err := s.q.RevokeToken(ctx, identitysql.RevokeTokenParams{
		ID: t.ID.UUID(), RevokedAt: timestamptz(t.RevokedAt), RevokedBy: pgtype.Text{String: by.String(), Valid: true},
	})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return domain.ErrTokenRevoked
	}
	return nil
}

func (s *tenantStore) Publish(ctx context.Context, e outbox.Event) error {
	return publish(ctx, s.tx, e)
}
