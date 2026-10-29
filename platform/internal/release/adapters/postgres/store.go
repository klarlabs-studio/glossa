// Package postgres implements Release's persistence port on the
// kernel's unit of work, with sqlc queries over the release_* tables.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/release/adapters/postgres/releasesql"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// Transactor implements app.Transactor.
type Transactor struct{ uow *db.UnitOfWork }

// NewTransactor returns a Transactor on uow.
func NewTransactor(uow *db.UnitOfWork) *Transactor { return &Transactor{uow: uow} }

// InTenant implements app.Transactor.
func (t *Transactor) InTenant(ctx context.Context, fn func(context.Context, app.Store) error) error {
	return t.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &store{tx: tx, q: releasesql.New(tx)})
	})
}

type store struct {
	tx *db.TenantTx
	q  *releasesql.Queries
}

var _ app.Store = (*store)(nil)

func storeError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ErrNotFound
	}
	return err
}

func int32Of(n int) int32 { return int32(n) } //nolint:gosec // versions, counters and page sizes stay small

func nullID(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: id != uuid.Nil} }

// ── environments ────────────────────────────────────────────────────

func environment(r releasesql.ReleaseEnvironment) (domain.Environment, error) {
	var p domain.Policy
	if err := json.Unmarshal(r.Policy, &p); err != nil {
		return domain.Environment{}, fmt.Errorf("release: stored policy of %s: %w", r.Name, err)
	}
	var approval *domain.ApprovalPolicy
	if len(r.Approval) > 0 {
		approval = &domain.ApprovalPolicy{}
		if err := json.Unmarshal(r.Approval, approval); err != nil {
			return domain.Environment{}, fmt.Errorf("release: stored approval of %s: %w", r.Name, err)
		}
	}
	return domain.Environment{
		ProjectID: r.ProjectID, Name: r.Name, Kind: domain.EnvironmentKind(r.Kind), Branch: r.Branch.String,
		Policy: p, Approval: approval, Current: r.CurrentReleaseID.UUID,
		Version: int(r.Version), CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC(),
	}, nil
}

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func environments(rows []releasesql.ReleaseEnvironment) ([]domain.Environment, error) {
	out := make([]domain.Environment, 0, len(rows))
	for _, r := range rows {
		e, err := environment(r)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *store) InsertEnvironment(ctx context.Context, e domain.Environment, by string) (bool, error) {
	policy, err := json.Marshal(e.Policy)
	if err != nil {
		return false, err
	}
	kind := e.Kind
	if kind == "" {
		kind = domain.KindStandard
	}
	n, err := s.q.InsertEnvironment(ctx, releasesql.InsertEnvironmentParams{
		ProjectID: e.ProjectID, Name: e.Name, Kind: string(kind), Branch: text(e.Branch), Policy: policy,
		Version: int32Of(e.Version), CreatedBy: by, CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	})
	return n == 1, storeError(err)
}

func (s *store) BranchEnvironment(ctx context.Context, project uuid.UUID, branch string, lock bool) (domain.Environment, error) {
	var (
		row releasesql.ReleaseEnvironment
		err error
	)
	if lock {
		row, err = s.q.LockBranchEnvironment(ctx, releasesql.LockBranchEnvironmentParams{ProjectID: project, Branch: text(branch)})
	} else {
		row, err = s.q.GetBranchEnvironment(ctx, releasesql.GetBranchEnvironmentParams{ProjectID: project, Branch: text(branch)})
	}
	if err != nil {
		return domain.Environment{}, storeError(err)
	}
	return environment(row)
}

func (s *store) CountBranchEnvironments(ctx context.Context, project uuid.UUID) (int, error) {
	n, err := s.q.CountBranchEnvironments(ctx, project)
	return int(n), storeError(err)
}

func (s *store) DeleteEnvironment(ctx context.Context, project uuid.UUID, name string) (bool, error) {
	n, err := s.q.DeleteEnvironment(ctx, releasesql.DeleteEnvironmentParams{ProjectID: project, Name: name})
	return n == 1, storeError(err)
}

func (s *store) Environment(ctx context.Context, project uuid.UUID, name string, lock bool) (domain.Environment, error) {
	var (
		row releasesql.ReleaseEnvironment
		err error
	)
	if lock {
		row, err = s.q.LockEnvironment(ctx, releasesql.LockEnvironmentParams{ProjectID: project, Name: name})
	} else {
		row, err = s.q.GetEnvironment(ctx, releasesql.GetEnvironmentParams{ProjectID: project, Name: name})
	}
	if err != nil {
		return domain.Environment{}, storeError(err)
	}
	return environment(row)
}

func (s *store) Environments(ctx context.Context, project uuid.UUID, after string, limit int) ([]domain.Environment, error) {
	rows, err := s.q.ListEnvironments(ctx, releasesql.ListEnvironmentsParams{ProjectID: project, After: after, MaxRows: int32Of(limit)})
	if err != nil {
		return nil, storeError(err)
	}
	return environments(rows)
}

func (s *store) LockProjectEnvironments(ctx context.Context, project uuid.UUID) ([]domain.Environment, error) {
	rows, err := s.q.LockProjectEnvironments(ctx, project)
	if err != nil {
		return nil, storeError(err)
	}
	return environments(rows)
}

func (s *store) UpdateEnvironment(ctx context.Context, e domain.Environment, expected int) error {
	policy, err := json.Marshal(e.Policy)
	if err != nil {
		return err
	}
	var approval []byte
	if e.Approval != nil {
		if approval, err = json.Marshal(e.Approval); err != nil {
			return err
		}
	}
	n, err := s.q.UpdateEnvironment(ctx, releasesql.UpdateEnvironmentParams{
		Policy: policy, Approval: approval, CurrentReleaseID: nullID(e.Current), Version: int32Of(e.Version), UpdatedAt: e.UpdatedAt,
		ProjectID: e.ProjectID, Name: e.Name, ExpectedVersion: int32Of(expected),
	})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrStaleVersion
	}
	return nil
}

func (s *store) DeleteProjectEnvironments(ctx context.Context, project uuid.UUID) ([]string, error) {
	names, err := s.q.DeleteProjectEnvironments(ctx, project)
	return names, storeError(err)
}

// ── releases ────────────────────────────────────────────────────────

func release(r releasesql.ReleaseRelease) (domain.Release, error) {
	out := domain.Release{
		ID: r.ID, ProjectID: r.ProjectID, Version: int(r.Version), Parent: r.ParentID.UUID, Environment: r.Environment,
		Branch: r.Branch.String, Digest: r.ManifestDigest, Note: r.Note, Author: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(),
	}
	for _, f := range []struct {
		name string
		raw  []byte
		into any
	}{{"policy", r.Policy, &out.Policy}, {"content", r.Content, &out.Content}, {"stats", r.Stats, &out.Stats}} {
		if err := json.Unmarshal(f.raw, f.into); err != nil {
			return domain.Release{}, fmt.Errorf("release: stored %s of %s: %w", f.name, r.ID, err)
		}
	}
	return out, nil
}

func (s *store) InsertRelease(ctx context.Context, r domain.Release) (bool, error) {
	policy, err := json.Marshal(r.Policy)
	if err != nil {
		return false, err
	}
	content, err := json.Marshal(r.Content)
	if err != nil {
		return false, err
	}
	stats, err := json.Marshal(r.Stats)
	if err != nil {
		return false, err
	}
	n, err := s.q.InsertRelease(ctx, releasesql.InsertReleaseParams{
		ID: r.ID, ProjectID: r.ProjectID, Version: int32Of(r.Version), ParentID: nullID(r.Parent),
		Environment: r.Environment, Policy: policy, Branch: text(r.Branch), Content: content, ManifestDigest: r.Digest, Stats: stats,
		Note: r.Note, CreatedBy: r.Author, CreatedAt: r.CreatedAt,
	})
	return n == 1, storeError(err)
}

func (s *store) Release(ctx context.Context, project, id uuid.UUID) (domain.Release, error) {
	row, err := s.q.GetRelease(ctx, releasesql.GetReleaseParams{ProjectID: project, ID: id})
	if err != nil {
		return domain.Release{}, storeError(err)
	}
	return release(row)
}

func (s *store) Releases(ctx context.Context, project uuid.UUID, beforeVersion, limit int) ([]domain.Release, error) {
	rows, err := s.q.ListReleases(ctx, releasesql.ListReleasesParams{
		ProjectID: project, BeforeVersion: int32Of(beforeVersion), MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Release, 0, len(rows))
	for _, r := range rows {
		rel, err := release(r)
		if err != nil {
			return nil, err
		}
		out = append(out, rel)
	}
	return out, nil
}

func (s *store) MaxReleaseVersion(ctx context.Context, project uuid.UUID) (int, error) {
	n, err := s.q.MaxReleaseVersion(ctx, project)
	return int(n), storeError(err)
}

// ── deployments ─────────────────────────────────────────────────────

func (s *store) AppendDeployment(ctx context.Context, d domain.Deployment) error {
	return storeError(s.q.InsertDeployment(ctx, releasesql.InsertDeploymentParams{
		ProjectID: d.ProjectID, Environment: d.Environment, Number: int32Of(d.Number), ReleaseID: d.ReleaseID,
		PreviousReleaseID: nullID(d.Previous), Action: string(d.Action), CreatedBy: d.By, CreatedAt: d.CreatedAt,
		Forced: d.Override.Forced, ForceReason: d.Override.Reason,
	}))
}

func (s *store) LastDeploymentNumber(ctx context.Context, project uuid.UUID, environment string) (int, error) {
	n, err := s.q.LastDeploymentNumber(ctx, releasesql.LastDeploymentNumberParams{ProjectID: project, Environment: environment})
	return int(n), storeError(err)
}

func (s *store) Deployments(ctx context.Context, project uuid.UUID, environment string, before, limit int) ([]domain.Deployment, error) {
	rows, err := s.q.ListDeployments(ctx, releasesql.ListDeploymentsParams{
		ProjectID: project, Environment: environment, BeforeNumber: int32Of(before), MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Deployment, len(rows))
	for i, r := range rows {
		out[i] = domain.Deployment{
			ProjectID: r.ProjectID, Environment: r.Environment, Number: int(r.Number), ReleaseID: r.ReleaseID,
			Previous: r.PreviousReleaseID.UUID, Action: domain.Action(r.Action), By: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(),
			Override: domain.Override{Forced: r.Forced, Reason: r.ForceReason},
		}
	}
	return out, nil
}

func (s *store) RollbackTarget(ctx context.Context, project uuid.UUID, environment string, version int) (domain.Release, error) {
	row, err := s.q.RollbackTarget(ctx, releasesql.RollbackTargetParams{
		ProjectID: project, Environment: environment, CurrentVersion: int32Of(version),
	})
	if err != nil {
		return domain.Release{}, storeError(err)
	}
	return release(row)
}

func (s *store) ServedIn(ctx context.Context, project uuid.UUID, environment string, id uuid.UUID) (bool, error) {
	ok, err := s.q.ServedInEnvironment(ctx, releasesql.ServedInEnvironmentParams{ProjectID: project, Environment: environment, ReleaseID: id})
	return ok, storeError(err)
}

// ── delivery keys ───────────────────────────────────────────────────

func deliveryKey(r releasesql.ReleaseDeliveryKey) domain.DeliveryKey {
	k := domain.DeliveryKey{
		ID: r.ID, ProjectID: r.ProjectID, Key: r.Key, Name: r.Name,
		Scope:     delivery.Scope{Environments: r.Environments, Branches: r.Branches},
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(),
	}
	if k.Scope.Environments == nil {
		k.Scope.Environments = []string{}
	}
	if r.RevokedAt.Valid {
		at := r.RevokedAt.Time.UTC()
		k.RevokedAt, k.RevokedBy = &at, r.RevokedBy.String
	}
	return k
}

func (s *store) InsertDeliveryKey(ctx context.Context, k domain.DeliveryKey) (bool, error) {
	envs := k.Scope.Environments
	if envs == nil {
		envs = []string{}
	}
	n, err := s.q.InsertDeliveryKey(ctx, releasesql.InsertDeliveryKeyParams{
		ID: k.ID, ProjectID: k.ProjectID, Key: k.Key, Name: k.Name, Environments: envs, Branches: k.Scope.Branches,
		CreatedBy: k.CreatedBy, CreatedAt: k.CreatedAt,
	})
	return n == 1, storeError(err)
}

func (s *store) MarkKeyIndexed(ctx context.Context, id uuid.UUID, version int) error {
	return storeError(s.q.MarkKeyIndexed(ctx, releasesql.MarkKeyIndexedParams{ID: id, IndexVersion: int16(version)})) //nolint:gosec // a small format number
}

func (s *store) DeliveryKey(ctx context.Context, project, id uuid.UUID, lock bool) (domain.DeliveryKey, error) {
	var (
		row releasesql.ReleaseDeliveryKey
		err error
	)
	if lock {
		row, err = s.q.LockDeliveryKey(ctx, releasesql.LockDeliveryKeyParams{ProjectID: project, ID: id})
	} else {
		row, err = s.q.GetDeliveryKey(ctx, releasesql.GetDeliveryKeyParams{ProjectID: project, ID: id})
	}
	if err != nil {
		return domain.DeliveryKey{}, storeError(err)
	}
	return deliveryKey(row), nil
}

func (s *store) DeliveryKeys(ctx context.Context, project, after uuid.UUID, limit int) ([]domain.DeliveryKey, error) {
	rows, err := s.q.ListDeliveryKeys(ctx, releasesql.ListDeliveryKeysParams{ProjectID: project, After: after, MaxRows: int32Of(limit)})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.DeliveryKey, len(rows))
	for i, r := range rows {
		out[i] = deliveryKey(r)
	}
	return out, nil
}

func (s *store) ActiveDeliveryKeys(ctx context.Context, project uuid.UUID) ([]domain.DeliveryKey, error) {
	rows, err := s.q.ActiveDeliveryKeys(ctx, project)
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.DeliveryKey, len(rows))
	for i, r := range rows {
		out[i] = deliveryKey(r)
	}
	return out, nil
}

func (s *store) SetDeliveryKeyScope(ctx context.Context, k domain.DeliveryKey) error {
	envs := k.Scope.Environments
	if envs == nil {
		envs = []string{}
	}
	n, err := s.q.SetDeliveryKeyScope(ctx, releasesql.SetDeliveryKeyScopeParams{
		Environments: envs, Branches: k.Scope.Branches, ProjectID: k.ProjectID, ID: k.ID,
	})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}

func (s *store) RevokeDeliveryKey(ctx context.Context, k domain.DeliveryKey) error {
	if k.RevokedAt == nil {
		return fmt.Errorf("release: key %s is not revoked", k.ID)
	}
	_, err := s.q.RevokeDeliveryKey(ctx, releasesql.RevokeDeliveryKeyParams{
		RevokedAt: pgtype.Timestamptz{Time: *k.RevokedAt, Valid: true}, RevokedBy: pgtype.Text{String: k.RevokedBy, Valid: true},
		ProjectID: k.ProjectID, ID: k.ID,
	})
	return storeError(err)
}

// ── publish requests ────────────────────────────────────────────────

func (s *store) PublishRequest(ctx context.Context, project uuid.UUID, environment string) (domain.PublishRequest, error) {
	r, err := s.q.LockPublishRequest(ctx, releasesql.LockPublishRequestParams{ProjectID: project, Environment: environment})
	if err != nil {
		return domain.PublishRequest{}, storeError(err)
	}
	return domain.PublishRequest{
		ProjectID: r.ProjectID, Environment: r.Environment, ID: r.RequestID,
		FirstRequestedAt: r.FirstRequestedAt.UTC(), NotBefore: r.NotBefore.UTC(), By: r.RequestedBy,
	}, nil
}

func (s *store) SavePublishRequest(ctx context.Context, r domain.PublishRequest) error {
	return storeError(s.q.SavePublishRequest(ctx, releasesql.SavePublishRequestParams{
		ProjectID: r.ProjectID, Environment: r.Environment, RequestID: r.ID,
		FirstRequestedAt: r.FirstRequestedAt, NotBefore: r.NotBefore, RequestedBy: r.By,
	}))
}

func (s *store) DeletePublishRequest(ctx context.Context, r domain.PublishRequest) error {
	_, err := s.q.DeletePublishRequest(ctx, releasesql.DeletePublishRequestParams{
		ProjectID: r.ProjectID, Environment: r.Environment, RequestID: r.ID,
	})
	return storeError(err)
}

// ── events ──────────────────────────────────────────────────────────

func (s *store) Publish(ctx context.Context, e outbox.Event) error {
	_, err := outbox.Publish(ctx, s.tx, e)
	return err
}
