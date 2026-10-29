// Package summary adapts the four other contexts the quality summary
// reads to Quality's own ports: Localization's coverage and lead-time
// samples, Intelligence's acceptance and review queue, Context's usage
// and capture coverage, Release's publish timeline and Integration's
// pull-request check health (RFC 0005 §8).
//
// Every call here is an ordinary authorized use case of the service
// that owns the number (RFC 0002 §4, contexts reach each other through
// application ports). Nothing reads another context's tables, and every
// one of those services keeps checking the caller's permissions — so
// the summary shows exactly what its caller could have read operation
// by operation through the API, and a caller who may not read one of
// them gets that number back as not measured rather than a page-wide
// 403.
//
// Nothing here decides anything. It translates a question Quality asks
// into the question the owning context already answers.
package summary

import (
	"context"
	"time"

	"github.com/google/uuid"

	contextapp "go.klarlabs.de/glossa/platform/internal/context/app"
	integrationapp "go.klarlabs.de/glossa/platform/internal/integration/app"
	intelligenceapp "go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	releaseapp "go.klarlabs.de/glossa/platform/internal/release/app"
	releasedomain "go.klarlabs.de/glossa/platform/internal/release/domain"
)

// defaultBranch is the view every coverage number is of. The dashboard
// is about the project as it stands, not about somebody's feature
// branch; Context's own gauges measure the same view for the same
// reason (RFC 0004 §11).
const defaultBranch = ""

// ── Localization ────────────────────────────────────────────────────

// Translations implements app.Translations on Localization's service.
type Translations struct{ svc *localizationapp.Service }

// NewTranslations returns the port.
func NewTranslations(svc *localizationapp.Service) *Translations { return &Translations{svc: svc} }

var _ app.Translations = (*Translations)(nil)

// TranslationCoverage implements app.Translations with `translation-stats`,
// the query that already answers this question for Studio and the CLI.
func (t *Translations) TranslationCoverage(ctx context.Context, project uuid.UUID) ([]app.LocaleCoverage, error) {
	stats, err := t.svc.TranslationStats(ctx, project)
	if err != nil {
		return nil, err
	}
	out := make([]app.LocaleCoverage, 0, len(stats.Locales))
	for _, l := range stats.Locales {
		out = append(out, app.LocaleCoverage{
			Locale: l.Code.String(), Direction: string(l.Direction()), IsSource: l.IsSource, Messages: l.Messages,
			Translated: l.Translated, Missing: l.Missing, Outdated: l.Outdated,
		})
	}
	return out, nil
}

// LeadTimeSamples implements app.Translations.
func (t *Translations) LeadTimeSamples(
	ctx context.Context, project uuid.UUID, q app.LeadTimeQuery,
) ([]app.LeadTimeSample, error) {
	rows, err := t.svc.LeadTimeSamples(ctx, project, localizationapp.LeadTimeQuery{
		Since: q.Since, States: q.States, Locales: q.Locales, Limit: q.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]app.LeadTimeSample, 0, len(rows))
	for _, r := range rows {
		out = append(out, app.LeadTimeSample{
			Locale: r.Locale, SourceChangedAt: r.SourceChangedAt, TranslatedAt: r.TranslatedAt,
		})
	}
	return out, nil
}

// ── Intelligence ────────────────────────────────────────────────────

// Suggestions implements app.Suggestions on Intelligence's service.
type Suggestions struct{ svc *intelligenceapp.Service }

// NewSuggestions returns the port.
func NewSuggestions(svc *intelligenceapp.Service) *Suggestions { return &Suggestions{svc: svc} }

var _ app.Suggestions = (*Suggestions)(nil)

// Acceptance implements app.Suggestions with `ai-metrics`.
func (s *Suggestions) Acceptance(
	ctx context.Context, project uuid.UUID, since time.Time,
) ([]app.LocaleAcceptance, error) {
	rows, _, err := s.svc.AcceptanceMetrics(ctx, project, since)
	if err != nil {
		return nil, err
	}
	out := make([]app.LocaleAcceptance, 0, len(rows))
	for _, r := range rows {
		out = append(out, app.LocaleAcceptance{
			Locale: r.Locale, Accepted: r.Accepted, Edited: r.Edited, Rejected: r.Rejected,
			AcceptanceRate: r.AcceptanceRate, MeanEditDistance: r.MeanEditDistance,
		})
	}
	return out, nil
}

// QueueAges implements app.Suggestions.
func (s *Suggestions) QueueAges(
	ctx context.Context, project uuid.UUID, locales []string, now time.Time,
) ([]app.LocaleQueue, error) {
	rows, err := s.svc.ReviewQueueAges(ctx, project, locales, now)
	if err != nil {
		return nil, err
	}
	out := make([]app.LocaleQueue, 0, len(rows))
	for _, r := range rows {
		out = append(out, app.LocaleQueue{
			Locale: r.Locale, Waiting: r.Waiting,
			Age: domain.Spread{N: r.Waiting, P50: r.P50, P90: r.P90},
		})
	}
	return out, nil
}

// ── Context ─────────────────────────────────────────────────────────

// Usages implements app.Usage on Context's service.
type Usages struct{ svc *contextapp.Service }

// NewUsages returns the port.
func NewUsages(svc *contextapp.Service) *Usages { return &Usages{svc: svc} }

var _ app.Usage = (*Usages)(nil)

// UsageCoverage implements app.Usage. Context reports the unused
// messages; the share with a usage is the rest of the active ones, and
// a project with none active has no share at all.
func (u *Usages) UsageCoverage(ctx context.Context, project uuid.UUID) (domain.Share, error) {
	unused, err := u.svc.UnusedMessages(ctx, project, defaultBranch)
	if err != nil {
		return domain.Share{}, err
	}
	return domain.Share{Of: unused.Active, With: unused.Active - len(unused.Messages)}, nil
}

// RegionCoverage implements app.Usage: the active messages a capture of
// a current build shows in a visible region.
func (u *Usages) RegionCoverage(ctx context.Context, project uuid.UUID) (domain.Share, error) {
	c, err := u.svc.CaptureCoverage(ctx, project, defaultBranch)
	if err != nil {
		return domain.Share{}, err
	}
	return domain.Share{Of: c.Active, With: c.Captured}, nil
}

// ── Release ─────────────────────────────────────────────────────────

// maxTimelinePages and timelinePageSize bound how far back the publish
// timeline is read. A project that deploys four hundred times in a
// window is measured against the last four hundred, which is more than
// a median needs and less than a table scan.
const (
	maxTimelinePages = 2
	timelinePageSize = 200
)

// Publishes implements app.Deployments on Release's service.
type Publishes struct{ svc *releaseapp.Service }

// NewPublishes returns the port.
func NewPublishes(svc *releaseapp.Service) *Publishes { return &Publishes{svc: svc} }

var _ app.Deployments = (*Publishes)(nil)

// Timeline implements app.Deployments: what the environment ships, and
// when it shipped.
//
// Every pointer move counts, a rollback included: content is live in an
// environment from the moment the environment points at it, and a
// rollback is a moment content became live exactly as a publish is.
func (p *Publishes) Timeline(
	ctx context.Context, project uuid.UUID, environment string, since time.Time,
) (app.Timeline, error) {
	env, err := p.svc.GetEnvironment(ctx, project, environment)
	if err != nil {
		return app.Timeline{}, err
	}
	out := app.Timeline{States: append([]string{}, env.Policy.States...)}
	var after string
	for range maxTimelinePages {
		page, next, err := p.svc.ListDeployments(ctx, project, environment,
			pagination.Page{Size: timelinePageSize, After: after})
		if err != nil {
			return app.Timeline{}, err
		}
		done := p.collect(&out, page, since)
		if done || next == nil {
			break
		}
		after = *next
	}
	// Oldest first, which is what a "first publish at or after this
	// translation" search needs.
	slicesReverse(out.PublishedAt)
	return out, nil
}

// collect appends the page's deployments newer than since, and reports
// whether the page ran past it.
func (p *Publishes) collect(out *app.Timeline, page []releasedomain.Deployment, since time.Time) bool {
	for _, d := range page {
		if d.CreatedAt.Before(since) {
			return true
		}
		out.PublishedAt = append(out.PublishedAt, d.CreatedAt.UTC())
	}
	return false
}

func slicesReverse(ts []time.Time) {
	for i, j := 0, len(ts)-1; i < j; i, j = i+1, j-1 {
		ts[i], ts[j] = ts[j], ts[i]
	}
}

// ── Integration ─────────────────────────────────────────────────────

// PullRequestChecks implements app.Checks on Integration's service.
type PullRequestChecks struct{ svc *integrationapp.Service }

// NewPullRequestChecks returns the port.
func NewPullRequestChecks(svc *integrationapp.Service) *PullRequestChecks {
	return &PullRequestChecks{svc: svc}
}

var _ app.Checks = (*PullRequestChecks)(nil)

// CheckHealth implements app.Checks.
func (c *PullRequestChecks) CheckHealth(
	ctx context.Context, project uuid.UUID, since time.Time,
) (app.CheckHealth, error) {
	h, err := c.svc.ProjectCheckHealth(ctx, project, since)
	if err != nil {
		return app.CheckHealth{}, err
	}
	return app.CheckHealth{
		Concluded: h.Concluded, Succeeded: h.Succeeded, Failed: h.Failed, Neutral: h.Neutral,
		Latency: domain.Spread{N: h.Concluded, P50: h.P50, P90: h.P90},
	}, nil
}
