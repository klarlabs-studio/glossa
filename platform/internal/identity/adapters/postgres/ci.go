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

// CI tokens (RFC 0004 §6.3). Minted in the tenant the repository's Git
// connection named, resolved in system scope like an API token: the
// tenant follows from the credential, so it can't be known before the
// lookup.

// ── tenant scope ────────────────────────────────────────────────────

func (s *tenantStore) InsertCIToken(ctx context.Context, t domain.CIToken) error {
	perms, err := json.Marshal(t.Permissions.LocaleScopes())
	if err != nil {
		return fmt.Errorf("identity: encode CI token permissions: %w", err)
	}
	return storeError(s.q.InsertCIToken(ctx, identitysql.InsertCITokenParams{
		ID: t.ID.UUID(), ProjectID: t.ProjectID.UUID(), TokenHash: t.Hash, Permissions: perms,
		RepositoryID: t.Run.RepositoryID, RepositoryOwnerID: t.Run.RepositoryOwnerID,
		Repository: t.Run.Repository, GitRef: t.Run.Ref, CommitSha: t.Run.SHA,
		EventName: t.Run.EventName, WorkflowRef: t.Run.WorkflowRef, RunID: t.Run.RunID,
		RunnerEnvironment: t.Run.RunnerEnvironment, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
	}))
}

// ── system scope ────────────────────────────────────────────────────

func (s *systemStore) CITokenByHash(ctx context.Context, hash string) (app.CITokenRecord, error) {
	row, err := s.q.SystemGetCITokenByHash(ctx, hash)
	if err != nil {
		return app.CITokenRecord{}, storeError(err)
	}
	var scopes map[domain.Permission][]string
	if err := json.Unmarshal(row.Permissions, &scopes); err != nil {
		return app.CITokenRecord{}, fmt.Errorf("identity: stored permissions of CI token %s: %w", row.ID, err)
	}
	grant, err := domain.GrantFromLocaleScopes(scopes)
	if err != nil {
		return app.CITokenRecord{}, fmt.Errorf("identity: stored permissions of CI token %s: %w", row.ID, err)
	}
	return app.CITokenRecord{
		ID: domain.CITokenID(row.ID), Tenant: tenancy.ID(row.TenantID),
		Project: domain.ProjectRef(row.ProjectID), Permissions: grant, ExpiresAt: row.ExpiresAt,
	}, nil
}

func (s *systemStore) TouchCIToken(ctx context.Context, id domain.CITokenID, at time.Time) error {
	return storeError(s.q.SystemTouchCIToken(ctx, identitysql.SystemTouchCITokenParams{
		ID: id.UUID(), At: timestamptz(&at),
	}))
}

func (s *systemStore) PurgeExpiredCITokens(ctx context.Context, at time.Time) (int64, error) {
	n, err := s.q.SystemPurgeExpiredCITokens(ctx, at)
	return n, storeError(err)
}
