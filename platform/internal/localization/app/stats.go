package app

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// LocaleCounts is one locale's row of the stored summary.
type LocaleCounts struct {
	Code     bcp47.Tag
	IsSource bool
	States   domain.StateCounts
	Outdated int
}

// StoredStats is what one summary query returns: the project's active
// messages and the counts of every locale it has.
type StoredStats struct {
	Messages int
	Locales  []LocaleCounts
}

// LocaleStats is a locale with its coverage.
type LocaleStats struct {
	Code     bcp47.Tag
	IsSource bool
	domain.Coverage
}

// Direction is the locale's text direction.
func (l LocaleStats) Direction() bcp47.Direction { return l.Code.Direction() }

// TranslationStats is a project's per-locale status summary.
type TranslationStats struct {
	// Messages counts the project's active messages.
	Messages int
	// Locales are the project's locales by code, the source included.
	Locales []LocaleStats
}

// TranslationStats summarizes every locale of a project in one query:
// active messages, translated, missing and outdated, and translations
// per review state — Studio's list badges and the CLI's status. It is
// computed from Localization's projection on each call rather than kept
// up to date on every write: a source revision alone changes the
// outdated count of every locale, and an aggregate over the project's
// indexed rows is correct by construction.
func (s *Service) TranslationStats(ctx context.Context, project uuid.UUID) (TranslationStats, error) {
	if err := authz.RequireIn(ctx, authz.TranslationsRead, project); err != nil {
		return TranslationStats{}, err
	}
	p, err := s.catalog.Project(ctx, project)
	if err != nil {
		return TranslationStats{}, err
	}
	var stored StoredStats
	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		stored, err = st.TranslationStats(ctx, project)
		return err
	})
	if err != nil {
		return TranslationStats{}, err
	}
	out := TranslationStats{Messages: stored.Messages}
	sourceSeen := false
	for _, l := range stored.Locales {
		isSource := l.Code == p.SourceLocale
		sourceSeen = sourceSeen || isSource
		out.Locales = append(out.Locales, localeStats(l.Code, isSource, stored.Messages, l))
	}
	// A project seen before its created event was handled still has
	// its source locale.
	if !sourceSeen {
		out.Locales = append(out.Locales, localeStats(p.SourceLocale, true, stored.Messages, LocaleCounts{}))
	}
	// By code, in the same order as the locale list.
	slices.SortFunc(out.Locales, func(a, b LocaleStats) int { return strings.Compare(a.Code.String(), b.Code.String()) })
	return out, nil
}

// MaxLeadTimeSamples bounds one lead-time sample. It is a measurement,
// not an audit: a few hundred recent messages give a stable median, and
// the cap is what keeps a dashboard from becoming a table scan.
const MaxLeadTimeSamples = 2000

// LeadTimeQuery bounds a lead-time sample.
type LeadTimeQuery struct {
	// Since is the oldest source change to consider.
	Since time.Time
	// States are the review states that count as finished work — the
	// ones the environment the number is measured to actually ships. A
	// draft that will never be published is not a translation that
	// landed, and counting it would make the lead time shorter than any
	// user's experience of it.
	States []string
	// Locales narrows the sample; empty is every locale.
	Locales []string
	// Limit bounds the rows, newest source change first; 0 and anything
	// over MaxLeadTimeSamples mean MaxLeadTimeSamples.
	Limit int
}

// LeadTimeSample is one message's leg in one locale: when its source
// last changed, and when a translation in a shipping state first caught
// up with that change.
type LeadTimeSample struct {
	Locale          string
	SourceChangedAt time.Time
	TranslatedAt    time.Time
}

// LeadTimeSamples are recent source changes with the moment a
// translation caught up with them, per locale (RFC 0005 §8, the lead
// time's Localization half).
//
// It answers only the part Localization knows. When that translation
// went live is Release's fact, and the two are joined by the caller
// rather than by a query across contexts. Needs `translations.read`,
// like every other read of a project's translations.
//
// Only messages whose translation is current qualify: an outdated
// translation has not caught up with its source, and a lead time for
// work that is not finished would be a guess wearing a measurement's
// clothes.
func (s *Service) LeadTimeSamples(ctx context.Context, project uuid.UUID, q LeadTimeQuery) ([]LeadTimeSample, error) {
	if err := authz.RequireIn(ctx, authz.TranslationsRead, project); err != nil {
		return nil, err
	}
	if _, err := s.catalog.Project(ctx, project); err != nil {
		return nil, err
	}
	if q.Limit <= 0 || q.Limit > MaxLeadTimeSamples {
		q.Limit = MaxLeadTimeSamples
	}
	if len(q.States) == 0 {
		// The states a default `production` environment ships. A caller
		// that names none is asking about work that went out, not work
		// that was started.
		q.States = []string{string(domain.StateApproved)}
	}
	var rows []LeadTimeSample
	err := s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		var err error
		rows, err = st.LeadTimeSamples(ctx, project, q)
		return err
	})
	return rows, err
}

func localeStats(code bcp47.Tag, isSource bool, messages int, c LocaleCounts) LocaleStats {
	cov := domain.LocaleCoverage(messages, c.States, c.Outdated)
	if isSource {
		cov = domain.SourceCoverage(messages)
	}
	return LocaleStats{Code: code, IsSource: isSource, Coverage: cov}
}
