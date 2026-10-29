// Package identity is Localization's adapter over Identity's tenant
// store: who else could review a locale.
package identity

import (
	"context"

	"github.com/google/uuid"

	identityapp "go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
)

// TenantReader runs a read on Identity's tenant store, joining the
// tenant transaction ctx is already in. Identity's postgres Transactor
// implements it (InCurrentTenant).
type TenantReader interface {
	InCurrentTenant(ctx context.Context, fn func(context.Context, identityapp.TenantStore) error) error
}

// Reviewers implements app.Reviewers. It reads the store, not the
// application service, because the question is about other members and
// the asker (an author) holds no members.read.
type Reviewers struct{ tx TenantReader }

var _ app.Reviewers = Reviewers{}

// NewReviewers returns Reviewers on Identity's store.
func NewReviewers(tx TenantReader) Reviewers { return Reviewers{tx: tx} }

const pageSize = 100

// OthersCanReview implements app.Reviewers. A member counts when they
// are active, are not excluding, hold translations.review for locale
// through their roles and locale scope, have a project scope that
// includes project, and read the whole tenant: a member limited to their
// assignments cannot review (RFC 0006 §3.3), so they are no second pair
// of eyes.
func (r Reviewers) OthersCanReview(ctx context.Context, project uuid.UUID, locale bcp47.Tag, excluding string) (bool, error) {
	l, err := domain.ParseLocale(locale.String())
	if err != nil {
		return true, nil // not a locale anyone can hold: stay strict
	}
	ref, err := domain.ParseProjectRef(project.String())
	if err != nil {
		return true, nil
	}
	found := false
	err = r.tx.InCurrentTenant(ctx, func(ctx context.Context, st identityapp.TenantStore) error {
		var after domain.MemberID
		for !found {
			page, err := st.Members(ctx, after, pageSize)
			if err != nil {
				return err
			}
			for _, m := range page {
				if counts(m.Member, ref, l, excluding) {
					found = true
					break
				}
			}
			if len(page) < pageSize {
				return nil
			}
			after = page[len(page)-1].ID
		}
		return nil
	})
	return found, err
}

func counts(m domain.Member, project domain.ProjectRef, l domain.Locale, excluding string) bool {
	if m.Status != domain.MemberActive || m.PersonID.IsZero() ||
		domain.PersonActor(m.PersonID).String() == excluding {
		return false
	}
	if m.Restriction.Visibility == domain.VisibilityAssigned || !m.Restriction.Projects.Covers(project) {
		return false
	}
	return domain.GrantForMember(m.Roles, m.Locales).AllowsFor(domain.PermTranslationsReview, l)
}
