// Package postgres implements Context's persistence port on the
// kernel's unit of work, with sqlc queries over the context_* tables:
// the tenant-scoped Store and the cross-tenant Sweeper (system scope
// context.retention).
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"go.klarlabs.de/glossa/platform/internal/context/adapters/postgres/contextsql"
	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// insertBatch bounds the rows one INSERT … unnest statement carries.
const insertBatch = 5000

// Transactor implements app.Transactor.
type Transactor struct{ uow *db.UnitOfWork }

// NewTransactor returns a Transactor on uow.
func NewTransactor(uow *db.UnitOfWork) *Transactor { return &Transactor{uow: uow} }

// InTenant implements app.Transactor.
func (t *Transactor) InTenant(ctx context.Context, fn func(context.Context, app.Store) error) error {
	return t.uow.InTenantTx(ctx, func(ctx context.Context, tx *db.TenantTx) error {
		return fn(ctx, &store{tx: tx, q: contextsql.New(tx)})
	})
}

type store struct {
	tx *db.TenantTx
	q  *contextsql.Queries
}

var _ app.Store = (*store)(nil)

func storeError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return app.ErrNotFound
	}
	return err
}

func int32Of(n int) int32 { return int32(n) } //nolint:gosec // positions, counts, pixels and limits are bounded by the domain

func idOrNil(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

func uuidPtr(n uuid.NullUUID) *uuid.UUID {
	if !n.Valid {
		return nil
	}
	id := n.UUID
	return &id
}

// ── builds ──────────────────────────────────────────────────────────

func build(r contextsql.ContextBuild) domain.Build {
	return domain.Build{
		ID: r.ID, ProjectID: r.ProjectID, ApplicationID: r.ApplicationID, Commit: domain.Commit(r.CommitSha),
		Branch: domain.Branch(r.Branch), OnDefaultBranch: r.OnDefaultBranch, Source: domain.Source(r.Source),
		Tool: domain.Tool{Name: r.ToolName, Version: r.ToolVersion}, Digest: domain.Digest(r.Digest),
		UsageCount: int(r.UsageCount), CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(),
	}
}

func (s *store) InsertBuild(ctx context.Context, b domain.Build) (bool, error) {
	n, err := s.q.InsertBuild(ctx, contextsql.InsertBuildParams{
		ID: b.ID, ProjectID: b.ProjectID, ApplicationID: b.ApplicationID, CommitSha: b.Commit.String(),
		Branch: b.Branch.String(), OnDefaultBranch: b.OnDefaultBranch, Source: string(b.Source),
		ToolName: b.Tool.Name, ToolVersion: b.Tool.Version, Digest: b.Digest.String(),
		UsageCount: int32Of(b.UsageCount), CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt,
	})
	return n == 1, storeError(err)
}

func (s *store) BuildByUpload(ctx context.Context, application uuid.UUID, commit domain.Commit, source domain.Source, digest domain.Digest) (domain.Build, error) {
	r, err := s.q.GetBuildByUpload(ctx, contextsql.GetBuildByUploadParams{
		ApplicationID: application, CommitSha: commit.String(), Source: string(source), Digest: digest.String(),
	})
	if err != nil {
		return domain.Build{}, storeError(err)
	}
	return build(r), nil
}

func (s *store) Build(ctx context.Context, id uuid.UUID) (domain.Build, error) {
	r, err := s.q.GetBuild(ctx, id)
	if err != nil {
		return domain.Build{}, storeError(err)
	}
	return build(r), nil
}

func (s *store) Builds(ctx context.Context, ids []uuid.UUID) ([]domain.Build, error) {
	rows, err := s.q.ListBuildsByIDs(ctx, ids)
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.Build, len(rows))
	for i, r := range rows {
		out[i] = build(r)
	}
	return out, nil
}

func (s *store) BuildSummaries(ctx context.Context, project uuid.UUID) ([]domain.BuildSummary, error) {
	rows, err := s.q.ListProjectBuildSummaries(ctx, project)
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]domain.BuildSummary, len(rows))
	for i, r := range rows {
		out[i] = domain.BuildSummary{
			ID: r.ID, ApplicationID: r.ApplicationID, Source: domain.Source(r.Source), Branch: domain.Branch(r.Branch),
			OnDefaultBranch: r.OnDefaultBranch, CreatedAt: r.CreatedAt.UTC(),
		}
	}
	return out, nil
}

func (s *store) DeleteBuilds(ctx context.Context, ids []uuid.UUID) (int, error) {
	n, err := s.q.DeleteBuilds(ctx, ids)
	return int(n), storeError(err)
}

func (s *store) DeleteProjectData(ctx context.Context, project uuid.UUID) error {
	return storeError(s.q.DeleteProjectBuilds(ctx, project))
}

func (s *store) DeleteApplicationData(ctx context.Context, project, application uuid.UUID) error {
	return storeError(s.q.DeleteApplicationBuilds(ctx, contextsql.DeleteApplicationBuildsParams{
		ProjectID: project, ApplicationID: application,
	}))
}

// ── usages ──────────────────────────────────────────────────────────

func (s *store) InsertUsages(ctx context.Context, buildID uuid.UUID, usages []domain.Usage) error {
	for start := 0; start < len(usages); start += insertBatch {
		batch := usages[start:min(start+insertBatch, len(usages))]
		p := contextsql.InsertUsagesParams{BuildID: buildID}
		for i, u := range batch {
			p.Positions = append(p.Positions, int32Of(start+i))
			p.Keys = append(p.Keys, u.Key)
			p.MessageIds = append(p.MessageIds, idOrNil(u.MessageID))
			p.Files = append(p.Files, u.File)
			p.Lines = append(p.Lines, int32Of(u.Line))
			p.Cols = append(p.Cols, int32Of(u.Column))
			p.Components = append(p.Components, u.Component)
			p.Routes = append(p.Routes, u.Route)
			p.Kinds = append(p.Kinds, u.Kind)
		}
		if err := s.q.InsertUsages(ctx, p); err != nil {
			return storeError(err)
		}
	}
	return nil
}

func (s *store) CountUnknownKeys(ctx context.Context, buildID uuid.UUID) (int, error) {
	n, err := s.q.CountUnknownKeys(ctx, buildID)
	return int(n), storeError(err)
}

func column(c pgtype.Int4) int {
	if !c.Valid {
		return 0
	}
	return int(c.Int32)
}

func (s *store) MessageUsages(ctx context.Context, message uuid.UUID, builds []uuid.UUID, limit int) ([]app.UsageView, error) {
	rows, err := s.q.ListMessageUsages(ctx, contextsql.ListMessageUsagesParams{
		MessageID: uuid.NullUUID{UUID: message, Valid: true}, BuildIds: builds, MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.UsageView, len(rows))
	for i, r := range rows {
		out[i] = app.UsageView{
			Usage: domain.Usage{
				Key: r.MessageKey, File: r.File, Line: int(r.Line), Column: column(r.Col), Component: r.Component,
				Route: r.Route, Kind: r.Kind, MessageID: uuidPtr(r.MessageID),
			},
			Position: int(r.Position), BuildID: r.BuildID, ApplicationID: r.ApplicationID, Commit: domain.Commit(r.CommitSha),
			Branch: domain.Branch(r.Branch), OnDefaultBranch: r.OnDefaultBranch, Source: domain.Source(r.Source),
		}
	}
	return out, nil
}

func (s *store) UsedMessages(ctx context.Context, builds []uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.q.ListUsedMessageIDs(ctx, builds)
	return ids, storeError(err)
}

func (s *store) ListBuilds(ctx context.Context, project uuid.UUID, application *uuid.UUID, after *app.BuildCursor, limit int) ([]app.BuildRecord, error) {
	p := contextsql.ListProjectBuildsParams{ProjectID: project, MaxRows: int32Of(limit)}
	if application != nil {
		p.ApplicationID = uuid.NullUUID{UUID: *application, Valid: true}
	}
	if after != nil {
		p.AfterCreatedAt = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		p.AfterID = uuid.NullUUID{UUID: after.ID, Valid: true}
	}
	rows, err := s.q.ListProjectBuilds(ctx, p)
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.BuildRecord, len(rows))
	for i, r := range rows {
		out[i] = app.BuildRecord{UnknownKeys: int(r.UnknownKeys), Build: build(contextsql.ContextBuild{
			ID: r.ID, TenantID: r.TenantID, ProjectID: r.ProjectID, ApplicationID: r.ApplicationID, CommitSha: r.CommitSha,
			Branch: r.Branch, OnDefaultBranch: r.OnDefaultBranch, Source: r.Source, ToolName: r.ToolName,
			ToolVersion: r.ToolVersion, Digest: r.Digest, UsageCount: r.UsageCount, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
		})}
	}
	return out, nil
}

func optionalText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func (s *store) ListUsages(ctx context.Context, builds []uuid.UUID, f app.UsageFilter, after app.UsageCursor, limit int) ([]app.UsageView, error) {
	rows, err := s.q.ListUsagesInBuilds(ctx, contextsql.ListUsagesInBuildsParams{
		BuildIds: builds, Route: optionalText(f.Route), Component: optionalText(f.Component), File: optionalText(f.File),
		Unknown: f.Unknown, AfterBuild: after.Build, AfterPosition: int32Of(after.Position), MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.UsageView, len(rows))
	for i, r := range rows {
		out[i] = app.UsageView{
			Usage: domain.Usage{
				Key: r.MessageKey, File: r.File, Line: int(r.Line), Column: column(r.Col), Component: r.Component,
				Route: r.Route, Kind: r.Kind, MessageID: uuidPtr(r.MessageID),
			},
			Position: int(r.Position), BuildID: r.BuildID, ApplicationID: r.ApplicationID, Commit: domain.Commit(r.CommitSha),
			Branch: domain.Branch(r.Branch), OnDefaultBranch: r.OnDefaultBranch, Source: domain.Source(r.Source),
		}
	}
	return out, nil
}

func (s *store) CoLocatedMessages(ctx context.Context, message uuid.UUID, builds []uuid.UUID, limit int) ([]uuid.UUID, error) {
	rows, err := s.q.ListCoLocatedMessages(ctx, contextsql.ListCoLocatedMessagesParams{
		MessageID: message, BuildIds: builds, MaxRows: int32Of(limit),
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		out[i] = r.MessageID
	}
	return out, nil
}

// ── captures ────────────────────────────────────────────────────────

func (s *store) LockBuildCaptures(ctx context.Context, buildID uuid.UUID) error {
	return storeError(s.q.LockBuildCaptures(ctx, buildID.String()))
}

func (s *store) CountCaptures(ctx context.Context, buildID uuid.UUID) (int, error) {
	n, err := s.q.CountBuildCaptures(ctx, buildID)
	return int(n), storeError(err)
}

func (s *store) InsertCapture(ctx context.Context, c domain.Capture) (bool, error) {
	n, err := s.q.InsertCapture(ctx, contextsql.InsertCaptureParams{
		ID: c.ID, BuildID: c.BuildID, ProjectID: c.ProjectID, Route: c.Route,
		ViewportWidth: int32Of(c.Viewport.Width), ViewportHeight: int32Of(c.Viewport.Height), Locale: c.Locale.String(),
		ImageDigest: c.Image.Digest.String(), ImageWidth: int32Of(c.Image.Width), ImageHeight: int32Of(c.Image.Height),
		ImageBytes: c.Image.Bytes, CreatedBy: c.CreatedBy, CreatedAt: c.CreatedAt,
	})
	if err != nil || n == 0 {
		return false, storeError(err)
	}
	p := contextsql.InsertRegionsParams{CaptureID: c.ID}
	for i, r := range c.Regions {
		p.Positions = append(p.Positions, int32Of(i))
		p.Keys = append(p.Keys, r.Key)
		p.MessageIds = append(p.MessageIds, idOrNil(r.MessageID))
		p.Kinds = append(p.Kinds, string(r.Kind))
		p.Xs = append(p.Xs, int32Of(r.Box.X))
		p.Ys = append(p.Ys, int32Of(r.Box.Y))
		p.Widths = append(p.Widths, int32Of(r.Box.Width))
		p.Heights = append(p.Heights, int32Of(r.Box.Height))
		p.Visibles = append(p.Visibles, r.Visible)
	}
	if len(c.Regions) > 0 {
		if err := s.q.InsertRegions(ctx, p); err != nil {
			return false, storeError(err)
		}
	}
	return true, nil
}

// PreviousCaptureOfScope is the capture the application showed last of
// one (route, viewport, locale) — the previous sighting the visual
// layer's two-sighting rule counts against (RFC 0005 §5.2). The upload
// that asks has not stored its own captures yet, so the newest row is
// the one immediately before it.
func (s *store) PreviousCaptureOfScope(
	ctx context.Context, project, application uuid.UUID, route string, v domain.Viewport, locale bcp47.Tag,
) (uuid.UUID, error) {
	id, err := s.q.GetPreviousCaptureOfScope(ctx, contextsql.GetPreviousCaptureOfScopeParams{
		ProjectID: project, ApplicationID: application, Route: route,
		ViewportWidth: int32Of(v.Width), ViewportHeight: int32Of(v.Height), Locale: locale.String(),
	})
	if err != nil {
		return uuid.Nil, storeError(err)
	}
	return id, nil
}

func (s *store) CaptureByShot(ctx context.Context, buildID uuid.UUID, route string, v domain.Viewport, locale bcp47.Tag) (domain.Capture, error) {
	r, err := s.q.GetCaptureByShot(ctx, contextsql.GetCaptureByShotParams{
		BuildID: buildID, Route: route, ViewportWidth: int32Of(v.Width), ViewportHeight: int32Of(v.Height),
		Locale: locale.String(),
	})
	if err != nil {
		return domain.Capture{}, storeError(err)
	}
	c, err := capture(r)
	if err != nil {
		return domain.Capture{}, err
	}
	regions, err := s.q.ListRegions(ctx, r.ID)
	if err != nil {
		return domain.Capture{}, storeError(err)
	}
	c.Regions = make([]domain.Region, len(regions))
	for i, g := range regions {
		c.Regions[i] = region(g)
	}
	return c, nil
}

// capture converts a stored capture, without its regions.
func capture(r contextsql.ContextCapture) (domain.Capture, error) {
	tag, err := bcp47.Parse(r.Locale)
	if err != nil {
		return domain.Capture{}, fmt.Errorf("context: stored locale %q: %w", r.Locale, err)
	}
	return domain.Capture{
		ID: r.ID, ProjectID: r.ProjectID, BuildID: r.BuildID, Route: r.Route,
		Viewport: domain.Viewport{Width: int(r.ViewportWidth), Height: int(r.ViewportHeight)}, Locale: tag,
		Image: domain.Image{Digest: domain.Digest(r.ImageDigest), Width: int(r.ImageWidth), Height: int(r.ImageHeight),
			Bytes: r.ImageBytes},
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt.UTC(),
	}, nil
}

func region(g contextsql.ContextRegion) domain.Region {
	return domain.Region{
		Key: g.MessageKey, MessageID: uuidPtr(g.MessageID), Kind: domain.RegionKind(g.Kind),
		Box: domain.Box{X: int(g.X), Y: int(g.Y), Width: int(g.Width), Height: int(g.Height)}, Visible: g.Visible,
	}
}

func (s *store) Capture(ctx context.Context, id uuid.UUID) (domain.Capture, error) {
	r, err := s.q.GetCapture(ctx, id)
	if err != nil {
		return domain.Capture{}, storeError(err)
	}
	return capture(r)
}

func (s *store) CaptureShows(ctx context.Context, capture uuid.UUID, messages []uuid.UUID) (bool, error) {
	if len(messages) == 0 {
		return false, nil
	}
	return s.q.CaptureShowsAny(ctx, contextsql.CaptureShowsAnyParams{CaptureID: capture, MessageIds: messages})
}

func (s *store) UnknownRegionKeys(ctx context.Context, buildID uuid.UUID) ([]string, error) {
	keys, err := s.q.ListBuildUnknownRegionKeys(ctx, buildID)
	return keys, storeError(err)
}

func (s *store) MessageCaptures(ctx context.Context, message uuid.UUID, builds []uuid.UUID, limit int) ([]app.CaptureView, error) {
	rows, err := s.q.ListMessageCaptures(ctx, contextsql.ListMessageCapturesParams{
		BuildIds: builds, MessageID: message, MaxRows: int32Of(limit),
	})
	if err != nil || len(rows) == 0 {
		return nil, storeError(err)
	}
	out := make([]app.CaptureView, len(rows))
	index := make(map[uuid.UUID]int, len(rows))
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		c, err := capture(contextsql.ContextCapture{
			ID: r.ID, BuildID: r.BuildID, ProjectID: r.ProjectID, Route: r.Route, ViewportWidth: r.ViewportWidth,
			ViewportHeight: r.ViewportHeight, Locale: r.Locale, ImageDigest: r.ImageDigest, ImageWidth: r.ImageWidth,
			ImageHeight: r.ImageHeight, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
		})
		if err != nil {
			return nil, err
		}
		out[i] = app.CaptureView{
			Capture: c, ApplicationID: r.ApplicationID, Commit: domain.Commit(r.CommitSha), Branch: domain.Branch(r.Branch),
			OnDefaultBranch: r.OnDefaultBranch,
		}
		index[r.ID], ids[i] = i, r.ID
	}
	regions, err := s.q.ListMessageRegions(ctx, contextsql.ListMessageRegionsParams{CaptureIds: ids, MessageID: message})
	if err != nil {
		return nil, storeError(err)
	}
	for _, g := range regions {
		c := &out[index[g.CaptureID]]
		c.Regions = append(c.Regions, region(g))
	}
	return out, nil
}

func (s *store) CapturedMessages(ctx context.Context, builds []uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.q.ListCapturedMessageIDs(ctx, builds)
	return ids, storeError(err)
}

func (s *store) ProjectImages(ctx context.Context, project uuid.UUID) ([]domain.Digest, error) {
	rows, err := s.q.ListProjectImages(ctx, project)
	return digests(rows), storeError(err)
}

func digests(ss []string) []domain.Digest {
	out := make([]domain.Digest, len(ss))
	for i, s := range ss {
		out[i] = domain.Digest(s)
	}
	return out
}

func (s *store) CountCapturesOfBuilds(ctx context.Context, builds []uuid.UUID) (int, error) {
	if len(builds) == 0 {
		return 0, nil
	}
	n, err := s.q.CountCapturesOfBuilds(ctx, builds)
	return int(n), storeError(err)
}

func (s *store) BuildImages(ctx context.Context, builds []uuid.UUID) ([]domain.Digest, error) {
	rows, err := s.q.ListBuildImages(ctx, builds)
	return digests(rows), storeError(err)
}

func (s *store) ReferencedImages(ctx context.Context, project uuid.UUID, ds []domain.Digest) ([]domain.Digest, error) {
	if len(ds) == 0 {
		return nil, nil
	}
	ss := make([]string, len(ds))
	for i, d := range ds {
		ss[i] = d.String()
	}
	rows, err := s.q.ListReferencedImages(ctx, contextsql.ListReferencedImagesParams{ProjectID: project, Digests: ss})
	return digests(rows), storeError(err)
}

func (s *store) StoredImageBytes(ctx context.Context) (int64, error) {
	n, err := s.q.GetTenantImageBytes(ctx)
	return n, storeError(err)
}

func (s *store) Publish(ctx context.Context, e outbox.Event) error {
	_, err := outbox.Publish(ctx, s.tx, e)
	return err
}

// ── system scope ────────────────────────────────────────────────────

// Sweeper implements app.Sweeper in the system scope context.retention,
// which migration 0012 opens to reading context_builds' tenant_id and
// project_id only.
type Sweeper struct {
	uow   *db.UnitOfWork
	scope db.SystemScope
}

// NewSweeper returns a sweeper on uow.
func NewSweeper(uow *db.UnitOfWork) *Sweeper {
	return &Sweeper{uow: uow, scope: db.NewSystemScope("context.retention")}
}

var _ app.Sweeper = (*Sweeper)(nil)

// ProjectsWithBuilds implements app.Sweeper.
func (s *Sweeper) ProjectsWithBuilds(ctx context.Context) ([]app.ProjectRef, error) {
	var out []app.ProjectRef
	err := s.uow.InSystemTx(ctx, s.scope, func(ctx context.Context, tx *db.SystemTx) error {
		rows, err := contextsql.New(tx).ListProjectsWithBuilds(ctx)
		for _, r := range rows {
			out = append(out, app.ProjectRef{Tenant: tenancy.ID(r.TenantID), Project: r.ProjectID})
		}
		return err
	})
	return out, err
}
