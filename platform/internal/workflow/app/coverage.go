package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Coverage implements authz.Coverage over assignments (RFC 0006 §3.3):
// a member sees a translation unit while an assignment given to them
// directly, to a group they are in or to their vendor is open or
// accepted, or for domain.CoverageWindow after it was completed.
//
// An assignment given to a role covers no one: a role names everyone
// who holds it, and "every translator" must not let every vendor's
// translator see the work. Declined and expired assignments cover
// nothing.
//
// It checks no permission: it is a filter read paths apply after their
// own checks, not an operation of its own. It reads inside the read
// path's transaction when there is one.
type Coverage struct {
	tx  WorkTransactor
	dir Directory
	now func() time.Time
}

var _ authz.Coverage = (*Coverage)(nil)

// NewCoverage returns the Coverage read port. now may be nil for the
// wall clock.
func NewCoverage(tx WorkTransactor, dir Directory, now func() time.Time) *Coverage {
	if now == nil {
		now = time.Now
	}
	return &Coverage{tx: tx, dir: dir, now: now}
}

// Covers implements authz.Coverage.
func (c *Coverage) Covers(ctx context.Context, member identity.MemberID, project, message uuid.UUID, locale string) (bool, error) {
	tag, err := bcp47.Parse(locale)
	if err != nil {
		return false, nil
	}
	unit := domain.Unit{Message: message, Locale: tag.String()}
	units, err := c.units(ctx, member, project, &unit)
	return len(units) > 0, err
}

// Covered implements authz.Coverage.
func (c *Coverage) Covered(ctx context.Context, member identity.MemberID, project uuid.UUID) (authz.CoveredSet, error) {
	units, err := c.units(ctx, member, project, nil)
	if err != nil {
		return authz.CoveredSet{}, err
	}
	out := make([]authz.Unit, len(units))
	for i, u := range units {
		out[i] = authz.Unit{Message: u.Message, Locale: u.Locale}
	}
	return authz.NewCoveredSet(out...), nil
}

func (c *Coverage) units(ctx context.Context, member identity.MemberID, project uuid.UUID, unit *domain.Unit) ([]domain.Unit, error) {
	if member.IsZero() || project == uuid.Nil {
		return nil, nil
	}
	var out []domain.Unit
	err := c.tx.InTenant(ctx, func(ctx context.Context, st WorkStore) error {
		aff, err := c.dir.Affiliation(ctx, member.UUID())
		if errors.Is(err, ErrNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		out, err = st.CoveredUnits(ctx, CoverageQuery{
			Project: project, Assignees: aff.visibilityKeys(),
			DoneSince: c.now().UTC().Add(-domain.CoverageWindow), Unit: unit,
		})
		return err
	})
	return out, err
}
