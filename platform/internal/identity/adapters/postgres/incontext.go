package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres/identitysql"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// Preview origins and in-context grants (RFC 0004 §5.2). The grant is
// resolved in system scope, like an API token: the tenant follows from
// the credential, so it can't be known before the lookup.

// ── tenant scope ────────────────────────────────────────────────────

func (s *tenantStore) InsertPreviewOrigin(ctx context.Context, o domain.PreviewOrigin) (bool, error) {
	n, err := s.q.InsertPreviewOrigin(ctx, identitysql.InsertPreviewOriginParams{
		ID: o.ID.UUID(), ProjectID: o.ProjectID.UUID(), Origin: o.Origin.String(), Label: o.Label,
		CreatedBy: o.CreatedBy.String(), CreatedAt: o.CreatedAt,
	})
	return n == 1, storeError(err)
}

func (s *tenantStore) PreviewOrigins(ctx context.Context, project domain.ProjectRef) ([]domain.PreviewOrigin, error) {
	rows, err := s.q.ListPreviewOrigins(ctx, project.UUID())
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.PreviewOrigin, len(rows))
	for i, row := range rows {
		if out[i], err = previewOrigin(row); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *tenantStore) PreviewOrigin(ctx context.Context, id domain.PreviewOriginID) (domain.PreviewOrigin, error) {
	row, err := s.q.GetPreviewOrigin(ctx, id.UUID())
	if err != nil {
		return domain.PreviewOrigin{}, storeError(err)
	}
	return previewOrigin(row)
}

func (s *tenantStore) PreviewOriginFor(
	ctx context.Context, project domain.ProjectRef, origin domain.Origin,
) (domain.PreviewOrigin, error) {
	row, err := s.q.GetPreviewOriginFor(ctx, identitysql.GetPreviewOriginForParams{
		ProjectID: project.UUID(), Origin: origin.String(),
	})
	if err != nil {
		return domain.PreviewOrigin{}, storeError(err)
	}
	return previewOrigin(row)
}

// DeletePreviewOrigin drops the registration and the grants minted for
// it in one transaction, so no editor session outlives the permission to
// run it.
func (s *tenantStore) DeletePreviewOrigin(ctx context.Context, o domain.PreviewOrigin) error {
	if _, err := s.q.DeleteGrantsForOrigin(ctx, identitysql.DeleteGrantsForOriginParams{
		ProjectID: o.ProjectID.UUID(), Origin: o.Origin.String(),
	}); err != nil {
		return storeError(err)
	}
	n, err := s.q.DeletePreviewOrigin(ctx, o.ID.UUID())
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (s *tenantStore) InsertGrant(ctx context.Context, g domain.InContextGrant) error {
	perms, err := json.Marshal(g.Permissions.LocaleScopes())
	if err != nil {
		return fmt.Errorf("identity: encode grant permissions: %w", err)
	}
	return storeError(s.q.InsertGrant(ctx, identitysql.InsertGrantParams{
		ID: g.ID.UUID(), ProjectID: g.ProjectID.UUID(), PersonID: g.Person.UUID(), TokenHash: g.Hash,
		Origin: g.Origin.String(), Permissions: perms, CreatedAt: g.CreatedAt, ExpiresAt: g.ExpiresAt,
	}))
}

// ── system scope ────────────────────────────────────────────────────

func (s *systemStore) GrantByHash(ctx context.Context, hash string) (app.GrantRecord, error) {
	row, err := s.q.SystemGetGrantByHash(ctx, hash)
	if err != nil {
		return app.GrantRecord{}, storeError(err)
	}
	origin, err := domain.ParseOrigin(row.Origin)
	if err != nil {
		return app.GrantRecord{}, fmt.Errorf("identity: stored origin of grant %s: %w", row.ID, err)
	}
	var scopes map[domain.Permission][]string
	if err := json.Unmarshal(row.Permissions, &scopes); err != nil {
		return app.GrantRecord{}, fmt.Errorf("identity: stored permissions of grant %s: %w", row.ID, err)
	}
	grant, err := domain.GrantFromLocaleScopes(scopes)
	if err != nil {
		return app.GrantRecord{}, fmt.Errorf("identity: stored permissions of grant %s: %w", row.ID, err)
	}
	return app.GrantRecord{
		ID: domain.InContextGrantID(row.ID), Tenant: tenancy.ID(row.TenantID),
		Project: domain.ProjectRef(row.ProjectID), Person: domain.PersonID(row.PersonID),
		Origin: origin, Permissions: grant, ExpiresAt: row.ExpiresAt,
	}, nil
}

func (s *systemStore) TouchGrant(ctx context.Context, id domain.InContextGrantID, at time.Time) error {
	return storeError(s.q.SystemTouchGrant(ctx, identitysql.SystemTouchGrantParams{
		ID: id.UUID(), At: timestamptz(&at),
	}))
}

func (s *systemStore) OriginRegistered(ctx context.Context, origin string) (bool, error) {
	ok, err := s.q.SystemOriginRegistered(ctx, origin)
	return ok, storeError(err)
}

func (s *systemStore) PurgeExpiredGrants(ctx context.Context, at time.Time) (int64, error) {
	n, err := s.q.SystemPurgeExpiredGrants(ctx, at)
	return n, storeError(err)
}

func previewOrigin(row identitysql.IdentityPreviewOrigin) (domain.PreviewOrigin, error) {
	origin, err := domain.ParseOrigin(row.Origin)
	if err != nil {
		return domain.PreviewOrigin{}, fmt.Errorf("identity: stored preview origin %s: %w", row.ID, err)
	}
	by, err := domain.ParseActor(row.CreatedBy)
	if err != nil {
		return domain.PreviewOrigin{}, fmt.Errorf("identity: stored creator of preview origin %s: %w", row.ID, err)
	}
	return domain.PreviewOrigin{
		ID: domain.PreviewOriginID(row.ID), TenantID: tenancy.ID(row.TenantID),
		ProjectID: domain.ProjectRef(row.ProjectID), Origin: origin, Label: row.Label,
		CreatedBy: by, CreatedAt: row.CreatedAt,
	}, nil
}
