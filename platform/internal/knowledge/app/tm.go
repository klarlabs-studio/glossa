package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// Lookup limits.
const (
	DefaultTMLimit = 5
	MaxTMLimit     = 50
	MaxQueryRunes  = 200
)

// TMQuery asks translation memory for matches of one source message.
type TMQuery struct {
	// ProjectID is the project the message belongs to; nil for text
	// outside any project.
	ProjectID *uuid.UUID
	// AllProjects also searches other projects' units; by default a
	// lookup sees tenant-wide units and ProjectID's.
	AllProjects  bool
	SourceLocale bcp47.Tag
	TargetLocale bcp47.Tag
	Source       mf.Message
	// MessageKey and Namespace are the message's context: an exact match
	// approved for the same key in the same namespace scores 101.
	MessageKey string
	Namespace  string
	// Limit is the number of matches (default 5, at most 50); MinScore
	// the lowest score returned (default and minimum 50; 100 asks for
	// exact matches only).
	Limit    int
	MinScore int
	// CountHits records the lookup in each returned unit's usage.
	CountHits bool
	// TargetSyntax is the syntax of each match's TargetText: MF1 when it
	// can express the target (MF2 with SyntaxFallback otherwise), or MF2
	// ("" too).
	TargetSyntax mfcontent.Syntax
}

// TMMatch is one translation-memory match.
type TMMatch struct {
	Unit  domain.TMUnit
	Score int
	Kind  domain.MatchKind
	// Target is the unit's target with its variables renamed to the
	// query's by position, and TargetMF2 its MF2 syntax; Adapted is false
	// when a variable had no counterpart and kept its name.
	Target    mf.Message
	TargetMF2 string
	Adapted   bool
	// TargetText is Target in TargetSyntax: the syntax the query asked
	// for, or MF2 with SyntaxFallback when MF1 can't express it.
	TargetText     string
	TargetSyntax   mfcontent.Syntax
	SyntaxFallback bool
}

func (q *TMQuery) normalize() error {
	if q.SourceLocale.IsZero() || q.TargetLocale.IsZero() {
		return fmt.Errorf("%w: source and target locales are required", ErrInvalidQuery)
	}
	if q.Limit == 0 {
		q.Limit = DefaultTMLimit
	}
	if q.MinScore == 0 {
		q.MinScore = domain.MinFuzzyScore
	}
	if q.Limit < 1 || q.Limit > MaxTMLimit {
		return fmt.Errorf("%w: limit must be 1 to %d", ErrInvalidQuery, MaxTMLimit)
	}
	if q.MinScore < domain.MinFuzzyScore || q.MinScore > domain.ScoreContext {
		return fmt.Errorf("%w: min_score must be %d to %d", ErrInvalidQuery, domain.MinFuzzyScore, domain.ScoreContext)
	}
	var err error
	q.TargetSyntax, err = mfcontent.ParseSyntax(string(q.TargetSyntax), mfcontent.MF2)
	return err
}

// LookupTM finds exact (100, or 101 in context) and fuzzy (50–99,
// pg_trgm similarity of the normalized text) matches for a source
// message in a locale pair, best first. Units with the same target are
// returned once, at their best score. Needs knowledge.read.
//
// A lookup in a project outside the caller's scope is not found, one
// over every project is refused to a caller limited to some (RFC 0006
// §4.1), and an assigned member — who never searches the tenant's TM
// (§3.3) — is refused.
func (s *Service) LookupTM(ctx context.Context, q TMQuery) ([]TMMatch, error) {
	if err := tmScope(ctx, authz.KnowledgeRead, q.ProjectID, q.AllProjects); err != nil {
		return nil, err
	}
	return s.lookup(ctx, q)
}

// lookup is LookupTM after the caller's access has been decided.
func (s *Service) lookup(ctx context.Context, q TMQuery) ([]TMMatch, error) {
	if err := q.normalize(); err != nil {
		return nil, err
	}
	norm := domain.Normalize(q.Source)
	scope := MatchScope{ProjectID: q.ProjectID, AllProjects: q.AllProjects}
	var matches []TMMatch
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		exact, err := st.ExactMatches(ctx, scope, q.SourceLocale, q.TargetLocale, norm, q.Limit*3)
		if err != nil {
			return err
		}
		for _, u := range exact {
			score, kind := domain.ScoreExact, domain.MatchExact
			if inContext(u, q) {
				score, kind = domain.ScoreContext, domain.MatchContext
			}
			matches = append(matches, TMMatch{Unit: u, Score: score, Kind: kind})
		}
		if q.MinScore <= domain.MaxFuzzyScore {
			fuzzy, err := st.FuzzyMatches(ctx, scope, q.SourceLocale, q.TargetLocale, norm.Text,
				float64(q.MinScore)/100, q.Limit*3)
			if err != nil {
				return err
			}
			for _, u := range fuzzy {
				if u.SourceNorm.Hash == norm.Hash && u.SourceNorm.Signature == norm.Signature {
					continue // an exact match, already listed
				}
				if score := domain.FuzzyScore(u.Similarity); score >= q.MinScore {
					matches = append(matches, TMMatch{Unit: u.TMUnit, Score: score, Kind: domain.MatchFuzzy})
				}
			}
		}
		matches = best(matches, q)
		if !q.CountHits || len(matches) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, len(matches))
		for i, m := range matches {
			ids[i] = m.Unit.ID
		}
		return st.CountHits(ctx, ids, s.now())
	})
	if err != nil {
		return nil, err
	}
	for i := range matches {
		if err := adapt(&matches[i], norm, q.TargetSyntax, q.TargetLocale); err != nil {
			return nil, err
		}
	}
	return matches, nil
}

func inContext(u domain.TMUnit, q TMQuery) bool {
	return q.MessageKey != "" && u.MessageKey == q.MessageKey && u.Namespace == q.Namespace &&
		q.ProjectID != nil && u.ProjectID != nil && *u.ProjectID == *q.ProjectID
}

// best keeps each target once at its best score, ranks by score, then
// the query's own project, then the most recently confirmed unit, and
// cuts at the limit.
func best(ms []TMMatch, q TMQuery) []TMMatch {
	own := func(m TMMatch) int {
		if q.ProjectID != nil && m.Unit.ProjectID != nil && *m.Unit.ProjectID == *q.ProjectID {
			return 1
		}
		return 0
	}
	slices.SortStableFunc(ms, func(a, b TMMatch) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		if c := cmp.Compare(own(b), own(a)); c != 0 {
			return c
		}
		if c := b.Unit.UpdatedAt.Compare(a.Unit.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.Unit.ID.String(), b.Unit.ID.String())
	})
	seen := map[string]bool{}
	out := ms[:0]
	for _, m := range ms {
		key := m.Unit.SourceNorm.Hash + "\x00" + m.Unit.TargetMF2
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
		if len(out) == q.Limit {
			break
		}
	}
	return out
}

// adapt renames the match's target variables to the query's and writes
// it in the syntax asked for.
func adapt(m *TMMatch, query domain.Normalized, syntax mfcontent.Syntax, locale bcp47.Tag) error {
	target, complete := domain.AdaptVariables(m.Unit.Target, m.Unit.SourceNorm.Vars, query.Vars)
	text, err := mf.Stringify(target)
	if err != nil {
		return fmt.Errorf("knowledge: adapt unit %s: %w", m.Unit.ID, err)
	}
	m.Target, m.TargetMF2, m.Adapted = target, text, complete
	m.TargetText, m.TargetSyntax = text, mfcontent.MF2
	if syntax == mfcontent.MF1 {
		if mf1, err := mfcontent.RenderMF1(target, locale); err == nil {
			m.TargetText, m.TargetSyntax = mf1, mfcontent.MF1
		} else {
			m.SyntaxFallback = true
		}
	}
	return nil
}

// ConcordanceQuery searches translation memory for a phrase.
type ConcordanceQuery struct {
	Query        string
	Side         domain.Side
	SourceLocale *bcp47.Tag
	TargetLocale *bcp47.Tag
	ProjectID    *uuid.UUID
	AllProjects  bool
	Limit        int
}

// ConcordanceMatch is a unit containing the phrase.
type ConcordanceMatch struct {
	Unit domain.TMUnit
	// Similarity is pg_trgm's word similarity of the phrase to the side
	// searched (1 when it contains the phrase as whole words).
	Similarity float64
}

// Concordance finds active units whose normalized source (or target)
// contains a phrase, case-insensitively, closest first: how a translator
// checks how a phrase was translated before. Needs knowledge.read.
func (s *Service) Concordance(ctx context.Context, q ConcordanceQuery) ([]ConcordanceMatch, error) {
	if err := tmScope(ctx, authz.KnowledgeRead, q.ProjectID, q.AllProjects); err != nil {
		return nil, err
	}
	q.Query = strings.TrimSpace(q.Query)
	if q.Query == "" || utf8.RuneCountInString(q.Query) > MaxQueryRunes {
		return nil, fmt.Errorf("%w: q must be 1 to %d characters", ErrInvalidQuery, MaxQueryRunes)
	}
	if q.Side == "" {
		q.Side = domain.SideSource
	}
	if q.Side != domain.SideSource && q.Side != domain.SideTarget {
		return nil, fmt.Errorf("%w: side must be source or target", ErrInvalidQuery)
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	if q.Limit < 1 || q.Limit > MaxTMLimit {
		return nil, fmt.Errorf("%w: limit must be 1 to %d", ErrInvalidQuery, MaxTMLimit)
	}
	var out []ConcordanceMatch
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rows, err := st.Concordance(ctx, ConcordanceFilter{
			MatchScope: MatchScope{ProjectID: q.ProjectID, AllProjects: q.AllProjects}, Side: q.Side,
			Query: q.Query, SourceLocale: q.SourceLocale, TargetLocale: q.TargetLocale, Limit: q.Limit,
		})
		for _, r := range rows {
			out = append(out, ConcordanceMatch{Unit: r.TMUnit, Similarity: r.Similarity})
		}
		return err
	})
	return out, err
}

// ListUnits lists TM units, active, retired or both, in a stable order.
// Needs knowledge.read.
func (s *Service) ListUnits(ctx context.Context, f UnitFilter, page pagination.Page) ([]domain.TMUnit, *string, error) {
	projects, err := listScope(ctx, authz.KnowledgeRead, f.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	f.Projects = projects
	switch f.State {
	case "":
		f.State = "active"
	case "active", "retired", "all":
	default:
		return nil, nil, fmt.Errorf("%w: state must be active, retired or all", ErrInvalidQuery)
	}
	after, err := afterUUID(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []domain.TMUnit
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		rows, err = st.Units(ctx, f, after, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(u domain.TMUnit) string { return u.ID.String() })
	return items, next, nil
}

// GetUnit returns one TM unit, active or retired. Needs knowledge.read.
func (s *Service) GetUnit(ctx context.Context, id uuid.UUID) (domain.TMUnit, error) {
	if err := authz.Require(ctx, authz.KnowledgeRead); err != nil {
		return domain.TMUnit{}, err
	}
	var u domain.TMUnit
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		u, err = st.Unit(ctx, id)
		return err
	})
	if err == nil {
		err = rowScope(ctx, u.ProjectID)
	}
	if err != nil {
		return domain.TMUnit{}, err
	}
	return u, nil
}

// RetireUnit takes a unit out of matching by hand (reason deleted); it
// stays listed as history. Retiring a retired unit changes nothing. A
// later approval of its translation derives a new unit. Needs
// knowledge.write.
func (s *Service) RetireUnit(ctx context.Context, id uuid.UUID) error {
	by, err := actor(ctx, authz.KnowledgeWrite)
	if err != nil {
		return err
	}
	return s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		u, err := st.LockUnit(ctx, id)
		if err != nil {
			return err
		}
		if err := rowScope(ctx, u.ProjectID); err != nil {
			return err
		}
		if _, err := writeScope(ctx, authz.KnowledgeWrite, u.ProjectID); err != nil {
			return err
		}
		if !u.Active() {
			return nil
		}
		if err := u.Retire(domain.RetireDeleted, by, s.now()); err != nil {
			return err
		}
		return st.RetireUnit(ctx, u)
	})
}
