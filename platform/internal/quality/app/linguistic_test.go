package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Linguistic-QA jobs (RFC 0005 §3.8, §13 wave 6).
//
// **No test here touches a network or a provider.** The reviewer is a
// fake behind the Linguist port, which is the whole point of the port:
// the model call is Intelligence's, and Quality's side of the boundary
// is testable without one. The recorded provider responses RFC 0003 §4
// describes (cassettes, `intelligence/adapters/cassette`) belong to the
// slice that owns the prompt and the golden set; nothing on this path
// has a prompt to record.

// ── the store's linguistic-QA rows ──────────────────────────────────

// jobs is the fakeStore's linguistic-QA table. It is a map on the same
// fakeStore the rest of this package uses, so a test can exercise the
// service's whole path — create, read, reconcile, record — against one
// store.
func (f *fakeStore) InsertLinguisticJob(_ context.Context, j domain.LinguisticJob) error {
	if f.linguistic == nil {
		f.linguistic = map[uuid.UUID]domain.LinguisticJob{}
	}
	f.linguistic[j.ID] = j
	return nil
}

func (f *fakeStore) LinguisticJob(_ context.Context, _, id uuid.UUID) (domain.LinguisticJob, error) {
	j, ok := f.linguistic[id]
	if !ok {
		return domain.LinguisticJob{}, app.ErrLinguisticJobNotFound
	}
	return j, nil
}

func (f *fakeStore) ListLinguisticJobs(
	_ context.Context, _ uuid.UUID, state string, _ *app.LinguisticCursor, limit int,
) ([]domain.LinguisticJob, error) {
	var out []domain.LinguisticJob
	for _, j := range f.linguistic {
		if state != "" && string(j.State) != state {
			continue
		}
		out = append(out, j)
	}
	slices.SortFunc(out, func(a, b domain.LinguisticJob) int { return b.CreatedAt.Compare(a.CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// UpdateLinguisticJob moves only a job that has not finished, exactly
// as the guarded UPDATE does: a job another reader settled stays
// settled.
func (f *fakeStore) UpdateLinguisticJob(_ context.Context, j domain.LinguisticJob) (bool, error) {
	cur, ok := f.linguistic[j.ID]
	if !ok || cur.State.Final() {
		return false, nil
	}
	f.linguistic[j.ID] = j
	return true, nil
}

// ── a reviewer that never calls anything ────────────────────────────

// fakeLinguist stands in for Intelligence. It records what it was asked
// so a test can assert what *did not* cross the boundary — which is the
// point of the consent, budget and `sensitive` rules.
type fakeLinguist struct {
	pre    app.LinguisticPreflight
	preErr error
	// startErr is the refusal Start answers, and started what it saw.
	startErr error
	started  []app.LinguisticRequest
	// progress is what Poll answers, pollErr instead of it.
	progress app.LinguisticProgress
	pollErr  error
	// cancelled counts the reviews stopped.
	cancelled int
}

func (l *fakeLinguist) Preflight(context.Context, uuid.UUID) (app.LinguisticPreflight, error) {
	return l.pre, l.preErr
}

func (l *fakeLinguist) Start(_ context.Context, req app.LinguisticRequest) (string, error) {
	l.started = append(l.started, req)
	if l.startErr != nil {
		return "", l.startErr
	}
	return "batch-1", nil
}

func (l *fakeLinguist) Poll(context.Context, uuid.UUID, string) (app.LinguisticProgress, error) {
	return l.progress, l.pollErr
}

func (l *fakeLinguist) Cancel(context.Context, uuid.UUID, string) error {
	l.cancelled++
	return nil
}

// allowed is a tenant that consented and has budget, with no sensitive
// namespace: the preflight a review passes.
func allowed() app.LinguisticPreflight {
	return app.LinguisticPreflight{ProviderConsent: true, BudgetRemaining: 5_000_000}
}

// linguisticService returns a service that takes writes, with the
// reviewer wired, plus the Catalog and the project.
func linguisticService(l app.Linguist) (*app.Service, *fakeStore, *knownProjects, uuid.UUID) {
	store := recordingStore()
	svc, catalog, project := serviceAndCatalog(store)
	svc.SetLinguist(l)
	return svc, store, catalog, project
}

// writeCtx (policy_test.go) carries a principal that may write the
// catalog, which is what asking for a review needs: it records a check
// run and spends the tenant's AI budget.

func reviewOf(locales ...string) app.CreateLinguisticJob {
	return app.CreateLinguisticJob{Ref: "main", Scope: domain.LinguisticScope{Locales: locales}}
}

// suspects is one thing a model suspects, in the shape the port hands
// back — note that it has no severity to hand back.
func suspects(code, key, locale string) domain.LinguisticFinding {
	return domain.LinguisticFinding{
		Code: code, Key: key, Locale: locale, Namespace: "checkout",
		Explanation: "The target says the opposite of the source.",
		Subject:     key,
	}
}

// ── the advisory guarantee ──────────────────────────────────────────

// TestAModelsOpinionIsAlwaysAWarning is RFC 0005 §3.8 and §14 decision
// 10, on the one path a model's opinion can reach a stored finding.
//
// The policy here is as strict as a policy can legally be: a wildcard
// rule that raises *everything* to `error`. It is accepted, because it
// names no layer — checkpolicy.Rule.Validate only refuses a rule that
// names an advisory layer at `error`, and a project is allowed to be
// severe about its own layers. What it may not do is fail a build on an
// opinion, and it does not: every recorded finding is `warning`, and
// the policy's own decision says it clamped.
func TestAModelsOpinionIsAlwaysAWarning(t *testing.T) {
	l := &fakeLinguist{pre: allowed(), progress: app.LinguisticProgress{
		Done: true, Reviewed: 2,
		Findings: []domain.LinguisticFinding{
			suspects("meaning-divergence", "checkout.pay", "de"),
			suspects("tone-mismatch", "checkout.cancel", "de"),
		},
	}}
	svc, store, catalog, project := linguisticService(l)
	// Everything to error, naming no layer: the wildcard a policy may
	// legally write, and the one that would raise an opinion if nothing
	// clamped it.
	catalog.policy = checkpolicy.Policy{Rules: []checkpolicy.Rule{{Severity: checkpolicy.Error}}}

	job, created, err := svc.RequestLinguisticReview(writeCtx(t), project, reviewOf("de"))
	if err != nil || !created {
		t.Fatalf("RequestLinguisticReview = %v, created %v", err, created)
	}
	job, err = svc.GetLinguisticJob(writeCtx(t), project, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != domain.LinguisticSucceeded || job.Findings != 2 {
		t.Fatalf("job = %s with %d findings, want succeeded with 2", job.State, job.Findings)
	}
	if len(store.inserted) != 2 {
		t.Fatalf("stored %d findings, want 2", len(store.inserted))
	}
	for _, f := range store.inserted {
		if f.Layer != domain.LayerLinguistic {
			t.Errorf("%s: layer = %s, want linguistic", f.Code, f.Layer)
		}
		if f.Severity != domain.Warning {
			t.Errorf("%s: severity = %s under a policy that raises everything to error;"+
				" a build never fails on an opinion (RFC 0005 §14 decision 10)", f.Code, f.Severity)
		}
	}
	// The run agrees: nothing it counted is an error, so nothing it
	// graded can fail.
	if store.recorded.Counts.Errors != 0 || store.recorded.Counts.Warnings != 2 {
		t.Errorf("counts = %+v, want 2 warnings and no errors", store.recorded.Counts)
	}
	if store.recorded.Conclusion == domain.ConclusionFailure {
		t.Error("the run failed on a model's opinion")
	}
	// And the clamp is the kernel's, not a second one here: the policy
	// says it clamped, which is what `--explain-policy` prints.
	d := catalog.policy.Decide(store.inserted[0].Target(""))
	if !d.Clamped || d.Severity != checkpolicy.Warning {
		t.Errorf("decision = %+v, want the advisory clamp", d)
	}
}

// TestAPolicyMayNotNameTheLinguisticLayerAtError is the other half of
// the guarantee: the wildcard above is clamped when it grades, and a
// rule that names the layer outright never gets that far — the policy
// write is refused.
func TestAPolicyMayNotNameTheLinguisticLayerAtError(t *testing.T) {
	if !domain.LinguisticAdvisory() {
		t.Fatal("the kernel no longer calls `linguistic` advisory; the whole job rests on it")
	}
	raise := checkpolicy.Rule{Severity: checkpolicy.Error}
	raise.Layer = string(domain.LayerLinguistic)
	_, err := raise.Validate()
	if !errors.Is(err, checkpolicy.ErrAdvisoryLayer) {
		t.Fatalf("Validate() = %v, want ErrAdvisoryLayer", err)
	}
}

// TestSealRefusesWhatALayerCouldNotHaveFound: the reviewer's output is
// structurally constrained, and a code outside the four RFC 0005 §3.8
// names is malformed output, not a new rule the model invented.
func TestSealRefusesWhatALayerCouldNotHaveFound(t *testing.T) {
	for name, in := range map[string]domain.LinguisticFinding{
		"a code nobody defined": suspects("vibes-off", "checkout.pay", "de"),
		"no locale":             {Code: "tone-mismatch", Key: "checkout.pay", Explanation: "x"},
		"no message":            {Code: "tone-mismatch", Locale: "de", Explanation: "x"},
		"no explanation":        {Code: "tone-mismatch", Key: "checkout.pay", Locale: "de"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := domain.SealLinguistic(in); err == nil {
				t.Fatal("sealed a finding the layer could not have produced")
			}
		})
	}
	f, err := domain.SealLinguistic(suspects("grammar-suspected", "checkout.pay", "de"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Severity != domain.Warning {
		t.Errorf("severity = %s, want warning: the reviewer has none to offer", f.Severity)
	}
	if f.Fingerprint == "" {
		t.Error("a sealed finding has no identity")
	}
}

// ── the inherited rules ─────────────────────────────────────────────

// TestNothingIsSentWithoutConsentBudgetOrOutsideSensitive is RFC 0003
// §7 and RFC 0005 §10, inherited rather than restated. Each refusal
// ends the job with the code Intelligence uses for the same refusal,
// and — the part that matters — the reviewer is never asked, so no text
// leaves the platform.
func TestNothingIsSentWithoutConsentBudgetOrOutsideSensitive(t *testing.T) {
	cases := map[string]struct {
		pre  app.LinguisticPreflight
		in   app.CreateLinguisticJob
		want string
	}{
		"the tenant never consented to sending text": {
			pre:  app.LinguisticPreflight{ProviderConsent: false, BudgetRemaining: 5_000_000},
			in:   reviewOf("de"),
			want: domain.LinguisticFailureProviderConsent,
		},
		"this month's AI budget is spent": {
			pre:  app.LinguisticPreflight{ProviderConsent: true, BudgetRemaining: 0},
			in:   reviewOf("de"),
			want: domain.LinguisticFailureBudgetExceeded,
		},
		"the only namespace asked for is sensitive": {
			pre: app.LinguisticPreflight{
				ProviderConsent: true, BudgetRemaining: 5_000_000, SensitiveNamespaces: []string{"legal"},
			},
			in: app.CreateLinguisticJob{
				Ref: "main", Scope: domain.LinguisticScope{Locales: []string{"de"}, Namespace: "legal"},
			},
			want: domain.LinguisticFailureSensitive,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			l := &fakeLinguist{pre: tc.pre}
			svc, _, _, project := linguisticService(l)

			job, _, err := svc.RequestLinguisticReview(writeCtx(t), project, tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if job.State != domain.LinguisticFailed || job.FailureCode != tc.want {
				t.Fatalf("job = %s/%s, want failed/%s", job.State, job.FailureCode, tc.want)
			}
			if len(l.started) != 0 {
				t.Errorf("the reviewer was asked anyway: %+v", l.started)
			}
			if job.LastError == "" {
				t.Error("a refusal with no sentence saying what to change")
			}
		})
	}
}

// TestASensitiveNamespaceIsExcludedFromTheRequest: the rule is not only
// "refuse a job that asks for one". A review of the whole project must
// reach the reviewer with the sensitive namespaces named as excluded,
// so a message under one is never offered to a provider (RFC 0003 §7).
func TestASensitiveNamespaceIsExcludedFromTheRequest(t *testing.T) {
	pre := allowed()
	pre.SensitiveNamespaces = []string{"payments", "legal", "legal"}
	l := &fakeLinguist{pre: pre}
	svc, _, _, project := linguisticService(l)

	if _, _, err := svc.RequestLinguisticReview(writeCtx(t), project, reviewOf("de", "fr")); err != nil {
		t.Fatal(err)
	}
	if len(l.started) != 1 {
		t.Fatalf("the reviewer was asked %d times, want once", len(l.started))
	}
	if want := []string{"legal", "payments"}; !slices.Equal(l.started[0].ExcludeNamespaces, want) {
		t.Errorf("excluded = %v, want %v, deduplicated and ordered", l.started[0].ExcludeNamespaces, want)
	}
	if want := []string{"de", "fr"}; !slices.Equal(l.started[0].Scope.Locales, want) {
		t.Errorf("locales = %v, want %v canonical and sorted", l.started[0].Scope.Locales, want)
	}
}

// ── the job's life ──────────────────────────────────────────────────

// TestAProjectThatSwitchedTheLayerOffPaysForNothing: `off` means the
// project does not compute the layer (RFC 0005 §4.1), so no job exists
// and no model is called.
func TestAProjectThatSwitchedTheLayerOffPaysForNothing(t *testing.T) {
	l := &fakeLinguist{pre: allowed()}
	svc, store, catalog, project := linguisticService(l)
	off := checkpolicy.Rule{Severity: checkpolicy.Off}
	off.Layer = "linguistic"
	catalog.policy = checkpolicy.Policy{Rules: []checkpolicy.Rule{off}}

	_, _, err := svc.RequestLinguisticReview(writeCtx(t), project, reviewOf("de"))
	if !errors.Is(err, app.ErrLinguisticLayerOff) {
		t.Fatalf("err = %v, want ErrLinguisticLayerOff", err)
	}
	if len(store.linguistic) != 0 || len(l.started) != 0 {
		t.Error("a job was created for a layer the project does not compute")
	}
}

// TestADeploymentWithNoReviewerSaysSo: "no model looked" and "a model
// looked and found nothing" are different answers, and only one of them
// is a clean bill of health.
func TestADeploymentWithNoReviewerSaysSo(t *testing.T) {
	svc, _ := serviceFor(recordingStore())

	_, _, err := svc.RequestLinguisticReview(writeCtx(t), uuid.New(), reviewOf("de"))
	if err == nil {
		t.Fatal("a review was accepted with no reviewer wired")
	}
}

// TestARetryFindsTheReviewItAlreadyPaidFor: a review costs money, so an
// Idempotency-Key makes a resent request find the job rather than start
// a second one — and a key reused for a different review is refused.
func TestARetryFindsTheReviewItAlreadyPaidFor(t *testing.T) {
	l := &fakeLinguist{pre: allowed()}
	svc, _, _, project := linguisticService(l)
	ctx := writeCtx(t)
	in := reviewOf("de")
	in.IdempotencyKey = "0192f0c4-abc"

	first, created, err := svc.RequestLinguisticReview(ctx, project, in)
	if err != nil || !created {
		t.Fatalf("first = %v, created %v", err, created)
	}
	again, created, err := svc.RequestLinguisticReview(ctx, project, in)
	if err != nil {
		t.Fatal(err)
	}
	if created || again.ID != first.ID {
		t.Fatalf("retry created %v with id %s, want the first job %s", created, again.ID, first.ID)
	}
	if len(l.started) != 1 {
		t.Errorf("the reviewer was asked %d times, want once", len(l.started))
	}
	other := reviewOf("fr")
	other.IdempotencyKey = in.IdempotencyKey
	if _, _, err := svc.RequestLinguisticReview(ctx, project, other); !errors.Is(err, app.ErrIdempotencyReuse) {
		t.Errorf("reusing the key for another review = %v, want ErrIdempotencyReuse", err)
	}
}

// TestRecordingIsIdempotent: a finished job's findings are recorded
// once. Polling it again must not double a project's linguistic
// findings, which is what the job's own check_run guarantees.
func TestRecordingIsIdempotent(t *testing.T) {
	l := &fakeLinguist{pre: allowed(), progress: app.LinguisticProgress{
		Done: true, Reviewed: 1, SkippedSensitive: 3,
		Findings: []domain.LinguisticFinding{suspects("inconsistent-phrasing", "checkout.pay", "de")},
	}}
	svc, store, _, project := linguisticService(l)
	ctx := writeCtx(t)

	job, _, err := svc.RequestLinguisticReview(ctx, project, reviewOf("de"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.GetLinguisticJob(ctx, project, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.CheckRun == nil || first.SkippedSensitive != 3 {
		t.Fatalf("job = %+v, want a run and the sensitive count carried through", first)
	}
	again, err := svc.GetLinguisticJob(ctx, project, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.CheckRun == nil || *again.CheckRun != *first.CheckRun {
		t.Errorf("run = %v, want the one it already had", again.CheckRun)
	}
	if len(store.inserted) != 1 {
		t.Errorf("stored %d findings after two reads, want 1", len(store.inserted))
	}
}

// TestCancelOnlyStopsAJobThatHasNotFinished: cancelling is not a way to
// undo findings. A finding a job recorded stays, and is waived rather
// than deleted (RFC 0005 §14 decision 5).
func TestCancelOnlyStopsAJobThatHasNotFinished(t *testing.T) {
	l := &fakeLinguist{pre: allowed()}
	svc, _, _, project := linguisticService(l)
	ctx := writeCtx(t)

	job, _, err := svc.RequestLinguisticReview(ctx, project, reviewOf("de"))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := svc.CancelLinguisticJob(ctx, project, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != domain.LinguisticCancelled || cancelled.FinishedAt == nil {
		t.Fatalf("job = %+v, want cancelled and finished", cancelled)
	}
	if l.cancelled != 1 {
		t.Errorf("the reviewer was stopped %d times, want once", l.cancelled)
	}
	_, err = svc.CancelLinguisticJob(ctx, project, job.ID)
	if !errors.Is(err, domain.ErrLinguisticJobNotCancellable) {
		t.Errorf("cancelling it twice = %v, want ErrLinguisticJobNotCancellable", err)
	}
	// And a cancelled job is not reconciled back to life by a read.
	read, err := svc.GetLinguisticJob(ctx, project, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if read.State != domain.LinguisticCancelled {
		t.Errorf("reading it made it %s again", read.State)
	}
}

// TestAScopeThatSelectsNothingIsRefused: a review is of a translation,
// and there is no translation without a locale.
func TestAScopeThatSelectsNothingIsRefused(t *testing.T) {
	l := &fakeLinguist{pre: allowed()}
	svc, _, _, project := linguisticService(l)

	for name, scope := range map[string]domain.LinguisticScope{
		"no locale at all":            {},
		"a locale that is not one":    {Locales: []string{"not a tag"}},
		"more locales than the limit": {Locales: make([]string, domain.MaxLinguisticLocales+1)},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := svc.RequestLinguisticReview(writeCtx(t), project,
				app.CreateLinguisticJob{Ref: "main", Scope: scope})
			if !errors.Is(err, domain.ErrInvalidLinguisticScope) {
				t.Fatalf("err = %v, want ErrInvalidLinguisticScope", err)
			}
		})
	}
}

// TestListDoesNotFollowARunningJob: one page should not fan out into a
// poll per row. A list is as of the last read; a read of one job brings
// that job up to date.
func TestListDoesNotFollowARunningJob(t *testing.T) {
	l := &fakeLinguist{pre: allowed(), progress: app.LinguisticProgress{Done: true}}
	svc, _, _, project := linguisticService(l)
	ctx := writeCtx(t)

	if _, _, err := svc.RequestLinguisticReview(ctx, project, reviewOf("de")); err != nil {
		t.Fatal(err)
	}
	jobs, _, err := svc.ListLinguisticJobs(ctx, project, "", pageOf(pagination.DefaultPageSize))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].State != domain.LinguisticRunning {
		t.Fatalf("jobs = %+v, want the one job as it was left", jobs)
	}
	if _, _, err := svc.ListLinguisticJobs(ctx, project, "nonsense", pageOf(10)); !errors.Is(err, app.ErrInvalidQuery) {
		t.Errorf("an unknown state = %v, want ErrInvalidQuery", err)
	}
}

// TestReadingNeedsTheCatalogAndAskingNeedsTheWrite: a finding is about
// the catalog's messages, and asking for a review spends money.
func TestReadingNeedsTheCatalogAndAskingNeedsTheWrite(t *testing.T) {
	l := &fakeLinguist{pre: allowed()}
	svc, _, _, project := linguisticService(l)

	// A reader may not ask for a review.
	if _, _, err := svc.RequestLinguisticReview(readCtx(t), project, reviewOf("de")); err == nil {
		t.Error("catalog.read was enough to spend the tenant's AI budget")
	}
	job, _, err := svc.RequestLinguisticReview(writeCtx(t), project, reviewOf("de"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetLinguisticJob(readCtx(t), project, job.ID); err != nil {
		t.Errorf("catalog.read could not read a job: %v", err)
	}
	if _, err := svc.CancelLinguisticJob(readCtx(t), project, job.ID); err == nil {
		t.Error("catalog.read was enough to cancel a review")
	}
}

// TestAJobOfAnUnknownProjectIsNotFound keeps an unknown project a 404
// rather than an empty answer.
func TestAJobOfAnUnknownProjectIsNotFound(t *testing.T) {
	l := &fakeLinguist{pre: allowed()}
	svc, _, _, _ := linguisticService(l)

	if _, err := svc.GetLinguisticJob(readCtx(t), uuid.New(), uuid.New()); !errors.Is(err, app.ErrProjectNotFound) {
		t.Fatalf("err = %v, want ErrProjectNotFound", err)
	}
}

// TestTheJobTimesAreRecorded: a job says when it started and when it
// ended, which is what a dashboard shows and what a stuck review is
// found by.
func TestTheJobTimesAreRecorded(t *testing.T) {
	at := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	l := &fakeLinguist{pre: allowed()}
	store := recordingStore()
	project := uuid.New()
	svc := app.NewService(fakeTx{store: store}, &knownProjects{id: project, projectVersion: 1},
		app.WithClock(func() time.Time { return at }))
	svc.SetLinguist(l)

	job, _, err := svc.RequestLinguisticReview(writeCtx(t), project, reviewOf("de"))
	if err != nil {
		t.Fatal(err)
	}
	if !job.CreatedAt.Equal(at) || job.StartedAt == nil || !job.StartedAt.Equal(at) {
		t.Fatalf("job times = %v / %v, want the clock's", job.CreatedAt, job.StartedAt)
	}
	if job.FinishedAt != nil {
		t.Error("a running job is finished")
	}
}
