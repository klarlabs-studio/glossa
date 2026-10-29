package app

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

// UnitTMQuery asks for the translation-memory matches of one
// translation unit: the message a key names, in a locale.
type UnitTMQuery struct {
	Project uuid.UUID
	// Key is the message's key in Project.
	Key    string
	Locale bcp47.Tag
	// Limit and MinScore are as in TMQuery (defaults 5 and 50).
	Limit    int
	MinScore int
	// TargetSyntax is the syntax of each TargetText ("": the source's).
	TargetSyntax mfcontent.Syntax
}

// UnitTMMatch is a match as a unit's workspace shows it: the text of
// what was remembered and how well it fits, never which unit it is.
// TMMatch.Unit is always zero.
type UnitTMMatch struct {
	TMMatch
	// SourceNormalized is the remembered source, normalized (how a fuzzy
	// match differs from the unit's source).
	SourceNormalized string
	// ProjectScoped says the remembered unit belongs to the project (not
	// to the whole tenant).
	ProjectScoped bool
	// MessageKey is the key of the message the match was learned from.
	// Empty for an imported unit — and always for an `assigned` member,
	// who learns no key but their own units' (RFC 0006 §3.3, §9).
	MessageKey string
}

// UnitTMResult is the matches of a unit with the unit's own normalized
// source.
type UnitTMResult struct {
	SourceNormalized string
	Matches          []UnitTMMatch
}

// UnitTMMatches finds the memory's matches for one unit — the workspace
// read a vendor member depends on, because tenant TM search is refused
// to them (RFC 0006 §3.3). It needs knowledge.read for the locale and
// the unit in the caller's scope: a project outside it, and for an
// `assigned` member a unit no assignment of theirs covers, are
// ErrNotFound. The lookup is the project's (tenant-wide units and the
// project's), counts no hits, and for an `assigned` caller leaves out
// every identifier and key of what it matched.
func (s *Service) UnitTMMatches(ctx context.Context, q UnitTMQuery) (UnitTMResult, error) {
	if s.messages == nil {
		return UnitTMResult{}, errors.New("knowledge: no message port configured")
	}
	if err := authz.RequireProject(ctx, authz.KnowledgeRead, q.Project); err != nil {
		return UnitTMResult{}, notVisible(err)
	}
	// Catalog resolves the key for those who may see the message; the
	// unit check then decides the locale.
	m, err := s.messages.Message(ctx, q.Project, q.Key)
	if err != nil {
		return UnitTMResult{}, err
	}
	loc, err := authz.ParseLocale(q.Locale.String())
	if err != nil {
		return UnitTMResult{}, err
	}
	if err := authz.RequireUnit(ctx, authz.KnowledgeRead, q.Project, m.ID, loc); err != nil {
		return UnitTMResult{}, notVisible(err)
	}
	syntax := q.TargetSyntax
	if syntax == "" {
		syntax = m.SourceSyntax
	}
	matches, err := s.lookup(ctx, TMQuery{
		ProjectID: &q.Project, SourceLocale: m.SourceLocale, TargetLocale: q.Locale, Source: m.Source,
		MessageKey: m.Key, Namespace: m.Namespace, Limit: q.Limit, MinScore: q.MinScore, TargetSyntax: syntax,
	})
	if err != nil {
		return UnitTMResult{}, err
	}
	p, _ := authz.From(ctx)
	out := UnitTMResult{SourceNormalized: domain.Normalize(m.Source).Text, Matches: make([]UnitTMMatch, len(matches))}
	for i, t := range matches {
		u := UnitTMMatch{SourceNormalized: t.Unit.SourceNorm.Text, ProjectScoped: t.Unit.ProjectID != nil, TMMatch: t}
		if !p.Assigned() {
			u.MessageKey = t.Unit.MessageKey
		}
		u.TMMatch.Unit = domain.TMUnit{} // never leaves the use case
		out.Matches[i] = u
	}
	return out, nil
}

// notVisible is a resource outside the caller's scope: not found.
func notVisible(err error) error {
	if errors.Is(err, authz.ErrNotVisible) {
		return ErrNotFound
	}
	return err
}
