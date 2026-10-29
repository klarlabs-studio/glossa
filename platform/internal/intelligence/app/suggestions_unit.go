package app

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
)

// MaxUnitSuggestions is how many suggestions a unit's workspace reads.
const MaxUnitSuggestions = 5

// UnitSuggestion is a suggestion as a unit's workspace shows it. The
// record carries more than a translator needs — the job, provider,
// model, cost, calls and the translation-memory units it drew on — and
// that stays with whoever reads suggestions project-wide.
type UnitSuggestion struct {
	Record domain.SuggestionRecord
	// Outdated says the message's source has been revised since.
	Outdated bool
	// Decidable says the caller may accept or reject it (not an
	// `assigned` member, whose work is writing the translation itself).
	Decidable bool
}

// UnitSuggestions returns the newest suggestions for one unit — the
// message a key names, in a locale — newest first. It is the read a
// vendor member's workspace depends on (RFC 0006 §3.3). It needs
// intelligence.read for the locale and the unit in the caller's scope:
// a project outside it, and for an `assigned` member a unit no
// assignment of theirs covers, are ErrNotFound.
func (s *Service) UnitSuggestions(ctx context.Context, project uuid.UUID, key string, locale bcp47.Tag) ([]UnitSuggestion, error) {
	if err := authz.RequireProject(ctx, authz.IntelligenceRead, project); err != nil {
		return nil, notVisible(err)
	}
	// Catalog finds the key for those who may see its message only.
	found, err := s.Catalog.MessagesByKeys(ctx, project, []string{key})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, ErrNotFound
	}
	m := found[0]
	loc, err := authz.ParseLocale(locale.String())
	if err != nil {
		return nil, err
	}
	if err := authz.RequireUnit(ctx, authz.IntelligenceRead, project, m.ID, loc); err != nil {
		return nil, notVisible(err)
	}
	var rows []domain.SuggestionRecord
	err = s.Tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rows, err = st.Suggestions(ctx, SuggestionFilter{
			ProjectID: &project, MessageID: &m.ID, Locale: locale.String(), Projects: []uuid.UUID{project},
		}, nil, MaxUnitSuggestions)
		return err
	})
	if err != nil {
		return nil, err
	}
	p, _ := authz.From(ctx)
	out := make([]UnitSuggestion, len(rows))
	for i, r := range rows {
		out[i] = UnitSuggestion{Record: r, Outdated: r.SourceRevision < m.Revision, Decidable: !p.Assigned()}
	}
	return out, nil
}

func notVisible(err error) error {
	if errors.Is(err, authz.ErrNotVisible) {
		return ErrNotFound
	}
	return err
}
