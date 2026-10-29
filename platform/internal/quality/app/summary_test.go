package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The quality summary (RFC 0005 §8). What these pin is the one property
// the whole number is for: a number that could not be computed says so,
// and never reads as zero. Everything else about it — which query
// answers which half — is graded against Postgres in the integration
// tests.

// ── the sources, as a test can set them ─────────────────────────────

type fakeCoverage struct {
	locales []app.LocaleCoverage
	samples []app.LeadTimeSample
	err     error
	// lastQuery is what the lead-time sample was asked for.
	lastQuery app.LeadTimeQuery
}

func (f *fakeCoverage) TranslationCoverage(context.Context, uuid.UUID) ([]app.LocaleCoverage, error) {
	return f.locales, f.err
}

func (f *fakeCoverage) LeadTimeSamples(
	_ context.Context, _ uuid.UUID, q app.LeadTimeQuery,
) ([]app.LeadTimeSample, error) {
	f.lastQuery = q
	return f.samples, f.err
}

type fakeSuggestions struct {
	acceptance []app.LocaleAcceptance
	queue      []app.LocaleQueue
	err        error
}

func (f *fakeSuggestions) Acceptance(context.Context, uuid.UUID, time.Time) ([]app.LocaleAcceptance, error) {
	return f.acceptance, f.err
}

func (f *fakeSuggestions) QueueAges(
	context.Context, uuid.UUID, []string, time.Time,
) ([]app.LocaleQueue, error) {
	return f.queue, f.err
}

type fakeUsage struct {
	usage, region domain.Share
	err           error
}

func (f *fakeUsage) UsageCoverage(context.Context, uuid.UUID) (domain.Share, error) {
	return f.usage, f.err
}
func (f *fakeUsage) RegionCoverage(context.Context, uuid.UUID) (domain.Share, error) {
	return f.region, f.err
}

type fakeDeployments struct {
	timeline app.Timeline
	err      error
}

func (f *fakeDeployments) Timeline(
	context.Context, uuid.UUID, string, time.Time,
) (app.Timeline, error) {
	return f.timeline, f.err
}

type fakeChecks struct {
	health app.CheckHealth
	err    error
}

func (f *fakeChecks) CheckHealth(context.Context, uuid.UUID, time.Time) (app.CheckHealth, error) {
	return f.health, f.err
}

// ── helpers ─────────────────────────────────────────────────────────

func dayAt(day int) time.Time { return time.Date(2026, 3, day, 12, 0, 0, 0, time.UTC) }

// wholeSources is every port wired and answering.
func wholeSources() app.SummarySources {
	return app.SummarySources{
		Coverage: &fakeCoverage{
			locales: []app.LocaleCoverage{
				{Locale: "de", Direction: "ltr", Messages: 10, Translated: 7, Missing: 2, Outdated: 1},
				{Locale: "fr", Direction: "ltr", Messages: 10, Translated: 3, Missing: 7},
			},
			samples: []app.LeadTimeSample{
				{Locale: "de", SourceChangedAt: dayAt(1), TranslatedAt: dayAt(2)},
				{Locale: "de", SourceChangedAt: dayAt(3), TranslatedAt: dayAt(4)},
			},
		},
		Suggestions: &fakeSuggestions{
			acceptance: []app.LocaleAcceptance{{Locale: "de", Accepted: 8, Rejected: 2, AcceptanceRate: 0.8}},
			queue: []app.LocaleQueue{
				{Locale: "de", Waiting: 2, Age: domain.NewSpread([]time.Duration{time.Hour, 3 * time.Hour})},
			},
		},
		Usage:       &fakeUsage{usage: domain.Share{Of: 10, With: 9}, region: domain.Share{Of: 10, With: 4}},
		Deployments: &fakeDeployments{timeline: app.Timeline{States: []string{"approved"}, PublishedAt: []time.Time{dayAt(5)}}},
		Checks:      &fakeChecks{health: app.CheckHealth{Concluded: 10, Succeeded: 8, Failed: 2}},
	}
}

// summaryService returns a service with sources and a store that has a
// run to report.
func summaryService(sources app.SummarySources) (*app.Service, *fakeStore, uuid.UUID) {
	store := storeWith(3)
	store.trend = []domain.DailyFindings{{Day: dayAt(1), Layer: domain.LayerParity, Findings: 3}}
	project := uuid.New()
	catalog := &knownProjects{id: project, projectVersion: 1}
	svc := app.NewService(fakeTx{store: store}, catalog, app.WithSummarySources(sources))
	return svc, store, project
}

func unmeasuredReason(s app.Summary, number string) (string, bool) {
	for _, u := range s.Unmeasured {
		if u.Number == number {
			return u.Reason, true
		}
	}
	return "", false
}

// ── the tests ───────────────────────────────────────────────────────

// TestSummaryReportsTheSevenNumbers: every source wired and answering
// gives seven numbers and nothing unmeasured.
func TestSummaryReportsTheSevenNumbers(t *testing.T) {
	svc, _, project := summaryService(wholeSources())

	got, err := svc.QualitySummary(readCtx(t), project, app.SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Unmeasured) != 0 {
		t.Fatalf("unmeasured = %+v, want none", got.Unmeasured)
	}
	for name, present := range map[string]bool{
		"coverage":         got.Health.Coverage != nil,
		"findings":         got.Health.Findings != nil,
		"ai":               got.Health.AI != nil,
		"review queue":     got.Health.Queue != nil,
		"context coverage": got.Health.Context != nil,
		"lead time":        got.Health.LeadTime != nil,
		"check health":     got.Health.Checks != nil,
		"findings by day":  got.Trend != nil,
	} {
		if !present {
			t.Errorf("%s is absent", name)
		}
	}
	if got.Environment != app.DefaultSummaryEnvironment {
		t.Errorf("environment = %q, want %q", got.Environment, app.DefaultSummaryEnvironment)
	}
	if !got.ExpiresAt.After(got.ComputedAt) {
		t.Errorf("expires_at %v is not after computed_at %v", got.ExpiresAt, got.ComputedAt)
	}
	if got.Schema != app.SummarySchema {
		t.Errorf("schema = %q, want %q", got.Schema, app.SummarySchema)
	}
	if rate, measured := got.Health.Checks.PassRate(); !measured || rate != 0.8 {
		t.Errorf("pass rate = %v (measured %v), want 0.8", rate, measured)
	}
}

// TestSummarySaysWhichNumbersItCouldNotCompute: a deployment that wires
// no source for a number reports it as absent with a reason, never as a
// zero. A dashboard that shows 0 for "not measured" is a lie.
func TestSummarySaysWhichNumbersItCouldNotCompute(t *testing.T) {
	svc, _, project := summaryService(app.SummarySources{})

	got, err := svc.QualitySummary(readCtx(t), project, app.SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, number := range []string{
		app.NumberCoverage, app.NumberAI, app.NumberReviewQueue,
		app.NumberContextCoverage, app.NumberLeadTime, app.NumberCheckHealth,
	} {
		reason, ok := unmeasuredReason(got, number)
		if !ok || reason == "" {
			t.Errorf("%s is not reported as unmeasured with a reason", number)
		}
	}
	if got.Health.Coverage != nil || got.Health.AI != nil || got.Health.Queue != nil ||
		got.Health.Context != nil || got.Health.LeadTime != nil || got.Health.Checks != nil {
		t.Errorf("a number with no source came back as a value: %+v", got.Health)
	}
	// Quality's own number is still there: it needs nobody else.
	if got.Health.Findings == nil {
		t.Error("the findings are absent although Quality owns them")
	}
}

// TestSummaryTellsNeverCheckedFromClean: a project with no check run at
// all has no findings number, because "nobody has looked" and "we
// looked and it is clean" are different facts and share a zero.
func TestSummaryTellsNeverCheckedFromClean(t *testing.T) {
	svc, store, project := summaryService(wholeSources())
	store.runErr = app.ErrCheckRunNotFound

	got, err := svc.QualitySummary(readCtx(t), project, app.SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Health.Findings != nil {
		t.Errorf("findings = %+v, want absent", got.Health.Findings)
	}
	if reason, ok := unmeasuredReason(got, app.NumberFindings); !ok || reason == "" {
		t.Error("an unchecked project does not say why its findings are absent")
	}
	if reason, ok := unmeasuredReason(got, app.NumberFindingsByDay); !ok || reason == "" {
		t.Error("an unchecked project does not say why its trend is absent")
	}
}

// TestSummaryLeadTimeNeedsAPublish: an environment that has published
// nothing in the window has no lead time — the leg has no end — and the
// reason names the environment, so a person can tell "we are slow" from
// "we have not shipped".
func TestSummaryLeadTimeNeedsAPublish(t *testing.T) {
	sources := wholeSources()
	sources.Deployments = &fakeDeployments{timeline: app.Timeline{States: []string{"approved"}}}
	svc, _, project := summaryService(sources)

	got, err := svc.QualitySummary(readCtx(t), project, app.SummaryQuery{Environment: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Health.LeadTime != nil {
		t.Errorf("lead time = %+v, want absent", got.Health.LeadTime)
	}
	reason, ok := unmeasuredReason(got, app.NumberLeadTime)
	if !ok || !contains(reason, "staging") {
		t.Errorf("reason = %q, want it to name the environment", reason)
	}
}

// TestSummaryLeadTimeIsSourceChangeToPublish: the leg runs from the
// source change to the first publish at or after the translation, and a
// translation nothing has published yet is left out rather than counted
// as instant.
func TestSummaryLeadTimeIsSourceChangeToPublish(t *testing.T) {
	sources := wholeSources()
	sources.Coverage = &fakeCoverage{locales: []app.LocaleCoverage{
		{Locale: "de", Direction: "ltr", Messages: 10, Translated: 7},
		{Locale: "fr", Direction: "ltr", Messages: 10, Translated: 3},
	}, samples: []app.LeadTimeSample{
		{Locale: "de", SourceChangedAt: dayAt(1), TranslatedAt: dayAt(2)}, // published on day 5: 4 days
		{Locale: "de", SourceChangedAt: dayAt(3), TranslatedAt: dayAt(4)}, // published on day 5: 2 days
		{Locale: "fr", SourceChangedAt: dayAt(6), TranslatedAt: dayAt(7)}, // nothing published after day 7
	}}
	sources.Deployments = &fakeDeployments{
		timeline: app.Timeline{States: []string{"approved"}, PublishedAt: []time.Time{dayAt(5)}},
	}
	svc, _, project := summaryService(sources)

	got, err := svc.QualitySummary(readCtx(t), project, app.SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	de, fr := localeRow(t, got, "de"), localeRow(t, got, "fr")
	if de.LeadTime == nil || de.LeadTime.N != 2 {
		t.Fatalf("German lead time = %+v, want two samples", de.LeadTime)
	}
	if want := 3 * 24 * time.Hour; de.LeadTime.P50 != want { // the median of 2 and 4 days
		t.Errorf("p50 = %v, want %v", de.LeadTime.P50, want)
	}
	if fr.LeadTime != nil {
		t.Errorf("French has shipped nothing and still reports a lead time: %+v", fr.LeadTime)
	}
	// The sample asked Localization for the states the environment
	// actually ships, so a draft nobody will publish is not counted as
	// work that landed.
	fake, _ := sources.Coverage.(*fakeCoverage)
	if len(fake.lastQuery.States) != 1 || fake.lastQuery.States[0] != "approved" {
		t.Errorf("lead-time states = %v, want the environment's", fake.lastQuery.States)
	}
}

// TestSummaryEmptyQueueHasADepthAndNoAge: depth 0 is a measurement;
// the age of an empty queue is not a number at all.
func TestSummaryEmptyQueueHasADepthAndNoAge(t *testing.T) {
	sources := wholeSources()
	sources.Suggestions = &fakeSuggestions{}
	svc, _, project := summaryService(sources)

	got, err := svc.QualitySummary(readCtx(t), project, app.SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Health.Queue == nil {
		t.Fatal("the review queue is absent although Intelligence answered")
	}
	if got.Health.Queue.Depth != 0 {
		t.Errorf("depth = %d, want 0", got.Health.Queue.Depth)
	}
	if got.Health.Queue.Age.Measured() {
		t.Errorf("an empty queue has an age: %+v", got.Health.Queue.Age)
	}
}

// TestSummaryAPermissionTheCallerLacksIsNotAZero: a source that refuses
// the caller leaves its number absent and says so, rather than showing
// a clean zero for something nobody was allowed to look at.
func TestSummaryAPermissionTheCallerLacksIsNotAZero(t *testing.T) {
	sources := wholeSources()
	sources.Suggestions = &fakeSuggestions{err: &authz.DeniedError{Permission: authz.IntelligenceRead}}
	svc, _, project := summaryService(sources)

	got, err := svc.QualitySummary(readCtx(t), project, app.SummaryQuery{})
	if err != nil {
		t.Fatalf("a refused source failed the whole summary: %v", err)
	}
	if got.Health.AI != nil || got.Health.Queue != nil {
		t.Errorf("a refused source came back as a value")
	}
	reason, ok := unmeasuredReason(got, app.NumberAI)
	if !ok || !contains(reason, "may not") {
		t.Errorf("reason = %q, want it to say the caller may not read it", reason)
	}
}

// TestSummaryASourceThatFailsDoesNotBlackOutThePage: one unreachable
// context costs its own number and nothing else. A health page that
// goes blank when a dependency wobbles is a health page nobody trusts.
func TestSummaryASourceThatFailsDoesNotBlackOutThePage(t *testing.T) {
	sources := wholeSources()
	sources.Checks = &fakeChecks{err: errors.New("the database is on fire")}
	svc, _, project := summaryService(sources)

	got, err := svc.QualitySummary(readCtx(t), project, app.SummaryQuery{})
	if err != nil {
		t.Fatalf("one failing source failed the whole summary: %v", err)
	}
	if got.Health.Checks != nil {
		t.Error("a failing source came back as a value")
	}
	if _, ok := unmeasuredReason(got, app.NumberCheckHealth); !ok {
		t.Error("a failing source is not reported as unmeasured")
	}
	if got.Health.Coverage == nil || got.Health.Findings == nil {
		t.Error("a failing source took the other numbers with it")
	}
}

// TestSummaryNarrowsToOneLocale: the locale reaches every per-locale
// number, and check health — which has no locale, because a pull
// request is about a commit — is reported whole.
func TestSummaryNarrowsToOneLocale(t *testing.T) {
	svc, _, project := summaryService(wholeSources())

	got, err := svc.QualitySummary(readCtx(t), project, app.SummaryQuery{Locale: "de"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Locales) != 1 || got.Locales[0].Code != "de" {
		t.Fatalf("locales = %+v, want German only", got.Locales)
	}
	if got.Locales[0].Coverage == nil || got.Locales[0].AI == nil {
		t.Errorf("the German row lost its numbers: %+v", got.Locales[0])
	}
	if got.Health.Checks == nil || got.Health.Checks.Concluded != 10 {
		t.Errorf("check health = %+v, want the project's whole record", got.Health.Checks)
	}
}

// TestSummaryIsCachedForAMinute: the second read of the same question
// is the first one's answer, and it says so.
func TestSummaryIsCachedForAMinute(t *testing.T) {
	sources := wholeSources()
	svc, _, project := summaryService(sources)
	ctx := readCtx(t)

	first, err := svc.QualitySummary(ctx, project, app.SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached {
		t.Error("the first computation says it came from the cache")
	}
	// Break every source. A cached summary must not notice.
	sources.Coverage.(*fakeCoverage).err = errors.New("nobody should ask")

	second, err := svc.QualitySummary(ctx, project, app.SummaryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Cached {
		t.Fatal("the second read recomputed")
	}
	if second.Health.Coverage == nil || !second.ComputedAt.Equal(first.ComputedAt) {
		t.Errorf("the cached summary is not the first one: %+v", second)
	}
	// A different question is a different entry, and it is computed.
	other, err := svc.QualitySummary(ctx, project, app.SummaryQuery{Locale: "fr"})
	if err != nil {
		t.Fatal(err)
	}
	if other.Cached {
		t.Error("a different locale was served from another locale's entry")
	}
	if other.Health.Coverage != nil {
		t.Error("the broken source answered anyway")
	}
}

// TestRecordCheckRunRestatesTheDaysTrend: the rollup is written in the
// transaction that wrote the findings, for the day the run started, so
// the trend can never disagree with what it summarizes.
func TestRecordCheckRunRestatesTheDaysTrend(t *testing.T) {
	store := recordingStore()
	svc, project := serviceFor(store)
	started := dayAt(4)

	if _, err := svc.RecordCheckRun(writeCtx(t), app.RecordRun{
		Project: project, Ref: "main", Trigger: domain.TriggerCLI,
		Layers: []domain.Layer{domain.LayerParity}, StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	if len(store.rolledUp) != 1 || !store.rolledUp[0].Equal(started) {
		t.Fatalf("rolled up %v, want the day the run started (%v)", store.rolledUp, started)
	}
}

// localeRow is the summary's row for a locale.
func localeRow(t *testing.T, s app.Summary, code string) app.LocaleHealth {
	t.Helper()
	for _, l := range s.Locales {
		if l.Code == code {
			return l
		}
	}
	t.Fatalf("no row for %s in %+v", code, s.Locales)
	return app.LocaleHealth{}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
