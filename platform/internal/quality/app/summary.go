package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The quality summary (RFC 0005 §8): seven numbers per project and
// locale, and no more, because a dashboard nobody reads is worse than a
// check that fails.
//
// Everything here is computed from the owning context's own tables on
// read and cached for a minute. M4 adds no time-series store —
// `chronos` and the Insights context stay for M5 (§14 decision 7) — so
// the one trend is the findings-by-day rollup Quality keeps itself.
//
// The document is pivoted the way it is read: one ProjectHealth and one
// LocaleHealth per locale, each carrying the same seven fields, rather
// than seven lists a reader would have to join by locale code. A locale
// row also says which layers are *available* for it, so an unsupported
// layer never reads as a green one (intent §41).
//
// **A number that could not be computed is absent, never zero.** Every
// member of a health row is a pointer, nil where the number was not
// measured, and Unmeasured carries the name and the reason. Nothing
// here ever answers a question it could not ask: a port the deployment
// never wired, a permission the caller does not hold, an environment
// that has published nothing, a project nothing has ever checked — each
// of those leaves the number absent. A dashboard that reports 0 for
// "not measured" is a lie, and the two become indistinguishable the
// moment they share a zero.

// SummarySchema names the document, as every other Glossa payload
// names itself.
const SummarySchema = "glossa.quality-summary/v1"

// The names Unmeasured uses for the seven numbers and the trend.
const (
	NumberCoverage        = "coverage"
	NumberFindings        = "findings"
	NumberAI              = "ai"
	NumberReviewQueue     = "queue"
	NumberContextCoverage = "context"
	NumberLeadTime        = "lead_time"
	NumberCheckHealth     = "checks"
	NumberFindingsByDay   = "findings_by_day"
)

// Reasons a number is not measured, as the summary states them.
const (
	reasonNotWired     = "this deployment has no source for that number"
	reasonNotPermitted = "the caller may not read what it is computed from"
	reasonFailed       = "the source could not be read"
	reasonNoRun        = "nothing has been checked in this project yet"
	reasonNoPublish    = "the environment %q has published nothing since %s"
)

// Why a layer is not available for a locale (intent §41).
const (
	// UnavailableUnsupportedLocale: the policy switches the layer off
	// for this locale while leaving it on for others.
	UnavailableUnsupportedLocale = "unsupported_locale"
	// UnavailableNotConfigured: the policy switches the layer off
	// everywhere in the project.
	UnavailableNotConfigured = "not_configured"
	// UnavailableNoEvidence: the policy asks for the layer, and the
	// newest run did not compute it — it had nothing to run it on (no
	// capture, no termbase, no provider), or the run was narrowed.
	UnavailableNoEvidence = "no_evidence"
)

// SummaryTTL is how long a computed summary is served again (RFC 0005
// §8: cached 60 s). It bounds how stale a dashboard can be and how much
// work a page that polls can cost: seven cross-context reads per
// project per minute, whoever asks and however often.
const SummaryTTL = time.Minute

// summaryWindow is the default window for the numbers that need one —
// the AI decisions, the lead-time samples, the check health and the
// findings trend. It matches `ai-metrics`' own default, so a caller who
// reads both sees one month in both.
const summaryWindow = 30 * 24 * time.Hour

// maxLeadTimeSamples bounds the lead-time sample. It is a measurement,
// not an audit: a few hundred recent messages give a stable median, and
// no project can turn a dashboard into a table scan.
const maxLeadTimeSamples = 1000

// DefaultSummaryEnvironment is where "published" means, unless the
// caller says otherwise.
const DefaultSummaryEnvironment = "production"

// Unmeasured names a number the summary could not compute, and why.
type Unmeasured struct {
	Number string
	Reason string
}

// SummaryQuery asks for one project's summary.
type SummaryQuery struct {
	// Locale narrows the locale rows to one. Empty is every locale.
	Locale string
	// Environment is the environment the lead time is measured to;
	// empty means DefaultSummaryEnvironment.
	Environment string
	// Since is the start of the window; zero means 30 days ago.
	Since time.Time
}

// Summary is the seven numbers, as of ComputedAt.
type Summary struct {
	Schema      string
	Project     uuid.UUID
	Locale      string
	Environment string
	Since       time.Time
	ComputedAt  time.Time
	// ExpiresAt is when this summary stops being served from the cache.
	ExpiresAt time.Time
	// Cached says it was served from a previous computation.
	Cached bool

	// Health is the project as a whole, and Locales is one row per
	// locale, in code order.
	Health  ProjectHealth
	Locales []LocaleHealth

	// Trend is the findings-by-day rollup; nil where it was not
	// measured.
	Trend *TrendSummary

	// Unmeasured is every number that is nil above, with its reason, in
	// the seven's own order.
	Unmeasured []Unmeasured
}

// ProjectHealth is the seven numbers for the project as a whole.
type ProjectHealth struct {
	// Coverage is number 1 (Localization).
	Coverage *Coverage
	// Findings is number 2, from Quality's own tables, and Run is the
	// run they came from.
	Findings *domain.Counts
	Run      *domain.CheckRun
	// ByLayer breaks the findings down, in report order.
	ByLayer []domain.LayerCount
	// AI is number 3 (Intelligence).
	AI *Acceptance
	// Queue is number 4 (Intelligence).
	Queue *Queue
	// Context is number 5 (Context).
	Context *ContextCoverage
	// LeadTime is number 6 (Localization revisions × Release).
	LeadTime *domain.Spread
	// Checks is number 7 (Integration). It has no locale: a pull request
	// is about a commit, not a language.
	Checks *CheckHealth
}

// LocaleHealth is the same numbers for one locale, plus which layers
// are available for it.
type LocaleHealth struct {
	Code string
	// Direction is the locale's text direction, so a row can be laid out
	// without the client parsing the tag.
	Direction string
	IsSource  bool

	Coverage *Coverage
	Findings *domain.Counts
	AI       *Acceptance
	Queue    *Queue
	LeadTime *domain.Spread
	// Layers says which layers are available for this locale and what
	// they found. Availability is asked before grading, so a layer that
	// cannot run here is never drawn as one that ran and passed.
	Layers []LayerHealth
}

// Coverage is number 1: how much of the catalog is translated.
type Coverage struct {
	Messages   int
	Translated int
	Outdated   int
	Missing    int
}

// Acceptance is number 3: what people did with the machine's
// suggestions. Decisions is accepted plus rejected — the denominator of
// the rate, so a rate with no decisions behind it is impossible to
// mistake for a bad one.
type Acceptance struct {
	Decisions        int
	Accepted         int
	Edited           int
	Rejected         int
	AcceptanceRate   float64
	MeanEditDistance float64
}

// Queue is number 4: the review queue. Depth is always measured; Age is
// the waiting time of what is in it, and an empty queue has none.
type Queue struct {
	Depth int
	Age   domain.Spread
}

// ContextCoverage is number 5: how much of the catalog the product's
// own code and screenshots account for.
type ContextCoverage struct {
	ActiveMessages int
	WithUsage      int
	WithRegion     int
}

// LayerHealth is one layer for one locale.
type LayerHealth struct {
	Layer domain.Layer
	// Available says the layer can produce findings here at all.
	Available bool
	// Unavailable is why it cannot, when it cannot.
	Unavailable string
	// Checked says the newest run actually computed it.
	Checked bool
	// Findings is what it found; nil where it was not checked, because
	// a layer nobody ran found nothing in the sense that says nothing.
	Findings *domain.Counts
}

// TrendSummary is the findings-by-day rollup: the one trend M4 keeps.
type TrendSummary struct {
	From, To time.Time
	Days     []domain.DailyFindings
}

// WithSummarySources gives the summary the other contexts' ports. A
// port left nil leaves its number not measured, which the summary says
// rather than reporting a zero nobody computed.
func WithSummarySources(s SummarySources) Option {
	return func(svc *Service) { svc.sources = s }
}

// SetSummarySources is WithSummarySources after construction, for the
// composition root: Quality is built before Context, Intelligence and
// Integration, because a capture upload hands it the visual findings it
// carries. The summary reads those three the other way round, so one of
// the two directions has to be wired afterwards, and it is the one that
// only reads.
func (s *Service) SetSummarySources(src SummarySources) { s.sources = src }

// QualitySummary is the project's seven numbers, cached for SummaryTTL
// (RFC 0005 §8).
//
// It needs `catalog.read`, like every other read of Quality. Each
// source then checks its own permission, and a caller who lacks one
// gets that number back as not measured rather than a 403 for the whole
// page: a translator with no `intelligence.read` should still see
// coverage and findings, and should be told plainly which numbers were
// not theirs to see.
func (s *Service) QualitySummary(ctx context.Context, project uuid.UUID, q SummaryQuery) (Summary, error) {
	if err := s.read(ctx, project); err != nil {
		return Summary{}, err
	}
	// The key carries the window the *caller* asked for, zero included,
	// rather than the one the default fills in: a default window that
	// moved with the clock would make every request its own entry and
	// the cache would never hit. The cost is that an entry's window lags
	// by at most its own minute of life, which is what a summary cached
	// for a minute already promises. It is keyed by what the caller may
	// read as well, because two callers may hold different permissions
	// and a number one of them was allowed to compute must never be
	// handed to the other out of a cache.
	key := summaryKey{
		project: project, locale: q.Locale,
		environment: defaultEnvironment(q.Environment), since: q.Since.UnixNano(),
		permissions: summaryPermissions(ctx),
	}
	q = q.normalize(s.now())
	if cached, ok := s.summaries.get(key, s.now()); ok {
		cached.Cached = true
		return cached, nil
	}
	out, err := s.computeSummary(ctx, project, q)
	if err != nil {
		return Summary{}, err
	}
	s.summaries.put(key, out, s.now())
	return out, nil
}

// defaultEnvironment names where "published" means.
func defaultEnvironment(name string) string {
	if name == "" {
		return DefaultSummaryEnvironment
	}
	return name
}

// normalize fills the query's defaults.
func (q SummaryQuery) normalize(now time.Time) SummaryQuery {
	q.Environment = defaultEnvironment(q.Environment)
	if q.Since.IsZero() {
		q.Since = now.Add(-summaryWindow)
	}
	q.Since = q.Since.UTC()
	return q
}

// locales is the locale filter as the ports take it.
func (q SummaryQuery) locales() []string {
	if q.Locale == "" {
		return nil
	}
	return []string{q.Locale}
}

// builder collects the numbers while they are read, keyed by locale,
// and assembles the rows at the end. Each number is read on its own, and
// one that cannot be read leaves its field nil with a reason: the point
// of a health page is to show the health it can see, and a single
// unreachable context must not black the whole page out.
type builder struct {
	out      *Summary
	coverage map[string]Coverage
	ai       map[string]Acceptance
	queue    map[string]Queue
	lead     map[string]domain.Spread
	findings map[string]domain.Counts
	layers   map[string]map[domain.Layer]domain.Counts
	// order is every locale any number mentioned, so a locale with only
	// a queue still gets a row.
	order map[string]*localeFacts
}

// localeFacts are the properties of the locale itself, as the first
// number that mentioned it knew them.
type localeFacts struct {
	direction string
	isSource  bool
}

func newBuilder(out *Summary) *builder {
	return &builder{
		out: out, coverage: map[string]Coverage{}, ai: map[string]Acceptance{}, queue: map[string]Queue{},
		lead: map[string]domain.Spread{}, findings: map[string]domain.Counts{},
		layers: map[string]map[domain.Layer]domain.Counts{}, order: map[string]*localeFacts{},
	}
}

// see records that a locale exists.
func (b *builder) see(locale string) *localeFacts {
	if locale == "" {
		return &localeFacts{}
	}
	f, ok := b.order[locale]
	if !ok {
		f = &localeFacts{}
		b.order[locale] = f
	}
	return f
}

// computeSummary reads the seven numbers and assembles the document.
func (s *Service) computeSummary(ctx context.Context, project uuid.UUID, q SummaryQuery) (out Summary, err error) {
	ctx, end := s.span(ctx, "quality.summary",
		attribute.String("glossa.project_id", project.String()),
		attribute.String("glossa.locale", q.Locale))
	defer end(&err)

	now := s.now()
	out = Summary{
		Schema: SummarySchema, Project: project, Locale: q.Locale, Environment: q.Environment,
		Since: q.Since, ComputedAt: now, ExpiresAt: now.Add(SummaryTTL),
	}
	b := newBuilder(&out)

	if err := s.summaryCoverage(ctx, b, project); err != nil {
		return Summary{}, err
	}
	policy, err := s.summaryFindings(ctx, b, project, q, now)
	if err != nil {
		return Summary{}, err
	}
	s.summaryAI(ctx, b, project, q, now)
	s.summaryContext(ctx, b, project)
	s.summaryLeadTime(ctx, b, project, q)
	s.summaryChecks(ctx, b, project, q)
	b.assemble(q, policy)
	return out, nil
}

// ── number 1: coverage (Localization) ───────────────────────────────

func (s *Service) summaryCoverage(ctx context.Context, b *builder, project uuid.UUID) error {
	if s.sources.Coverage == nil {
		b.out.unmeasured(NumberCoverage, reasonNotWired)
		return nil
	}
	rows, err := s.sources.Coverage.TranslationCoverage(ctx, project)
	if err != nil {
		s.noteSourceFailure(b.out, NumberCoverage, err)
		return nil
	}
	total := Coverage{}
	for _, l := range rows {
		f := b.see(l.Locale)
		f.direction, f.isSource = l.Direction, l.IsSource
		b.coverage[l.Locale] = Coverage{
			Messages: l.Messages, Translated: l.Translated, Outdated: l.Outdated, Missing: l.Missing,
		}
		// The project's coverage is the target locales' work, not the
		// source's: the source locale is complete by definition, and
		// counting it would flatter every project by one locale's worth.
		if l.IsSource {
			continue
		}
		total.Messages += l.Messages
		total.Translated += l.Translated
		total.Outdated += l.Outdated
		total.Missing += l.Missing
	}
	b.out.Health.Coverage = &total
	return nil
}

// ── number 2: findings (Quality's own tables) ───────────────────────

// summaryFindings reads the newest run, its counts as they stand today
// and the per-locale, per-layer breakdown, and hands back the project's
// check policy — which is what decides whether a layer is available for
// a locale at all.
func (s *Service) summaryFindings(
	ctx context.Context, b *builder, project uuid.UUID, q SummaryQuery, now time.Time,
) (checkpolicy.Policy, error) {
	stored, err := s.storedPolicy(ctx, project)
	if err != nil {
		// Without the policy nothing can be said about availability, and
		// saying nothing is the honest answer: every layer reads as
		// unavailable for want of a policy rather than as green.
		s.logger.Warn("quality summary: the check policy could not be read",
			slog.String("project", project.String()), slog.Any("error", err))
	}
	policy := stored.Policy.Current()

	err = s.tx.InTenant(ctx, func(ctx context.Context, st Store) error {
		run, err := st.LatestCheckRun(ctx, project, RunFilter{})
		if errors.Is(err, ErrCheckRunNotFound) {
			// Never checked is not "clean". A dashboard that showed a
			// green zero here would be reporting the absence of a check
			// as the absence of problems.
			b.out.unmeasured(NumberFindings, reasonNoRun)
			b.out.unmeasured(NumberFindingsByDay, reasonNoRun)
			return nil
		}
		if err != nil {
			return err
		}
		counts, err := st.CountFindings(ctx, run, now)
		if err != nil {
			return err
		}
		rows, err := st.CountFindingsByLayer(ctx, run, now)
		if err != nil {
			return err
		}
		b.out.Health.Run = &run
		b.out.Health.Findings = &counts
		byLayer := map[domain.Layer]*domain.Counts{}
		for _, r := range rows {
			b.see(r.Locale)
			c := b.findings[r.Locale]
			c.Errors += r.Counts.Errors
			c.Warnings += r.Counts.Warnings
			c.Waived += r.Counts.Waived
			b.findings[r.Locale] = c
			if b.layers[r.Locale] == nil {
				b.layers[r.Locale] = map[domain.Layer]domain.Counts{}
			}
			b.layers[r.Locale][r.Layer] = r.Counts
			if byLayer[r.Layer] == nil {
				byLayer[r.Layer] = &domain.Counts{}
			}
			byLayer[r.Layer].Errors += r.Counts.Errors
			byLayer[r.Layer].Warnings += r.Counts.Warnings
			byLayer[r.Layer].Waived += r.Counts.Waived
		}
		for layer, c := range byLayer {
			b.out.Health.ByLayer = append(b.out.Health.ByLayer, domain.LayerCount{Layer: layer, Counts: *c})
		}
		b.out.Health.ByLayer = domain.SortLayerCounts(b.out.Health.ByLayer)

		days, err := st.FindingsByDay(ctx, project, q.Since, now)
		if err != nil {
			return err
		}
		b.out.Trend = &TrendSummary{
			From: q.Since.UTC().Truncate(24 * time.Hour), To: now.UTC().Truncate(24 * time.Hour), Days: days,
		}
		return nil
	})
	return policy, err
}

// ── numbers 3 and 4: the machine's suggestions (Intelligence) ───────

func (s *Service) summaryAI(ctx context.Context, b *builder, project uuid.UUID, q SummaryQuery, now time.Time) {
	if s.sources.Suggestions == nil {
		b.out.unmeasured(NumberAI, reasonNotWired)
		b.out.unmeasured(NumberReviewQueue, reasonNotWired)
		return
	}
	if rows, err := s.sources.Suggestions.Acceptance(ctx, project, q.Since); err != nil {
		s.noteSourceFailure(b.out, NumberAI, err)
	} else {
		total := Acceptance{}
		for _, r := range rows {
			b.see(r.Locale)
			b.ai[r.Locale] = acceptance(r)
			total.Accepted += r.Accepted
			total.Edited += r.Edited
			total.Rejected += r.Rejected
			total.MeanEditDistance += r.MeanEditDistance * float64(r.Accepted)
		}
		total.Decisions = total.Accepted + total.Rejected
		if total.Decisions > 0 {
			total.AcceptanceRate = float64(total.Accepted) / float64(total.Decisions)
		}
		if total.Accepted > 0 {
			// Pooled, not averaged over locales: a locale with four
			// accepted suggestions must not weigh as much as one with
			// four hundred.
			total.MeanEditDistance /= float64(total.Accepted)
		}
		b.out.Health.AI = &total
	}
	if rows, err := s.sources.Suggestions.QueueAges(ctx, project, q.locales(), now); err != nil {
		s.noteSourceFailure(b.out, NumberReviewQueue, err)
	} else {
		project := Queue{}
		for _, r := range rows {
			b.see(r.Locale)
			b.queue[r.Locale] = Queue{Depth: r.Waiting, Age: r.Age}
			project.Depth += r.Waiting
		}
		// The whole queue's age is the pooled percentiles of the locale
		// queues, which cannot be recovered from their percentiles. With
		// one locale there is nothing to pool, and with more the age
		// stays on the rows that have it.
		if len(rows) == 1 {
			project.Age = rows[0].Age
		}
		b.out.Health.Queue = &project
	}
}

func acceptance(r LocaleAcceptance) Acceptance {
	return Acceptance{
		Decisions: r.Accepted + r.Rejected, Accepted: r.Accepted, Edited: r.Edited, Rejected: r.Rejected,
		AcceptanceRate: r.AcceptanceRate, MeanEditDistance: r.MeanEditDistance,
	}
}

// ── number 5: context coverage (Context) ────────────────────────────

// summaryContext fills the project's number only. A usage is a place in
// the product's code and a region a box on a screenshot; neither
// belongs to a locale, so a locale row carries no context coverage
// rather than a copy of the project's.
func (s *Service) summaryContext(ctx context.Context, b *builder, project uuid.UUID) {
	if s.sources.Usage == nil {
		b.out.unmeasured(NumberContextCoverage, reasonNotWired)
		return
	}
	usage, err := s.sources.Usage.UsageCoverage(ctx, project)
	if err != nil {
		s.noteSourceFailure(b.out, NumberContextCoverage, err)
		return
	}
	region, err := s.sources.Usage.RegionCoverage(ctx, project)
	if err != nil {
		s.noteSourceFailure(b.out, NumberContextCoverage, err)
		return
	}
	b.out.Health.Context = &ContextCoverage{
		ActiveMessages: usage.Of, WithUsage: usage.With, WithRegion: region.With,
	}
}

// ── number 6: lead time (Localization revisions × Release) ──────────

// summaryLeadTime joins Localization's "the translation caught up at T"
// with Release's "the environment went live at U": the lead time is
// from the source change to the first deployment at or after T.
//
// A sample whose translation has not been published yet is dropped, not
// counted as zero and not counted as still running: the measurement is
// of work that finished, and a locale with nothing finished has no lead
// time rather than a lead time of none.
func (s *Service) summaryLeadTime(ctx context.Context, b *builder, project uuid.UUID, q SummaryQuery) {
	if s.sources.Coverage == nil || s.sources.Deployments == nil {
		b.out.unmeasured(NumberLeadTime, reasonNotWired)
		return
	}
	timeline, err := s.sources.Deployments.Timeline(ctx, project, q.Environment, q.Since)
	if err != nil {
		s.noteSourceFailure(b.out, NumberLeadTime, err)
		return
	}
	if len(timeline.PublishedAt) == 0 {
		b.out.unmeasured(NumberLeadTime, fmt.Sprintf(reasonNoPublish, q.Environment, q.Since.Format(time.RFC3339)))
		return
	}
	samples, err := s.sources.Coverage.LeadTimeSamples(ctx, project, LeadTimeQuery{
		Since: q.Since, States: timeline.States, Locales: q.locales(), Limit: maxLeadTimeSamples,
	})
	if err != nil {
		s.noteSourceFailure(b.out, NumberLeadTime, err)
		return
	}
	byLocale := map[string][]time.Duration{}
	var all []time.Duration
	for _, sm := range samples {
		if sm.TranslatedAt.Before(sm.SourceChangedAt) {
			continue // a clock nobody can explain is not a measurement
		}
		at, ok := firstAtOrAfter(timeline.PublishedAt, sm.TranslatedAt)
		if !ok {
			continue // translated, not yet shipped
		}
		d := at.Sub(sm.SourceChangedAt)
		byLocale[sm.Locale] = append(byLocale[sm.Locale], d)
		all = append(all, d)
	}
	for locale, ds := range byLocale {
		b.see(locale)
		b.lead[locale] = domain.NewSpread(ds)
	}
	overall := domain.NewSpread(all)
	b.out.Health.LeadTime = &overall
}

// firstAtOrAfter is the first publish at or after t; published is
// oldest first.
func firstAtOrAfter(published []time.Time, t time.Time) (time.Time, bool) {
	i := sort.Search(len(published), func(i int) bool { return !published[i].Before(t) })
	if i == len(published) {
		return time.Time{}, false
	}
	return published[i], true
}

// ── number 7: check health (Integration) ────────────────────────────

func (s *Service) summaryChecks(ctx context.Context, b *builder, project uuid.UUID, q SummaryQuery) {
	if s.sources.Checks == nil {
		b.out.unmeasured(NumberCheckHealth, reasonNotWired)
		return
	}
	h, err := s.sources.Checks.CheckHealth(ctx, project, q.Since)
	if err != nil {
		s.noteSourceFailure(b.out, NumberCheckHealth, err)
		return
	}
	b.out.Health.Checks = &h
}

// ── assembling the locale rows ──────────────────────────────────────

// assemble builds one row per locale any number mentioned, in code
// order, and works out which layers are available for each.
func (b *builder) assemble(q SummaryQuery, policy checkpolicy.Policy) {
	codes := make([]string, 0, len(b.order))
	for code := range b.order {
		if q.Locale != "" && code != q.Locale {
			continue
		}
		codes = append(codes, code)
	}
	// A caller who asked about a locale no number mentioned still gets
	// its row, so "this locale has nothing" is an answer rather than a
	// missing row somebody has to interpret.
	if q.Locale != "" && len(codes) == 0 {
		codes = append(codes, q.Locale)
		b.see(q.Locale)
	}
	sort.Strings(codes)

	// Which locales the policy switches a layer off for tells an
	// unsupported locale from a layer nobody configured at all.
	anywhere := map[domain.Layer]bool{}
	for _, layer := range domain.Layers {
		for _, code := range codes {
			anywhere[layer] = anywhere[layer] || computes(policy, layer, code)
		}
	}
	ran := map[domain.Layer]bool{}
	if b.out.Health.Run != nil {
		for _, l := range b.out.Health.Run.Layers {
			ran[l] = true
		}
	}

	for _, code := range codes {
		facts := b.order[code]
		row := LocaleHealth{Code: code, Direction: facts.direction, IsSource: facts.isSource}
		if c, ok := b.coverage[code]; ok {
			row.Coverage = &c
		}
		if c, ok := b.findings[code]; ok {
			row.Findings = &c
		}
		if a, ok := b.ai[code]; ok {
			row.AI = &a
		}
		if qu, ok := b.queue[code]; ok {
			row.Queue = &qu
		}
		if l, ok := b.lead[code]; ok {
			row.LeadTime = &l
		}
		row.Layers = layerHealth(policy, code, anywhere, ran, b.layers[code], b.out.Health.Run != nil)
		b.out.Locales = append(b.out.Locales, row)
	}
}

// computes reports whether the policy lets a layer run for a locale.
func computes(policy checkpolicy.Policy, layer domain.Layer, locale string) bool {
	return policy.Decide(checkpolicy.Target{
		Layer: string(layer), Locale: locale, Severity: checkpolicy.Warning,
	}).Computed()
}

// layerHealth works out, for one locale, which layers can produce
// findings at all and which the newest run actually computed (intent
// §41: a locale shows which layers are available for it, so an
// unsupported layer never reads as a green one).
//
// Availability is asked of the policy, which is the only thing that
// answers it without guessing: a layer switched off for this locale
// while on elsewhere is unsupported *here*; one switched off everywhere
// is not configured at all; one the policy asks for that the newest run
// did not compute had no evidence to compute it from — no capture, no
// termbase, no provider — or was narrowed by the caller that ran it.
// Only a layer that is available and was checked carries counts, so a
// layer nobody ran can never be drawn as one that ran and passed.
func layerHealth(
	policy checkpolicy.Policy, locale string,
	anywhere, ran map[domain.Layer]bool, found map[domain.Layer]domain.Counts, checked bool,
) []LayerHealth {
	out := make([]LayerHealth, 0, len(domain.Layers))
	for _, layer := range domain.Layers {
		h := LayerHealth{Layer: layer}
		switch {
		case !computes(policy, layer, locale):
			if anywhere[layer] {
				h.Unavailable = UnavailableUnsupportedLocale
			} else {
				h.Unavailable = UnavailableNotConfigured
			}
		case checked && !ran[layer]:
			h.Unavailable = UnavailableNoEvidence
		default:
			h.Available = true
			h.Checked = checked && ran[layer]
			if h.Checked {
				c := found[layer]
				h.Findings = &c
			}
		}
		out = append(out, h)
	}
	return out
}

// ── absent numbers ──────────────────────────────────────────────────

// noteSourceFailure turns a source's error into an absent number. A
// permission the caller does not hold is said as such; anything else is
// logged with the error and reported as a read that did not work, so an
// outage looks like an outage and never like a clean project.
func (s *Service) noteSourceFailure(out *Summary, number string, err error) {
	var denied *authz.DeniedError
	if errors.As(err, &denied) {
		out.unmeasured(number, reasonNotPermitted)
		return
	}
	s.logger.Warn("quality summary: a source could not be read",
		slog.String("number", number), slog.String("project", out.Project.String()), slog.Any("error", err))
	out.unmeasured(number, reasonFailed)
}

// unmeasured records that a number is absent and why. The order is the
// order the numbers are read in, which is the seven's own.
func (s *Summary) unmeasured(number, reason string) {
	s.Unmeasured = append(s.Unmeasured, Unmeasured{Number: number, Reason: reason})
}
