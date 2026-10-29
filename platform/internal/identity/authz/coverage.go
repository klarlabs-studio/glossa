package authz

import (
	"context"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

// Coverage is the read port assignment-scoped visibility filters
// through (RFC 0006 §3.3): which translation units a member with
// visibility `assigned` may see.
//
// Workflow's assignments implement it; every context's read path
// consults it, through this package, for a principal whose restriction
// is `assigned`. It is declared here, beside the rest of what a read
// path asks authz, so that no context has to import Workflow to be
// filtered by it (RFC 0006 §2.1 rule 3).
//
// A member sees a unit while an assignment given to them — directly,
// through a group they belong to, or through their vendor — is open or
// accepted, or was completed within the last 30 days.
type Coverage interface {
	// Covers reports whether member may see message in locale within
	// project. locale is a canonical BCP 47 tag.
	Covers(ctx context.Context, member domain.MemberID, project uuid.UUID, message uuid.UUID, locale string) (bool, error)
	// Covered is every unit member may see within project, for list
	// paths that filter a page rather than test one row.
	Covered(ctx context.Context, member domain.MemberID, project uuid.UUID) (CoveredSet, error)
}

// Unit is one translation unit: a message in a locale.
type Unit struct {
	Message uuid.UUID
	// Locale is a canonical BCP 47 tag.
	Locale string
}

// CoveredSet is the units a member may see in one project.
type CoveredSet struct{ units map[Unit]struct{} }

// NewCoveredSet builds a set from units.
func NewCoveredSet(units ...Unit) CoveredSet {
	s := CoveredSet{units: make(map[Unit]struct{}, len(units))}
	for _, u := range units {
		s.units[u] = struct{}{}
	}
	return s
}

// Has reports whether the set covers message in locale.
func (s CoveredSet) Has(message uuid.UUID, locale string) bool {
	_, ok := s.units[Unit{Message: message, Locale: locale}]
	return ok
}

// HasMessage reports whether the set covers message in any locale: the
// source side of a unit (its message, description, usages) is visible
// to whoever may see one of its translations.
func (s CoveredSet) HasMessage(message uuid.UUID) bool {
	for u := range s.units {
		if u.Message == message {
			return true
		}
	}
	return false
}

// Len is how many units the set covers.
func (s CoveredSet) Len() int { return len(s.units) }
