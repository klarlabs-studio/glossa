package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	inteldomain "go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/adapters/linguistic"
	qualityreview "go.klarlabs.de/glossa/platform/internal/quality/adapters/review"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The whole seam, end to end: a job asks for a scope, the batcher
// expands it into (message, locale) pairs, the layer answers about each
// one, and what comes back is stored as one check run of warnings
// (RFC 0005 §3.8, §13 wave 6).
//
// The tests beside this one drive the service against a fake Linguist;
// this one drives it against the *real* seam — quality/adapters/review —
// with only the layer itself faked. That is the boundary that matters:
// the model call is the one thing this side of the port does not own,
// and it is the one thing replaced here.
//
// **Nothing here calls a provider.** The Layer is a struct declared in
// this file. A live provider could only arrive through the Intelligence
// port, which is also faked; the composition root is the only place a
// real router is ever built, and a test that reached one would have to
// have been handed the server's Intelligence service.

// ── the layer, faked ────────────────────────────────────────────────

// seamLayer answers about one translation. It suspects something about
// every message whose key it was told to suspect, which is how the test
// knows which pairs crossed the seam.
type seamLayer struct {
	suspect map[string]bool
	seen    []linguistic.Request
}

func (l *seamLayer) Review(_ context.Context, req linguistic.Request) (linguistic.Result, error) {
	l.seen = append(l.seen, req)
	if !l.suspect[req.Key] {
		return linguistic.Result{}, nil
	}
	r := layers.Reviewed{
		Locus: domain.Locus{
			Message: req.Message, Key: req.Key, Locale: req.TargetLocale,
			Namespace: req.Namespace, Revision: req.Revision,
			Span: &domain.Span{Side: domain.SideTarget, Start: 0, End: 4},
		},
		Code: layers.CodeToneMismatch, Quote: "Jetzt",
		Explanation:    "The guide asks for Sie and the translation says du.",
		Suggestion:     "Sie zahlen jetzt",
		SourceRevision: req.SourceRevision,
		Evidence:       map[string]any{layers.EvidencePromptVersion: "linguistic/v1"},
	}
	return linguistic.Result{Layer: layers.NewLinguistic([]layers.Reviewed{r}), Reviewed: []layers.Reviewed{r}}, nil
}

// seamIntelligence hands out that layer and says the tenant consented.
type seamIntelligence struct{ layer qualityreview.Layer }

func (i *seamIntelligence) ProviderConsent(context.Context) (bool, error) { return true, nil }

func (i *seamIntelligence) Layer(context.Context, uuid.UUID) (qualityreview.Layer, error) {
	return i.layer, nil
}

// ── the catalog, faked ──────────────────────────────────────────────

type seamCatalog struct{ msgs []qualityreview.Message }

func (c *seamCatalog) Messages(context.Context, uuid.UUID) ([]qualityreview.Message, error) {
	return c.msgs, nil
}

type seamLocalization struct{ tr qualityreview.Translated }

func (l *seamLocalization) Translations(
	context.Context, uuid.UUID, []string,
) (qualityreview.Translated, error) {
	return l.tr, nil
}

type seamKnowledge struct{}

func (seamKnowledge) RecognizeTerms(
	context.Context, inteldomain.Scope, inteldomain.LocalePair, string,
) ([]inteldomain.TermHit, error) {
	return nil, nil
}

func (seamKnowledge) EffectiveStyle(
	context.Context, inteldomain.Scope, string, string,
) (inteldomain.StyleGuide, error) {
	return inteldomain.StyleGuide{Version: "house@3", Formality: inteldomain.FormalityFormal}, nil
}

// ── the port the service sees ───────────────────────────────────────

// seamLinguist is the Linguist the service is wired with: the batcher
// for the three verbs that do the work, and the preflight the
// Intelligence adapter answers from the tenant's own settings. The
// preflight is canned here because its own path is tested next door;
// what is real here is everything after it.
type seamLinguist struct {
	*qualityreview.Batcher
	pre app.LinguisticPreflight
}

func (l *seamLinguist) Preflight(context.Context, uuid.UUID) (app.LinguisticPreflight, error) {
	return l.pre, nil
}

// seam builds the service over the real batcher, with two messages in
// `checkout` and one in `legal`, translated into German.
func seam(t *testing.T, sensitive ...string) (*app.Service, *fakeStore, uuid.UUID, *seamLayer) {
	t.Helper()
	layer := &seamLayer{suspect: map[string]bool{"checkout.pay": true}}
	b, err := qualityreview.New(qualityreview.Deps{
		Catalog: &seamCatalog{msgs: []qualityreview.Message{
			{ID: "m1", Key: "checkout.pay", Namespace: "checkout", Revision: 3,
				Description: "The button that takes the money.", Source: "Pay now"},
			{ID: "m2", Key: "checkout.cancel", Namespace: "checkout", Revision: 1, Source: "Cancel"},
			{ID: "m3", Key: "legal.terms", Namespace: "legal", Revision: 2, Source: "Terms of service"},
		}},
		Localization: &seamLocalization{tr: qualityreview.Translated{
			SourceLocale: "en",
			ByLocale: map[string]map[string]qualityreview.Translation{"de": {
				"m1": {Text: "Zahl jetzt", Revision: "tr_1", SourceRevision: 3},
				"m2": {Text: "Abbrechen", Revision: "tr_2", SourceRevision: 1},
				"m3": {Text: "Nutzungsbedingungen", Revision: "tr_3", SourceRevision: 2},
			}},
		}},
		Knowledge:    seamKnowledge{},
		Intelligence: &seamIntelligence{layer: layer},
	}, qualityreview.WithConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	store := recordingStore()
	svc, _, project := serviceAndCatalog(store)
	svc.SetLinguist(&seamLinguist{Batcher: b, pre: app.LinguisticPreflight{
		ProviderConsent: true, BudgetRemaining: 5_000_000, SensitiveNamespaces: sensitive,
	}})
	return svc, store, project, layer
}

// finished polls the job until it is over, as a client following a
// review does. The batcher runs the reviews in the background, so a
// read straight after the create legitimately finds it `running`.
func finished(t *testing.T, svc *app.Service, project, id uuid.UUID) domain.LinguisticJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, err := svc.GetLinguisticJob(writeCtx(t), project, id)
		if err != nil {
			t.Fatalf("GetLinguisticJob = %v", err)
		}
		if job.State.Final() {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("the job never finished; it is %s", job.State)
		}
		time.Sleep(time.Millisecond)
	}
}

// ── the tests ───────────────────────────────────────────────────────

// A job runs end to end: a scope in, stored findings out, every one of
// them a warning.
func TestAJobRunsThroughTheSeamToStoredFindings(t *testing.T) {
	svc, store, project, layer := seam(t)

	job, created, err := svc.RequestLinguisticReview(writeCtx(t), project, reviewOf("de"))
	if err != nil || !created {
		t.Fatalf("RequestLinguisticReview = %v, created %v", err, created)
	}
	if job.Batch == "" {
		t.Fatal("the job was never handed over: it has no batch handle")
	}
	job = finished(t, svc, project, job.ID)

	if job.State != domain.LinguisticSucceeded {
		t.Fatalf("job = %s (%s: %s)", job.State, job.FailureCode, job.LastError)
	}
	if job.Reviewed != 3 {
		t.Errorf("reviewed = %d, want the three German translations", job.Reviewed)
	}
	if job.Findings != 1 {
		t.Errorf("findings = %d, want the one the layer suspected", job.Findings)
	}
	if job.CheckRun == nil {
		t.Fatal("the findings were stored in no check run")
	}
	if len(store.inserted) != 1 {
		t.Fatalf("stored %d findings, want 1", len(store.inserted))
	}
	f := store.inserted[0]
	switch {
	case f.Layer != domain.LayerLinguistic:
		t.Errorf("layer = %s", f.Layer)
	case f.Severity != domain.Warning:
		t.Errorf("severity = %s, want warning", f.Severity)
	case f.Locus.Message != "m1":
		t.Errorf("the stored finding does not carry the catalog message ID: %+v", f.Locus)
	case f.Locus.Revision != "tr_1":
		t.Errorf("revision = %q, want the translation revision reviewed", f.Locus.Revision)
	case f.Fix == nil || f.Fix.Hint != "Sie zahlen jetzt":
		t.Errorf("fix = %+v; a suggestion is a hint, never an action", f.Fix)
	}
	// The layer was given what it needs to have an opinion.
	if len(layer.seen) != 3 {
		t.Fatalf("the layer saw %d translations, want 3", len(layer.seen))
	}
	for _, r := range layer.seen {
		if r.Message == "" {
			t.Errorf("%s: the layer was given no message ID, and it would refuse", r.Key)
		}
		if r.SourceLocale != "en" || r.Style == nil {
			t.Errorf("%s: request = %+v, want the source locale and the style guide", r.Key, r)
		}
	}
}

// A `sensitive` namespace never crosses the seam, and the count reaches
// the job: "the layer did not look there" is a thing a person has to be
// able to read off the record (RFC 0003 §7).
func TestASensitiveNamespaceNeverReachesTheLayerAndIsCounted(t *testing.T) {
	svc, _, project, layer := seam(t, "legal")

	job, _, err := svc.RequestLinguisticReview(writeCtx(t), project, reviewOf("de"))
	if err != nil {
		t.Fatal(err)
	}
	job = finished(t, svc, project, job.ID)

	if job.State != domain.LinguisticSucceeded {
		t.Fatalf("job = %s (%s: %s)", job.State, job.FailureCode, job.LastError)
	}
	if job.SkippedSensitive != 1 {
		t.Errorf("skipped = %d, want the one translation under `legal`", job.SkippedSensitive)
	}
	if job.Reviewed != 2 {
		t.Errorf("reviewed = %d, want 2", job.Reviewed)
	}
	for _, r := range layer.seen {
		if r.Namespace == "legal" {
			t.Fatalf("a sensitive namespace reached the layer: %+v", r)
		}
	}
}

// The advisory guarantee, on the real seam rather than a fake progress:
// the strictest policy a project may legally write cannot make a
// model's opinion an error (RFC 0005 §14 decision 10).
func TestTheRealSeamStillOnlyProducesWarnings(t *testing.T) {
	store := recordingStore()
	layer := &seamLayer{suspect: map[string]bool{"checkout.pay": true, "checkout.cancel": true}}
	b, err := qualityreview.New(qualityreview.Deps{
		Catalog: &seamCatalog{msgs: []qualityreview.Message{
			{ID: "m1", Key: "checkout.pay", Namespace: "checkout", Revision: 3, Source: "Pay now"},
			{ID: "m2", Key: "checkout.cancel", Namespace: "checkout", Revision: 1, Source: "Cancel"},
		}},
		Localization: &seamLocalization{tr: qualityreview.Translated{
			SourceLocale: "en",
			ByLocale: map[string]map[string]qualityreview.Translation{"de": {
				"m1": {Text: "Zahl jetzt", Revision: "tr_1", SourceRevision: 3},
				"m2": {Text: "Abbrechen", Revision: "tr_2", SourceRevision: 1},
			}},
		}},
		Knowledge:    seamKnowledge{},
		Intelligence: &seamIntelligence{layer: layer},
	}, qualityreview.WithConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	svc, catalog, project := serviceAndCatalog(store)
	svc.SetLinguist(&seamLinguist{
		Batcher: b,
		pre:     app.LinguisticPreflight{ProviderConsent: true, BudgetRemaining: 5_000_000},
	})
	// Everything to error, naming no layer: the wildcard a policy may
	// legally write, and the one that would raise an opinion if nothing
	// clamped it.
	catalog.policy = checkpolicy.Policy{Rules: []checkpolicy.Rule{{Severity: checkpolicy.Error}}}

	job, _, err := svc.RequestLinguisticReview(writeCtx(t), project, reviewOf("de"))
	if err != nil {
		t.Fatal(err)
	}
	job = finished(t, svc, project, job.ID)
	if job.State != domain.LinguisticSucceeded || job.Findings != 2 {
		t.Fatalf("job = %s with %d findings", job.State, job.Findings)
	}
	for _, f := range store.inserted {
		if f.Severity != domain.Warning {
			t.Errorf("%s: severity = %s; a build never fails on an opinion", f.Code, f.Severity)
		}
	}
	if store.recorded.Counts.Errors != 0 {
		t.Errorf("counts = %+v, want no errors", store.recorded.Counts)
	}
}

// Cancelling stops the review: the job is `cancelled`, and the batch
// behind it stops rather than running on and spending the tenant's
// budget on work nobody is waiting for.
func TestCancellingAJobStopsTheReview(t *testing.T) {
	svc, _, project, _ := seam(t)

	job, _, err := svc.RequestLinguisticReview(writeCtx(t), project, reviewOf("de"))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := svc.CancelLinguisticJob(writeCtx(t), project, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != domain.LinguisticCancelled {
		t.Fatalf("job = %s, want cancelled", cancelled.State)
	}
	// And it stays cancelled: a later read does not resurrect it into a
	// success, because the state is final and the job is not reconciled
	// again.
	again, err := svc.GetLinguisticJob(writeCtx(t), project, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.State != domain.LinguisticCancelled {
		t.Errorf("job = %s on a second read, want cancelled", again.State)
	}
}
