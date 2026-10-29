package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// MaxCheckTextBytes bounds a text recognized or checked in one call.
const MaxCheckTextBytes = 20000

// CreateConcept adds a concept with its terms, tenant-wide (project nil)
// or to a project. A repeated idemKey returns the first request's
// concept with replayed set. Needs knowledge.write.
func (s *Service) CreateConcept(ctx context.Context, project *uuid.UUID, in domain.ConceptInput, idemKey string) (c domain.Concept, replayed bool, err error) {
	by, err := writeScope(ctx, authz.KnowledgeWrite, project)
	if err != nil {
		return domain.Concept{}, false, err
	}
	id, err := idempotentID(ctx, "concept.create", by, idemKey)
	if err != nil {
		return domain.Concept{}, false, err
	}
	if c, err = domain.NewConcept(id, project, in, by, s.now()); err != nil {
		return domain.Concept{}, false, err
	}
	if err := s.requireProject(ctx, project); err != nil {
		return domain.Concept{}, false, err
	}
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		inserted, err := st.InsertConcept(ctx, c)
		if err != nil {
			return err
		}
		if !inserted {
			first, err := st.Concept(ctx, c.ID)
			if err != nil {
				return err
			}
			if !sameProject(first.ProjectID, project) {
				return ErrIdempotencyReuse
			}
			c, replayed = first, true
			return nil
		}
		return s.recordConcept(ctx, st, c, ActionCreated, by)
	})
	return c, replayed, err
}

func sameProject(a, b *uuid.UUID) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// recordConcept appends the concept's history entry and publishes its
// event.
func (s *Service) recordConcept(ctx context.Context, st Store, c domain.Concept, action RevisionAction, by string) error {
	if err := st.AppendConceptRevision(ctx, ConceptRevision{Concept: c, Action: action, Author: by, CreatedAt: s.now()}); err != nil {
		return err
	}
	typ := map[RevisionAction]string{
		ActionCreated: domain.EventConceptCreated, ActionUpdated: domain.EventConceptUpdated,
		ActionDeleted: domain.EventConceptDeleted,
	}[action]
	return st.Publish(ctx, outbox.Event{
		Type: typ, AggregateType: domain.AggregateConcept, AggregateID: c.ID.String(), Actor: outbox.Actor(by),
		Payload: domain.ConceptEventOf(c, by),
	})
}

// GetConcept returns a concept with its terms. Needs knowledge.read.
func (s *Service) GetConcept(ctx context.Context, id uuid.UUID) (domain.Concept, error) {
	if err := authz.Require(ctx, authz.KnowledgeRead); err != nil {
		return domain.Concept{}, err
	}
	var c domain.Concept
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		c, err = st.Concept(ctx, id)
		return err
	})
	if err == nil {
		err = rowScope(ctx, c.ProjectID)
	}
	if err != nil {
		return domain.Concept{}, err
	}
	return c, nil
}

// ListConcepts lists concepts, optionally only those that apply to a
// project, of a domain, with a term in a locale, or matching a search.
// Needs knowledge.read.
func (s *Service) ListConcepts(ctx context.Context, f ConceptFilter, page pagination.Page) ([]domain.Concept, *string, error) {
	projects, err := listScope(ctx, authz.KnowledgeRead, f.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	f.Projects = projects
	if f.Query != nil {
		q := strings.TrimSpace(*f.Query)
		if q == "" || utf8.RuneCountInString(q) > MaxQueryRunes {
			return nil, nil, fmt.Errorf("%w: q must be 1 to %d characters", ErrInvalidQuery, MaxQueryRunes)
		}
		f.Query = &q
	}
	after, err := afterUUID(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.Concept
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rows, err = st.Concepts(ctx, f, after, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(c domain.Concept) string { return c.ID.String() })
	return items, next, nil
}

// ReplaceConcept replaces a concept's content and terms if it is still
// at ifMatch. Unchanged content is no new version. Needs
// knowledge.write.
func (s *Service) ReplaceConcept(ctx context.Context, id uuid.UUID, in domain.ConceptInput, ifMatch int) (domain.Concept, error) {
	by, err := actor(ctx, authz.KnowledgeWrite)
	if err != nil {
		return domain.Concept{}, err
	}
	var c domain.Concept
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		if c, err = st.LockConcept(ctx, id); err != nil {
			return err
		}
		if err := rowScope(ctx, c.ProjectID); err != nil {
			return err
		}
		if _, err := writeScope(ctx, authz.KnowledgeWrite, c.ProjectID); err != nil {
			return err
		}
		if err := checkIfMatch(c.Version, &ifMatch); err != nil {
			return err
		}
		expected := c.Version
		changed, err := c.Replace(in, by, s.now())
		if err != nil || !changed {
			return err
		}
		if err := st.UpdateConcept(ctx, c, expected); err != nil {
			return err
		}
		return s.recordConcept(ctx, st, c, ActionUpdated, by)
	})
	return c, err
}

// DeleteConcept deletes a concept and its terms; its history stays,
// ending in a deleted entry. ifMatch, when given, must be its version.
// Needs knowledge.write.
func (s *Service) DeleteConcept(ctx context.Context, id uuid.UUID, ifMatch *int) error {
	by, err := actor(ctx, authz.KnowledgeWrite)
	if err != nil {
		return err
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		c, err := st.LockConcept(ctx, id)
		if err != nil {
			return err
		}
		if err := rowScope(ctx, c.ProjectID); err != nil {
			return err
		}
		if _, err := writeScope(ctx, authz.KnowledgeWrite, c.ProjectID); err != nil {
			return err
		}
		if ifMatch != nil && *ifMatch != c.Version {
			return ErrPreconditionFailed
		}
		if err := st.DeleteConcept(ctx, id); err != nil {
			return err
		}
		c.Version++
		c.UpdatedBy, c.UpdatedAt = by, s.now()
		return s.recordConcept(ctx, st, c, ActionDeleted, by)
	})
}

// ConceptRevisions lists a concept's history, newest first — also after
// it was deleted. Needs knowledge.read.
func (s *Service) ConceptRevisions(ctx context.Context, id uuid.UUID, page pagination.Page) ([]ConceptRevision, *string, error) {
	if err := authz.Require(ctx, authz.KnowledgeRead); err != nil {
		return nil, nil, err
	}
	before, err := beforeVersion(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []ConceptRevision
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rows, err = st.ConceptRevisions(ctx, id, before, page.Limit())
		if err == nil && len(rows) == 0 && page.After == "" {
			return ErrNotFound
		}
		if err == nil && len(rows) > 0 {
			err = rowScope(ctx, rows[0].Concept.ProjectID)
		}
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(r ConceptRevision) string { return strconv.Itoa(r.Concept.Version) })
	return items, next, nil
}

// termbase loads the concepts in scope with terms in the locales (and
// their truncations: a de term applies to de-AT text).
func (s *Service) termbase(ctx context.Context, project *uuid.UUID, locales ...bcp47.Tag) (*domain.Termbase, error) {
	var tags []bcp47.Tag
	for _, l := range locales {
		tags = append(tags, l)
		tags = append(tags, l.Truncations()...)
	}
	var concepts []domain.Concept
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		concepts, err = st.ConceptsWithTermsIn(ctx, project, tags)
		return err
	})
	if err != nil {
		return nil, err
	}
	return domain.NewTermbase(concepts), nil
}

// TermQuery asks which termbase terms a text contains.
type TermQuery struct {
	// ProjectID adds the project's concepts to the tenant-wide ones.
	ProjectID *uuid.UUID
	Text      string
	Locale    bcp47.Tag
	// TargetLocale, when set, adds each concept's terms in that locale.
	TargetLocale *bcp47.Tag
}

// RecognizedTerm is a term found in a text, with its concept and, when
// asked for, the concept's terms in the target locale (allowed ones
// first, then deprecated and forbidden).
type RecognizedTerm struct {
	domain.TermHit
	Concept domain.Concept
	Targets []domain.Term
}

// RecognizeTerms finds termbase terms in a text (see domain's
// recognition rules): the term_lookup tool, and Studio's highlighting.
// Needs knowledge.read.
//
// An assigned member asks it for a project and the target locale of one
// of their units: the termbase translating it needs (RFC 0006 §3.3).
func (s *Service) RecognizeTerms(ctx context.Context, q TermQuery) ([]RecognizedTerm, error) {
	target := bcp47.Tag{}
	if q.TargetLocale != nil {
		target = *q.TargetLocale
	}
	if err := readFor(ctx, authz.KnowledgeRead, q.ProjectID, target); err != nil {
		return nil, err
	}
	if q.Locale.IsZero() || len(q.Text) > MaxCheckTextBytes {
		return nil, fmt.Errorf("%w: a locale and at most %d bytes of text", ErrInvalidQuery, MaxCheckTextBytes)
	}
	locales := []bcp47.Tag{q.Locale}
	if q.TargetLocale != nil {
		locales = append(locales, *q.TargetLocale)
	}
	tb, err := s.termbase(ctx, q.ProjectID, locales...)
	if err != nil {
		return nil, err
	}
	hits := tb.Recognize(q.Text, q.Locale)
	out := make([]RecognizedTerm, len(hits))
	for i, h := range hits {
		c, _ := tb.Concept(h.ConceptID)
		out[i] = RecognizedTerm{TermHit: h, Concept: c}
		if q.TargetLocale != nil {
			out[i].Targets = targetTerms(c, *q.TargetLocale)
		}
	}
	return out, nil
}

// targetTerms lists c's terms for locale, allowed ones first.
func targetTerms(c domain.Concept, locale bcp47.Tag) []domain.Term {
	var allowed, other []domain.Term
	for _, t := range c.TermsIn(locale) {
		if t.Status.Allowed() {
			allowed = append(allowed, t)
		} else {
			other = append(other, t)
		}
	}
	return append(allowed, other...)
}

// TermCheck asks whether a translation follows the termbase.
type TermCheck struct {
	ProjectID    *uuid.UUID
	Source       string
	SourceLocale bcp47.Tag
	Target       string
	TargetLocale bcp47.Tag
}

// CheckTerminology runs terminology QA (term_missing, term_forbidden) on
// a translation — plain text; for a message, pass domain.VisibleText of
// each side. Needs knowledge.read.
func (s *Service) CheckTerminology(ctx context.Context, c TermCheck) ([]domain.TermFinding, error) {
	if err := readFor(ctx, authz.KnowledgeRead, c.ProjectID, c.TargetLocale); err != nil {
		return nil, err
	}
	if c.SourceLocale.IsZero() || c.TargetLocale.IsZero() ||
		len(c.Source) > MaxCheckTextBytes || len(c.Target) > MaxCheckTextBytes {
		return nil, fmt.Errorf("%w: both locales and at most %d bytes per text", ErrInvalidQuery, MaxCheckTextBytes)
	}
	tb, err := s.termbase(ctx, c.ProjectID, c.SourceLocale, c.TargetLocale)
	if err != nil {
		return nil, err
	}
	return domain.CheckTerminology(tb, c.Source, c.SourceLocale, c.Target, c.TargetLocale), nil
}
