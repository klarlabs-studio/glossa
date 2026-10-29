package app

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/defaults"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// EnsureDefault seeds the default review definition
// (defaults/review.json) for the tenant on ctx, once (RFC 0006 §2.3).
// It is a document like any other: compiled and stored as version 1 of
// a tenant-wide definition, editable and versioned from then on. It is
// not bound to anything — a project without a binding behaves as after
// M4 — and a tenant that deletes it is never given it back. A tenant
// that already has a tenant-wide definition called "review" keeps its
// own.
//
// "First use" is the tenant's creation (identity.tenant.created) or,
// for tenants that existed before the runner, the first workflow event
// it handles there. It is safe to call from anywhere and at any time.
func (r *Runner) EnsureDefault(ctx context.Context) error {
	tenant, ok := tenancy.FromContext(ctx)
	if !ok {
		return errors.New("workflow: seeding needs a tenant")
	}
	if _, done := r.seeded.Load(tenant); done {
		return nil
	}
	d, err := domain.Compile(defaults.Review())
	if err != nil {
		return err // the embedded document is the platform's own; a test keeps it compiling
	}
	by := authz.SystemEventActor(PrincipalSeed).String()
	now := r.now()
	err = r.d.Tx.InTenant(ctx, func(ctx context.Context, st InstanceStore) error {
		seeded, err := st.Seeded(ctx)
		if err != nil || seeded {
			return err
		}
		taken, err := st.HasTenantDefinition(ctx, d.Name)
		if err != nil {
			return err
		}
		var (
			rec *domain.DefinitionRecord
			ver *domain.Version
		)
		if !taken {
			rec = &domain.DefinitionRecord{ID: uuid.New(), Name: d.Name, Subject: d.Subject, Latest: 1, CreatedBy: by, CreatedAt: now}
			v := domain.FirstVersion(*rec, d, by, now)
			ver = &v
		}
		_, err = st.SeedDefault(ctx, rec, ver, now)
		return err
	})
	if errors.Is(err, ErrConflict) {
		err = nil // a concurrent seeding won
	}
	if err == nil {
		r.seeded.Store(tenant, struct{}{})
	}
	return err
}

// releaseDefault is the version a release request runs on when no
// binding names one (RFC 0006 §5.1): the latest version of the tenant's
// definition called release-approval, seeded from
// defaults/release-approval.json in the step's transaction when the
// tenant has none — also after the tenant deleted it, because a release
// request with no workflow would wait forever. A tenant definition of
// that name about another subject is the tenant's own and is left
// alone: then nothing runs, and the request waits for a binding.
func (r *Runner) releaseDefault(ctx context.Context, st InstanceStore) (domain.Version, bool, error) {
	rec, err := st.TenantDefinition(ctx, defaults.ReleaseApprovalName)
	switch {
	case err == nil:
		if rec.Subject != domain.SubjectReleaseRequest {
			r.d.Logger.WarnContext(ctx, "workflow: the tenant's release-approval definition is not about release requests; "+
				"release requests run only where a binding names a definition")
			return domain.Version{}, false, nil
		}
		v, err := st.Version(ctx, rec.ID, rec.Latest)
		return v, err == nil, err
	case !errors.Is(err, ErrNotFound):
		return domain.Version{}, false, err
	}
	d, err := domain.Compile(defaults.ReleaseApproval())
	if err != nil {
		return domain.Version{}, false, err // the embedded document is the platform's own; a test keeps it compiling
	}
	now := r.now()
	by := authz.SystemEventActor(PrincipalSeed).String()
	rec = domain.DefinitionRecord{ID: uuid.New(), Name: d.Name, Subject: d.Subject, Latest: 1, CreatedBy: by, CreatedAt: now}
	v := domain.FirstVersion(rec, d, by, now)
	if err := st.InsertDefinition(ctx, rec, v); err != nil {
		return domain.Version{}, false, err
	}
	return v, true, nil
}
