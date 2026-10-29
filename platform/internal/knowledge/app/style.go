package app

import (
	"context"
	"strconv"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// NewStyleGuide is a guide to create: its scope and content.
type NewStyleGuide struct {
	ProjectID *uuid.UUID
	// Locale "" means every locale.
	Locale string
	// Namespace "" means every namespace; it needs a project.
	Namespace string
	domain.StyleInput
}

// CreateStyleGuide adds the guide for a scope; each scope has at most
// one (ErrStyleGuideExists). A repeated idemKey returns the first
// request's guide with replayed set. Needs knowledge.write.
func (s *Service) CreateStyleGuide(ctx context.Context, in NewStyleGuide, idemKey string) (g domain.StyleGuide, replayed bool, err error) {
	by, err := writeScope(ctx, authz.KnowledgeWrite, in.ProjectID)
	if err != nil {
		return domain.StyleGuide{}, false, err
	}
	scope := domain.StyleScope{ProjectID: in.ProjectID, Namespace: in.Namespace}
	if in.Locale != "" {
		tag, err := bcp47.Parse(in.Locale)
		if err != nil {
			return domain.StyleGuide{}, false, err
		}
		scope.Locale = &tag
	}
	id, err := idempotentID(ctx, "style_guide.create", by, idemKey)
	if err != nil {
		return domain.StyleGuide{}, false, err
	}
	if g, err = domain.NewStyleGuide(id, scope, in.StyleInput, by, s.now()); err != nil {
		return domain.StyleGuide{}, false, err
	}
	if err := s.requireProject(ctx, in.ProjectID); err != nil {
		return domain.StyleGuide{}, false, err
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		inserted, err := st.InsertStyleGuide(ctx, g)
		if err != nil {
			return err
		}
		if !inserted {
			first, err := st.StyleGuide(ctx, g.ID)
			if err != nil {
				return err
			}
			if !sameScope(first.Scope, scope) {
				return ErrIdempotencyReuse
			}
			g, replayed = first, true
			return nil
		}
		return s.recordStyleGuide(ctx, st, g, ActionCreated, by)
	})
	return g, replayed, err
}

func sameScope(a, b domain.StyleScope) bool {
	sameLocale := (a.Locale == nil) == (b.Locale == nil) && (a.Locale == nil || *a.Locale == *b.Locale)
	return sameProject(a.ProjectID, b.ProjectID) && sameLocale && a.Namespace == b.Namespace
}

func (s *Service) recordStyleGuide(ctx context.Context, st Store, g domain.StyleGuide, action RevisionAction, by string) error {
	if err := st.AppendStyleGuideVersion(ctx, StyleGuideVersion{Guide: g, Action: action, Author: by, CreatedAt: s.now()}); err != nil {
		return err
	}
	typ := map[RevisionAction]string{
		ActionCreated: domain.EventStyleGuideCreated, ActionUpdated: domain.EventStyleGuideUpdated,
		ActionDeleted: domain.EventStyleGuideDeleted,
	}[action]
	return st.Publish(ctx, outbox.Event{
		Type: typ, AggregateType: domain.AggregateStyleGuide, AggregateID: g.ID.String(), Actor: outbox.Actor(by),
		Payload: domain.StyleGuideEventOf(g, by),
	})
}

// GetStyleGuide returns one guide. Needs knowledge.read.
func (s *Service) GetStyleGuide(ctx context.Context, id uuid.UUID) (domain.StyleGuide, error) {
	if err := authz.Require(ctx, authz.KnowledgeRead); err != nil {
		return domain.StyleGuide{}, err
	}
	var g domain.StyleGuide
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		g, err = st.StyleGuide(ctx, id)
		return err
	})
	if err == nil {
		err = rowScope(ctx, g.Scope.ProjectID)
	}
	if err != nil {
		return domain.StyleGuide{}, err
	}
	return g, nil
}

// ListStyleGuides lists guides, optionally only tenant-level ones, one
// project's, or one locale's. Needs knowledge.read.
func (s *Service) ListStyleGuides(ctx context.Context, f StyleFilter, page pagination.Page) ([]domain.StyleGuide, *string, error) {
	projects, err := listScope(ctx, authz.KnowledgeRead, f.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	f.Projects = projects
	after, err := afterUUID(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.StyleGuide
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rows, err = st.StyleGuides(ctx, f, after, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(g domain.StyleGuide) string { return g.ID.String() })
	return items, next, nil
}

// ReplaceStyleGuide replaces a guide's name, fields and rules if it is
// still at ifMatch; its scope never changes. Unchanged content is no new
// version. Needs knowledge.write.
func (s *Service) ReplaceStyleGuide(ctx context.Context, id uuid.UUID, in domain.StyleInput, ifMatch int) (domain.StyleGuide, error) {
	by, err := actor(ctx, authz.KnowledgeWrite)
	if err != nil {
		return domain.StyleGuide{}, err
	}
	var g domain.StyleGuide
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if g, err = st.LockStyleGuide(ctx, id); err != nil {
			return err
		}
		if err := rowScope(ctx, g.Scope.ProjectID); err != nil {
			return err
		}
		if _, err := writeScope(ctx, authz.KnowledgeWrite, g.Scope.ProjectID); err != nil {
			return err
		}
		if err := checkIfMatch(g.Version, &ifMatch); err != nil {
			return err
		}
		expected := g.Version
		changed, err := g.Replace(in, by, s.now())
		if err != nil || !changed {
			return err
		}
		if err := st.UpdateStyleGuide(ctx, g, expected); err != nil {
			return err
		}
		return s.recordStyleGuide(ctx, st, g, ActionUpdated, by)
	})
	return g, err
}

// DeleteStyleGuide deletes a guide; its versions stay, ending in a
// deleted entry. ifMatch, when given, must be its version. Needs
// knowledge.write.
func (s *Service) DeleteStyleGuide(ctx context.Context, id uuid.UUID, ifMatch *int) error {
	by, err := actor(ctx, authz.KnowledgeWrite)
	if err != nil {
		return err
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		g, err := st.LockStyleGuide(ctx, id)
		if err != nil {
			return err
		}
		if err := rowScope(ctx, g.Scope.ProjectID); err != nil {
			return err
		}
		if _, err := writeScope(ctx, authz.KnowledgeWrite, g.Scope.ProjectID); err != nil {
			return err
		}
		if ifMatch != nil && *ifMatch != g.Version {
			return ErrPreconditionFailed
		}
		if err := st.DeleteStyleGuide(ctx, id); err != nil {
			return err
		}
		g.Version++
		g.UpdatedBy, g.UpdatedAt = by, s.now()
		return s.recordStyleGuide(ctx, st, g, ActionDeleted, by)
	})
}

// StyleGuideVersions lists a guide's versions, newest first — also after
// it was deleted. Needs knowledge.read.
func (s *Service) StyleGuideVersions(ctx context.Context, id uuid.UUID, page pagination.Page) ([]StyleGuideVersion, *string, error) {
	if err := authz.Require(ctx, authz.KnowledgeRead); err != nil {
		return nil, nil, err
	}
	before, err := beforeVersion(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []StyleGuideVersion
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rows, err = st.StyleGuideVersions(ctx, id, before, page.Limit())
		if err == nil && len(rows) == 0 && page.After == "" {
			return ErrNotFound
		}
		if err == nil && len(rows) > 0 {
			err = rowScope(ctx, rows[0].Guide.Scope.ProjectID)
		}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(v StyleGuideVersion) string { return strconv.Itoa(v.Guide.Version) })
	return items, next, nil
}

// StyleQuery asks which style applies to a message.
type StyleQuery struct {
	// ProjectID nil asks for tenant-level guides only.
	ProjectID *uuid.UUID
	// Locale zero leaves out every locale-scoped guide.
	Locale    bcp47.Tag
	Namespace string
}

// EffectiveStyle merges the guides that apply to a project, locale and
// namespace (domain.EffectiveStyleOf) and names the versions it used:
// the style_rules tool, and what a suggestion's provenance records.
// Needs knowledge.read.
//
// An assigned member asks it for a project and a locale one of their
// units is in: translating the unit needs it (RFC 0006 §3.3).
func (s *Service) EffectiveStyle(ctx context.Context, q StyleQuery) (domain.EffectiveStyle, error) {
	if err := readFor(ctx, authz.KnowledgeRead, q.ProjectID, q.Locale); err != nil {
		return domain.EffectiveStyle{}, err
	}
	var guides []domain.StyleGuide
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		guides, err = st.StyleGuidesInScope(ctx, q.ProjectID)
		return err
	})
	if err != nil {
		return domain.EffectiveStyle{}, err
	}
	return domain.EffectiveStyleOf(guides, q.ProjectID, q.Locale, q.Namespace), nil
}
