package review_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	inteldomain "go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/quality/adapters/linguistic"
	"go.klarlabs.de/glossa/platform/internal/quality/adapters/review"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
)

// The seam between the linguistic-QA job and the linguistic layer.
//
// **Nothing in this file can reach a provider or a network.** The Layer
// port is a struct declared here; the only thing that ever builds a
// real one is the composition root, which hands it a router over the
// tenant's configured providers. A live provider would have to arrive
// through review.Deps.Intelligence, and every test below passes a fake
// one — so a test that somehow called out would fail on the fake's own
// recorded calls, loudly, rather than quietly costing money.

// ── the fakes ───────────────────────────────────────────────────────

type fakeCatalog struct {
	msgs []review.Message
	err  error
}

func (c *fakeCatalog) Messages(context.Context, uuid.UUID) ([]review.Message, error) {
	return c.msgs, c.err
}

type fakeLocalization struct {
	tr  review.Translated
	err error
	// asked records the locales the expansion narrowed to.
	asked []string
}

func (l *fakeLocalization) Translations(
	_ context.Context, _ uuid.UUID, locales []string,
) (review.Translated, error) {
	l.asked = locales
	return l.tr, l.err
}

// fakeKnowledge answers with fixed evidence and records the pairs it
// was asked about, so a test can assert what the reviewer was shown.
type fakeKnowledge struct {
	mu    sync.Mutex
	terms []inteldomain.TermHit
	style inteldomain.StyleGuide
	seen  []inteldomain.LocalePair
}

func (k *fakeKnowledge) RecognizeTerms(
	_ context.Context, _ inteldomain.Scope, pair inteldomain.LocalePair, _ string,
) ([]inteldomain.TermHit, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.seen = append(k.seen, pair)
	return k.terms, nil
}

func (k *fakeKnowledge) EffectiveStyle(
	_ context.Context, _ inteldomain.Scope, _, _ string,
) (inteldomain.StyleGuide, error) {
	return k.style, nil
}

// fakeLayer stands in for the linguistic layer. answer decides what one
// review returns; got records every request that crossed the seam,
// which is how a test asserts what did *not* — the point of the
// `sensitive` rule.
type fakeLayer struct {
	mu     sync.Mutex
	got    []linguistic.Request
	answer func(n int, req linguistic.Request) (linguistic.Result, error)
	// inflight tracks concurrent calls, so the bound can be measured
	// rather than assumed.
	inflight, peak atomic.Int64
}

func (f *fakeLayer) Review(ctx context.Context, req linguistic.Request) (linguistic.Result, error) {
	n := f.inflight.Add(1)
	for {
		peak := f.peak.Load()
		if n <= peak || f.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	defer f.inflight.Add(-1)
	if err := ctx.Err(); err != nil {
		return linguistic.Result{}, err
	}
	f.mu.Lock()
	f.got = append(f.got, req)
	i := len(f.got)
	f.mu.Unlock()
	if f.answer == nil {
		return linguistic.Result{}, nil
	}
	return f.answer(i, req)
}

func (f *fakeLayer) requests() []linguistic.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]linguistic.Request(nil), f.got...)
}

type fakeIntelligence struct {
	consent    bool
	consentErr error
	layer      review.Layer
	layerErr   error
}

func (i *fakeIntelligence) ProviderConsent(context.Context) (bool, error) {
	return i.consent, i.consentErr
}

func (i *fakeIntelligence) Layer(context.Context, uuid.UUID) (review.Layer, error) {
	return i.layer, i.layerErr
}

// ── a project to review ─────────────────────────────────────────────

// catalog is three messages in two namespaces: two under `checkout` and
// one under `legal`, which the tests tag sensitive.
func catalog() *fakeCatalog {
	return &fakeCatalog{msgs: []review.Message{
		{ID: "m1", Key: "checkout.pay", Namespace: "checkout", Revision: 3,
			Description: "The button that takes the money.", Source: "Pay now"},
		{ID: "m2", Key: "checkout.cancel", Namespace: "checkout", Revision: 1, Source: "Cancel"},
		{ID: "m3", Key: "legal.terms", Namespace: "legal", Revision: 2, Source: "Terms of service"},
	}}
}

func translations() *fakeLocalization {
	return &fakeLocalization{tr: review.Translated{
		SourceLocale: "en",
		ByLocale: map[string]map[string]review.Translation{
			"de": {
				"m1": {Text: "Jetzt zahlen", Revision: "tr_1", SourceRevision: 3},
				"m2": {Text: "Abbrechen", Revision: "tr_2", SourceRevision: 1},
				"m3": {Text: "Nutzungsbedingungen", Revision: "tr_3", SourceRevision: 2},
			},
			"fr": {
				"m1": {Text: "Payer", Revision: "tr_4", SourceRevision: 3},
			},
		},
	}}
}

// notes is one accepted note, in the shape the layer hands back: a
// sealed layer plus the pre-seal notes the seam reads.
func notes(req linguistic.Request, code, quote string) linguistic.Result {
	r := layers.Reviewed{
		Locus: domain.Locus{
			Message: req.Message, Key: req.Key, Locale: req.TargetLocale,
			Namespace: req.Namespace, Revision: req.Revision,
			Span: &domain.Span{Side: domain.SideTarget, Start: 0, End: len(quote)},
		},
		Code: code, Quote: quote, Explanation: "The register is wrong for this product.",
		Suggestion: "Sie", SourceRevision: req.SourceRevision,
		Evidence: map[string]any{layers.EvidencePromptVersion: "linguistic/v1"},
	}
	return linguistic.Result{Layer: layers.NewLinguistic([]layers.Reviewed{r}), Reviewed: []layers.Reviewed{r}}
}

func batcher(t *testing.T, layer review.Layer, opts ...review.Option) (*review.Batcher, *fakeLocalization, *fakeKnowledge) {
	t.Helper()
	local, know := translations(), &fakeKnowledge{style: inteldomain.StyleGuide{Version: "v3", Formality: "formal"}}
	b, err := review.New(review.Deps{
		Catalog: catalog(), Localization: local, Knowledge: know,
		Intelligence: &fakeIntelligence{consent: true, layer: layer},
	}, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return b, local, know
}

func ctxOf(t *testing.T) context.Context {
	t.Helper()
	return authztest.Token(t.Context(), tenancy.NewID(), "read", "write")
}

func request(project uuid.UUID, scope domain.LinguisticScope, exclude ...string) app.LinguisticRequest {
	return app.LinguisticRequest{
		Project: project, Job: uuid.New(), Ref: "main", Scope: scope, ExcludeNamespaces: exclude,
	}
}

// settle polls until the batch is over, so a test never sleeps on a
// duration it guessed.
func settle(t *testing.T, b *review.Batcher, project uuid.UUID, id string) app.LinguisticProgress {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, err := b.Poll(t.Context(), project, id)
		if err != nil {
			t.Fatalf("Poll = %v", err)
		}
		if p.Done || p.Cancelled {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatal("the batch never finished")
		}
		time.Sleep(time.Millisecond)
	}
}

// ── expansion ───────────────────────────────────────────────────────

// A scope is locales × the slice of the catalog it names, and a pair
// exists only where something has actually been translated.
func TestAScopeExpandsToTheTranslationsItNames(t *testing.T) {
	layer := &fakeLayer{}
	b, local, know := batcher(t, layer)
	project := uuid.New()

	id, err := b.Start(ctxOf(t), request(project, domain.LinguisticScope{Locales: []string{"de", "fr"}}))
	if err != nil {
		t.Fatal(err)
	}
	p := settle(t, b, project, id)

	// Three German translations and one French one: `fr` has only
	// `checkout.pay`, and a message nobody translated is the
	// completeness layer's business, not this one's.
	if p.Reviewed != 4 {
		t.Errorf("reviewed = %d, want 4", p.Reviewed)
	}
	if len(local.asked) != 2 {
		t.Errorf("Localization was asked for %v, want the scope's two locales", local.asked)
	}
	got := map[string]bool{}
	for _, r := range layer.requests() {
		got[r.Key+"/"+r.TargetLocale] = true
		if r.SourceLocale != "en" {
			t.Errorf("%s: source locale = %q, want the project's", r.Key, r.SourceLocale)
		}
		if !r.ProviderConsent {
			t.Errorf("%s: the request does not carry the tenant's consent, so Intelligence would refuse it", r.Key)
		}
	}
	for _, want := range []string{
		"checkout.pay/de", "checkout.cancel/de", "legal.terms/de", "checkout.pay/fr",
	} {
		if !got[want] {
			t.Errorf("%s was not reviewed; got %v", want, got)
		}
	}
	// The knowledge was asked about the right pair, which is what makes
	// the terms and the style guide evidence about this translation.
	if len(know.seen) != 4 || know.seen[0].Source != "en" {
		t.Errorf("knowledge saw %+v", know.seen)
	}
}

// The catalog identity travels: the layer refuses a request without a
// message ID, because a fingerprint over a key is not the one every
// other surface computes (RFC 0005 §2.1).
func TestEveryRequestCarriesTheCatalogMessageID(t *testing.T) {
	layer := &fakeLayer{}
	b, _, _ := batcher(t, layer)
	project := uuid.New()

	id, err := b.Start(ctxOf(t), request(project, domain.LinguisticScope{Locales: []string{"de"}, Keys: []string{"checkout.pay"}}))
	if err != nil {
		t.Fatal(err)
	}
	settle(t, b, project, id)

	reqs := layer.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %+v, want one", reqs)
	}
	r := reqs[0]
	switch {
	case r.Message != "m1":
		t.Errorf("message = %q, want the catalog's ID", r.Message)
	case r.Revision != "tr_1":
		t.Errorf("revision = %q, want the translation revision under review", r.Revision)
	case r.SourceRevision == nil || *r.SourceRevision != 3:
		t.Errorf("source revision = %v, want 3: a waiver dies when it changes", r.SourceRevision)
	case r.Description != "The button that takes the money.":
		t.Errorf("description = %q, want the catalog's", r.Description)
	case r.Source != "Pay now" || r.Translation != "Jetzt zahlen":
		t.Errorf("texts = %q / %q", r.Source, r.Translation)
	}
}

// A source that cannot name a message is refused outright rather than
// allowed to mint findings under an identity nobody else computes.
func TestAMessageWithNoIDIsRefused(t *testing.T) {
	b, err := review.New(review.Deps{
		Catalog: &fakeCatalog{msgs: []review.Message{
			{Key: "checkout.pay", Namespace: "checkout", Source: "Pay now"},
		}},
		Localization: translations(),
		Knowledge:    &fakeKnowledge{},
		Intelligence: &fakeIntelligence{consent: true, layer: &fakeLayer{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Start(ctxOf(t), request(uuid.New(), domain.LinguisticScope{Locales: []string{"de"}})); err == nil {
		t.Fatal("a message with no catalog ID was reviewed anyway")
	}
}

// The narrowing members do narrow, and an empty one does not.
func TestTheScopeNarrows(t *testing.T) {
	for name, tc := range map[string]struct {
		scope domain.LinguisticScope
		want  []string
	}{
		"a namespace": {
			scope: domain.LinguisticScope{Locales: []string{"de"}, Namespace: "checkout"},
			want:  []string{"checkout.pay", "checkout.cancel"},
		},
		"a key prefix": {
			scope: domain.LinguisticScope{Locales: []string{"de"}, KeyPrefix: "legal."},
			want:  []string{"legal.terms"},
		},
		"named keys": {
			scope: domain.LinguisticScope{Locales: []string{"de"}, Keys: []string{"checkout.cancel"}},
			want:  []string{"checkout.cancel"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			layer := &fakeLayer{}
			b, _, _ := batcher(t, layer)
			project := uuid.New()
			id, err := b.Start(ctxOf(t), request(project, tc.scope))
			if err != nil {
				t.Fatal(err)
			}
			settle(t, b, project, id)
			var got []string
			for _, r := range layer.requests() {
				got = append(got, r.Key)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("reviewed %v, want %v", got, tc.want)
			}
			for _, w := range tc.want {
				if !contains(got, w) {
					t.Errorf("reviewed %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// ── the sensitive rule ──────────────────────────────────────────────

// A namespace tagged `sensitive` is never offered to a provider
// (RFC 0003 §7), and what was left out is counted rather than dropped:
// a reviewer has to know the layer did not look there.
func TestASensitiveNamespaceIsExcludedAndCounted(t *testing.T) {
	layer := &fakeLayer{}
	b, _, _ := batcher(t, layer)
	project := uuid.New()

	id, err := b.Start(ctxOf(t), request(project, domain.LinguisticScope{Locales: []string{"de", "fr"}}, "legal"))
	if err != nil {
		t.Fatal(err)
	}
	p := settle(t, b, project, id)

	if p.SkippedSensitive != 1 {
		t.Errorf("skipped = %d, want the one German translation under `legal`", p.SkippedSensitive)
	}
	if p.Reviewed != 3 {
		t.Errorf("reviewed = %d, want 3", p.Reviewed)
	}
	for _, r := range layer.requests() {
		if r.Namespace == "legal" {
			t.Fatalf("a message under a sensitive namespace reached the reviewer: %+v", r)
		}
	}
	for _, f := range p.Findings {
		if f.Namespace == "legal" {
			t.Fatalf("a finding under a sensitive namespace: %+v", f)
		}
	}
}

// ── findings ────────────────────────────────────────────────────────

// What comes back is what the port promises: a suspicion with no
// severity, carrying the locus and the evidence the layer resolved.
func TestAFindingComesBackWithoutASeverity(t *testing.T) {
	layer := &fakeLayer{answer: func(_ int, req linguistic.Request) (linguistic.Result, error) {
		if req.Key != "checkout.pay" {
			return linguistic.Result{}, nil
		}
		return notes(req, layers.CodeToneMismatch, "Jetzt"), nil
	}}
	b, _, _ := batcher(t, layer)
	project := uuid.New()

	id, err := b.Start(ctxOf(t), request(project, domain.LinguisticScope{Locales: []string{"de"}}))
	if err != nil {
		t.Fatal(err)
	}
	p := settle(t, b, project, id)

	if len(p.Findings) != 1 {
		t.Fatalf("findings = %+v, want one", p.Findings)
	}
	f := p.Findings[0]
	switch {
	case f.Code != layers.CodeToneMismatch:
		t.Errorf("code = %q", f.Code)
	case f.Message != "m1" || f.Key != "checkout.pay" || f.Locale != "de" || f.Namespace != "checkout":
		t.Errorf("locus = %+v", f)
	case f.Revision != "tr_1":
		t.Errorf("revision = %q", f.Revision)
	case f.Span == nil || f.Span.Side != domain.SideTarget:
		t.Errorf("span = %+v", f.Span)
	case f.Suggestion != "Sie":
		t.Errorf("suggestion = %q", f.Suggestion)
	case f.Evidence[layers.EvidencePromptVersion] != "linguistic/v1":
		t.Errorf("evidence = %+v", f.Evidence)
	}
	// Sealing is the domain's, and it is the only place a severity is
	// set. A finding that arrived here with one would mean the port had
	// grown a way to assert it.
	sealed, err := domain.SealLinguistic(f)
	if err != nil {
		t.Fatal(err)
	}
	if sealed.Severity != domain.Warning {
		t.Errorf("severity = %q, want warning", sealed.Severity)
	}
}

// ── bounds ──────────────────────────────────────────────────────────

// A large scope does not become a large fan-out: at most Concurrency
// model calls are ever in flight.
func TestTheFanOutIsBounded(t *testing.T) {
	msgs := make([]review.Message, 0, 60)
	texts := map[string]review.Translation{}
	for i := range 60 {
		id := fmt.Sprintf("m%d", i)
		msgs = append(msgs, review.Message{
			ID: id, Key: fmt.Sprintf("checkout.k%d", i), Namespace: "checkout", Revision: 1, Source: "Pay",
		})
		texts[id] = review.Translation{Text: "Zahlen", Revision: "tr_" + id, SourceRevision: 1}
	}
	layer := &fakeLayer{answer: func(int, linguistic.Request) (linguistic.Result, error) {
		time.Sleep(time.Millisecond)
		return linguistic.Result{}, nil
	}}
	b, err := review.New(review.Deps{
		Catalog: &fakeCatalog{msgs: msgs},
		Localization: &fakeLocalization{tr: review.Translated{
			SourceLocale: "en", ByLocale: map[string]map[string]review.Translation{"de": texts},
		}},
		Knowledge:    &fakeKnowledge{},
		Intelligence: &fakeIntelligence{consent: true, layer: layer},
	}, review.WithConcurrency(3))
	if err != nil {
		t.Fatal(err)
	}
	project := uuid.New()
	id, err := b.Start(ctxOf(t), request(project, domain.LinguisticScope{Locales: []string{"de"}}))
	if err != nil {
		t.Fatal(err)
	}
	p := settle(t, b, project, id)
	if p.Reviewed != 60 {
		t.Errorf("reviewed = %d, want 60", p.Reviewed)
	}
	if peak := layer.peak.Load(); peak > 3 {
		t.Errorf("%d reviews ran at once, want at most 3", peak)
	}
}

// A scope bigger than the cap is refused with the sentence that says
// how to narrow it, rather than reviewing an arbitrary prefix of
// somebody's catalog.
func TestAnOversizedScopeIsRefused(t *testing.T) {
	b, err := review.New(review.Deps{
		Catalog: catalog(), Localization: translations(), Knowledge: &fakeKnowledge{},
		Intelligence: &fakeIntelligence{consent: true, layer: &fakeLayer{}},
	}, review.WithMaxPairs(2))
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Start(ctxOf(t), request(uuid.New(), domain.LinguisticScope{Locales: []string{"de", "fr"}}))
	if !errors.Is(err, review.ErrScopeTooLarge) {
		t.Fatalf("err = %v, want ErrScopeTooLarge", err)
	}
	if !strings.Contains(err.Error(), "narrow") {
		t.Errorf("the refusal does not say what to do: %v", err)
	}
}

// ── cancellation ────────────────────────────────────────────────────

// A cancel stops the review and Poll says Cancelled, which is what the
// job turns into the `cancelled` state.
func TestCancellationStopsTheBatch(t *testing.T) {
	msgs := make([]review.Message, 0, 40)
	texts := map[string]review.Translation{}
	for i := range 40 {
		id := fmt.Sprintf("m%d", i)
		msgs = append(msgs, review.Message{
			ID: id, Key: fmt.Sprintf("checkout.k%d", i), Namespace: "checkout", Revision: 1, Source: "Pay",
		})
		texts[id] = review.Translation{Text: "Zahlen", Revision: "tr_" + id, SourceRevision: 1}
	}
	started := make(chan struct{})
	var once sync.Once
	layer := &fakeLayer{answer: func(int, linguistic.Request) (linguistic.Result, error) {
		once.Do(func() { close(started) })
		time.Sleep(20 * time.Millisecond)
		return linguistic.Result{}, nil
	}}
	b, err := review.New(review.Deps{
		Catalog: &fakeCatalog{msgs: msgs},
		Localization: &fakeLocalization{tr: review.Translated{
			SourceLocale: "en", ByLocale: map[string]map[string]review.Translation{"de": texts},
		}},
		Knowledge:    &fakeKnowledge{},
		Intelligence: &fakeIntelligence{consent: true, layer: layer},
	}, review.WithConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	project := uuid.New()
	id, err := b.Start(ctxOf(t), request(project, domain.LinguisticScope{Locales: []string{"de"}}))
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if err := b.Cancel(t.Context(), project, id); err != nil {
		t.Fatal(err)
	}
	p, err := b.Poll(t.Context(), project, id)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Cancelled {
		t.Fatalf("progress = %+v, want cancelled", p)
	}
	// And it really stopped: far fewer than the forty pairs were sent.
	time.Sleep(50 * time.Millisecond)
	if n := len(layer.requests()); n > 10 {
		t.Errorf("%d reviews ran after the cancel; the batch did not stop", n)
	}
}

// Cancelling a batch nobody started is nothing to stop, not an error
// the job has to handle.
func TestCancellingAnUnknownBatchIsNotFound(t *testing.T) {
	b, _, _ := batcher(t, &fakeLayer{})
	if err := b.Cancel(t.Context(), uuid.New(), "lq_nope"); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// A batch belongs to its project. Another project's job cannot follow
// it, or stop it.
func TestABatchBelongsToItsProject(t *testing.T) {
	b, _, _ := batcher(t, &fakeLayer{})
	project := uuid.New()
	id, err := b.Start(ctxOf(t), request(project, domain.LinguisticScope{Locales: []string{"de"}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Poll(t.Context(), uuid.New(), id); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// ── the budget ──────────────────────────────────────────────────────

// The Intelligence router checks the tenant's budget on every call. When
// it refuses mid-batch the review stops cleanly and reports what it
// managed: evidence already paid for is not thrown away to report a
// code, and nothing spins retrying a call that will be refused again.
func TestABudgetRefusalMidBatchStopsAndReportsWhatItManaged(t *testing.T) {
	layer := &fakeLayer{answer: func(n int, req linguistic.Request) (linguistic.Result, error) {
		if n == 1 {
			return notes(req, layers.CodeMeaningDivergence, "Jetzt"), nil
		}
		return linguistic.Result{}, fmt.Errorf("review: %w", inteldomain.ErrBudgetExceeded)
	}}
	b, err := review.New(review.Deps{
		Catalog: catalog(), Localization: translations(), Knowledge: &fakeKnowledge{},
		Intelligence: &fakeIntelligence{consent: true, layer: layer},
	}, review.WithConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	project := uuid.New()
	id, err := b.Start(ctxOf(t), request(project, domain.LinguisticScope{Locales: []string{"de", "fr"}}))
	if err != nil {
		t.Fatal(err)
	}
	p := settle(t, b, project, id)

	if !p.Done {
		t.Fatalf("progress = %+v, want a finished batch", p)
	}
	if p.Reviewed != 1 {
		t.Errorf("reviewed = %d, want the one review that got through", p.Reviewed)
	}
	if len(p.Findings) != 1 {
		t.Errorf("findings = %+v, want the one the budget paid for", p.Findings)
	}
	// It stopped rather than trying every remaining pair.
	if n := len(layer.requests()); n > 2 {
		t.Errorf("%d calls were made after the budget refused; the batch did not stop", n)
	}
}

// A batch that produced nothing at all has nothing to report but the
// refusal, so it reports it in Quality's vocabulary and the job fails
// with `budget_exceeded`.
func TestABudgetRefusalWithNothingToShowIsReportedAsOne(t *testing.T) {
	layer := &fakeLayer{answer: func(int, linguistic.Request) (linguistic.Result, error) {
		return linguistic.Result{}, fmt.Errorf("review: %w", inteldomain.ErrBudgetExceeded)
	}}
	b, _, _ := batcher(t, layer, review.WithConcurrency(1))
	project := uuid.New()
	id, err := b.Start(ctxOf(t), request(project, domain.LinguisticScope{Locales: []string{"de"}}))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := b.Poll(t.Context(), project, id)
		if errors.Is(err, app.ErrBudgetExceeded) {
			return
		}
		if err != nil {
			t.Fatalf("Poll = %v, want ErrBudgetExceeded", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("the batch never reported the refusal")
		}
		time.Sleep(time.Millisecond)
	}
}

// One malformed answer costs one translation, not the batch: a model
// that answers with prose about `checkout.pay` has said nothing about
// it, and nothing about `checkout.cancel` either way.
func TestAMalformedAnswerCostsOneTranslation(t *testing.T) {
	layer := &fakeLayer{answer: func(n int, req linguistic.Request) (linguistic.Result, error) {
		if n == 1 {
			return linguistic.Result{}, fmt.Errorf("review: %w", inteldomain.ErrMalformedReview)
		}
		return notes(req, layers.CodeGrammarSuspected, "Abbrechen"), nil
	}}
	b, _, _ := batcher(t, layer, review.WithConcurrency(1))
	project := uuid.New()
	id, err := b.Start(ctxOf(t), request(project, domain.LinguisticScope{Locales: []string{"de"}}))
	if err != nil {
		t.Fatal(err)
	}
	p := settle(t, b, project, id)
	if p.Reviewed != 2 {
		t.Errorf("reviewed = %d, want the two that answered", p.Reviewed)
	}
	if len(p.Findings) != 2 {
		t.Errorf("findings = %+v, want two", p.Findings)
	}
}

// ── consent ─────────────────────────────────────────────────────────

// The job refused this already. The seam refuses it again, because
// nothing below may build a request that says a tenant consented when
// it did not (RFC 0003 §7).
func TestWithoutConsentNothingIsSent(t *testing.T) {
	layer := &fakeLayer{}
	b, err := review.New(review.Deps{
		Catalog: catalog(), Localization: translations(), Knowledge: &fakeKnowledge{},
		Intelligence: &fakeIntelligence{consent: false, layer: layer},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Start(ctxOf(t), request(uuid.New(), domain.LinguisticScope{Locales: []string{"de"}}))
	if !errors.Is(err, app.ErrProviderConsent) {
		t.Fatalf("err = %v, want ErrProviderConsent", err)
	}
	if n := len(layer.requests()); n != 0 {
		t.Errorf("%d translations were sent without consent", n)
	}
}

// A tenant with no route is a refusal in Quality's vocabulary, so the
// job fails with `no_route` rather than `internal`.
func TestNoRouteIsReportedAsNoRoute(t *testing.T) {
	b, err := review.New(review.Deps{
		Catalog: catalog(), Localization: translations(), Knowledge: &fakeKnowledge{},
		Intelligence: &fakeIntelligence{consent: true, layerErr: inteldomain.ErrNoRoute},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = b.Start(ctxOf(t), request(uuid.New(), domain.LinguisticScope{Locales: []string{"de"}}))
	if !errors.Is(err, app.ErrNoRoute) {
		t.Fatalf("err = %v, want ErrNoRoute", err)
	}
}

// A scope with nothing translated in it is a finished review that found
// nothing, not one still running — and the skipped count still travels.
func TestAnEmptyScopeFinishesImmediately(t *testing.T) {
	layer := &fakeLayer{}
	b, _, _ := batcher(t, layer)
	project := uuid.New()
	id, err := b.Start(ctxOf(t), request(project,
		domain.LinguisticScope{Locales: []string{"de"}, Keys: []string{"nothing.here"}}))
	if err != nil {
		t.Fatal(err)
	}
	p, err := b.Poll(t.Context(), project, id)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Done || p.Reviewed != 0 || len(p.Findings) != 0 {
		t.Errorf("progress = %+v, want a finished, empty review", p)
	}
	if n := len(layer.requests()); n != 0 {
		t.Errorf("%d reviews ran for an empty scope", n)
	}
}

// A review outlives the request that asked for it: the POST's context
// is done long before the batch is, and cancelling it must not cancel
// the review. Only Cancel does that.
func TestAReviewOutlivesTheRequestThatAskedForIt(t *testing.T) {
	layer := &fakeLayer{answer: func(int, linguistic.Request) (linguistic.Result, error) {
		time.Sleep(5 * time.Millisecond)
		return linguistic.Result{}, nil
	}}
	b, _, _ := batcher(t, layer, review.WithConcurrency(1))
	project := uuid.New()
	ctx, cancel := context.WithCancel(ctxOf(t))
	id, err := b.Start(ctx, request(project, domain.LinguisticScope{Locales: []string{"de"}}))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	p := settle(t, b, project, id)
	if p.Cancelled {
		t.Fatal("the HTTP request going away cancelled the review")
	}
	if p.Reviewed != 3 {
		t.Errorf("reviewed = %d, want all three", p.Reviewed)
	}
}
