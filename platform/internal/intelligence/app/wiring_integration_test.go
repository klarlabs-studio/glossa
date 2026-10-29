//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	intelligencepg "go.klarlabs.de/glossa/platform/internal/intelligence/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
)

func firstPageW() pagination.Page { return pagination.Page{Size: pagination.MaxPageSize} }

func (w *wiring) autoTranslate(t *testing.T, project uuid.UUID, locales ...string) {
	t.Helper()
	if _, err := w.svc.PutProjectSettings(w.admin(), project, app.ProjectSettingsInput{AutoTranslateLocales: &locales}, nil); err != nil {
		t.Fatal(err)
	}
}

func (w *wiring) onlyJob(t *testing.T) app.JobView {
	t.Helper()
	jobs := w.jobs(t)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v, want exactly one", jobs)
	}
	v, err := w.svc.GetJob(w.developer(), jobs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Consent is off by default: the job reaches no provider, fails for
// good and says why; the fill request warns up front.
func TestWiringConsentOffRefusesWithoutProviderCall(t *testing.T) {
	w := newWiring(t, nil)
	w.configure(t, false, 5_000_000)
	p := w.project(t, []string{"de"}, nil)
	w.autoTranslate(t, p, "de")
	w.push(t, p, map[string]string{"cart.save": "Save your changes"})

	if j := w.onlyJob(t); j.State != domain.JobQueued || j.Trigger != domain.TriggerMessageCreated || j.SourceRevision != 1 {
		t.Fatalf("job = %+v", j.Job)
	}
	w.work(t)
	j := w.onlyJob(t)
	if j.State != domain.JobFailed || j.FailureCode != domain.FailureProviderConsent || !strings.Contains(j.LastError, "provider_consent") {
		t.Errorf("job = %+v", j.Job)
	}
	if n := w.factory.calls.Load(); n != 0 {
		t.Errorf("%d provider calls without consent", n)
	}
	if n := wcount(t, "SELECT count(*) FROM intelligence_disclosures") + wcount(t, "SELECT count(*) FROM intelligence_spend"); n != 0 {
		t.Errorf("%d disclosures or spend rows", n)
	}
	res, _, err := w.svc.RequestFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.Warnings, app.WarnProviderConsentOff) || res.Fill.JobsCreated != 1 || res.Counts[domain.JobQueued] != 1 {
		t.Errorf("fill = %+v: the failed job is queued again, with a warning", res)
	}
}

// A sensitive namespace is never queued, a fill skips it, and a job
// queued before the tag fails without reaching a provider.
func TestWiringSensitiveNamespaceIsNeverSent(t *testing.T) {
	w := newWiring(t, nil)
	w.configure(t, true, 5_000_000)
	p := w.project(t, []string{"de"}, nil)
	tags := domain.NamespaceTags{"legal": {domain.TagSensitive}}
	auto := []string{"de"}
	if _, err := w.svc.PutProjectSettings(w.admin(), p, app.ProjectSettingsInput{NamespaceTags: &tags, AutoTranslateLocales: &auto}, nil); err != nil {
		t.Fatal(err)
	}
	legal := "legal"
	res, err := w.catalog.UpsertMessages(w.developer(), catalogdomain.ProjectID(p), []catalogapp.UpsertItem{
		{Key: "terms.body", Namespace: &legal, Text: "You agree to the terms"},
	})
	if err != nil || res[0].Error != nil {
		t.Fatalf("push: %v %+v", err, res)
	}
	w.drain(t)
	if jobs := w.jobs(t); len(jobs) != 0 {
		t.Fatalf("a sensitive message was queued: %+v", jobs)
	}
	fill, _, err := w.svc.RequestFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}}, "")
	if err != nil || fill.Fill.Skipped[domain.FailureSensitive] != 1 || fill.Fill.JobsCreated != 0 {
		t.Fatalf("fill = %+v, %v", fill, err)
	}

	// Tagged after it was queued: the worker refuses it.
	none := domain.NamespaceTags{}
	if _, err := w.svc.PutProjectSettings(w.admin(), p, app.ProjectSettingsInput{NamespaceTags: &none}, nil); err != nil {
		t.Fatal(err)
	}
	if fill, _, err = w.svc.RequestFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}}, ""); err != nil || fill.Fill.JobsCreated != 1 {
		t.Fatalf("fill = %+v, %v", fill, err)
	}
	if _, err := w.svc.PutProjectSettings(w.admin(), p, app.ProjectSettingsInput{NamespaceTags: &tags}, nil); err != nil {
		t.Fatal(err)
	}
	w.work(t)
	if j := w.onlyJob(t); j.State != domain.JobFailed || j.FailureCode != domain.FailureSensitive {
		t.Errorf("job = %+v", j.Job)
	}
	if n := w.factory.calls.Load(); n != 0 {
		t.Errorf("%d provider calls for a sensitive message", n)
	}
}

// A new message in an auto-translate locale becomes a job, the job a
// suggestion routed by its confidence, with its provider disclosure,
// spend and audit; accepting it writes a revision with origin ai and the
// full origin_detail.
func TestWiringMessageCreatedToSuggestionAndAccept(t *testing.T) {
	w := newWiring(t, map[domain.Task][]answer{
		domain.TaskTranslate: {{text: draft("Speichere deine Änderungen")}},
		domain.TaskAssess:    {{text: assessment(0.9, true)}},
	})
	w.configure(t, true, 5_000_000)
	p := w.project(t, []string{"de"}, map[string]string{"cart.cancel": "Cancel"})
	w.autoTranslate(t, p, "de")
	w.push(t, p, map[string]string{"cart.save": "Save your changes"})
	if ran := w.work(t); ran != 1 {
		t.Fatalf("ran %d jobs", ran)
	}

	j := w.onlyJob(t)
	if j.State != domain.JobSucceeded || j.SuggestionID == nil || len(j.Audit) == 0 {
		t.Fatalf("job = %+v", j.Job)
	}
	s, err := w.svc.GetSuggestion(w.developer(), *j.SuggestionID)
	if err != nil {
		t.Fatal(err)
	}
	pv := s.Provenance
	if s.Status != domain.StatusPending || s.Message != "Speichere deine Änderungen" || pv.Origin != domain.OriginAI ||
		pv.Provider != "anthropic" || pv.Model != app.DefaultTranslateModel || pv.PromptVersion != "translate/v1" ||
		len(s.Confidence.Explanation) == 0 || s.Cost == 0 || s.MessageKey != "cart.save" {
		t.Fatalf("suggestion = %+v", s)
	}
	if s.Action != domain.ActionApproveRecommended && s.Action != domain.ActionReviewRequired {
		t.Errorf("action = %s", s.Action)
	}
	if calls := w.factory.calls.Load(); calls != 2 || !slices.Equal(w.factory.keys, []string{"sk-test-key"}) {
		t.Errorf("calls = %d, keys %v: the sealed key is opened for the call", calls, w.factory.keys)
	}
	ds, _, err := w.svc.ListDisclosures(w.developer(), app.DisclosureFilter{JobID: &j.ID}, firstPageW())
	if err != nil || len(ds) != 2 || ds[0].MessageID != s.MessageID || len(ds[0].Sent) == 0 {
		t.Errorf("disclosures = %+v, %v", ds, err)
	}
	budget, err := w.svc.GetBudget(w.developer())
	if err != nil || budget.Calls != 2 || budget.Spent != s.Cost || budget.Remaining() != 5_000_000-s.Cost {
		t.Errorf("budget = %+v, %v (suggestion cost %v)", budget, err, s.Cost)
	}
	queue, _, err := w.svc.ReviewQueue(w.developer(), p, nil, firstPageW())
	if err != nil || len(queue) != 1 || queue[0].ID != s.ID {
		t.Errorf("queue = %+v, %v", queue, err)
	}

	// A translator limited to fr may not decide on de.
	if _, err := w.svc.AcceptSuggestion(w.as([]string{"translator"}, "fr"), s.ID, app.AcceptInput{}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("out-of-scope accept: %v", err)
	}
	accepted, err := w.svc.AcceptSuggestion(w.reviewer(), s.ID, app.AcceptInput{})
	if err != nil || accepted.Status != domain.StatusAccepted || accepted.TranslationRevision == nil {
		t.Fatalf("accept = %+v, %v", accepted, err)
	}
	if _, err := w.svc.AcceptSuggestion(w.reviewer(), s.ID, app.AcceptInput{}); !errors.Is(err, domain.ErrSuggestionDecided) {
		t.Errorf("accepting twice: %v", err)
	}
	revs, _, err := w.localization.TranslationRevisions(w.reviewer(), p, "cart.save", "de", firstPageW())
	if err != nil || len(revs) != 1 {
		t.Fatalf("revisions = %+v, %v", revs, err)
	}
	rev := revs[0]
	var detail map[string]any
	_ = json.Unmarshal(rev.Provenance.Detail, &detail)
	if rev.Provenance.Origin != "ai" || rev.State != "approved" || detail["provider"] != "anthropic" ||
		detail["model"] != app.DefaultTranslateModel || detail["prompt_version"] != "translate/v1" ||
		detail["suggestion_id"] != s.ID.String() || detail["score"] == nil || detail["explanation"] == nil {
		t.Errorf("revision = %+v, detail %v", rev, detail)
	}
	m, _, err := w.svc.AcceptanceMetrics(w.developer(), p, time.Time{})
	if err != nil || len(m) != 1 || m[0].Accepted != 1 || m[0].AcceptanceRate != 1 {
		t.Errorf("metrics = %+v, %v", m, err)
	}
}

// Edited acceptance records a structured diff; rejection writes nothing;
// the metrics count both.
func TestWiringEditAcceptAndReject(t *testing.T) {
	w := newWiring(t, map[domain.Task][]answer{
		domain.TaskTranslate: {{text: draft("Speichere deine Änderungen")}, {text: draft("Löschen")}},
		domain.TaskAssess:    {{text: assessment(0.8, true)}, {text: assessment(0.8, true)}},
	})
	w.configure(t, true, 5_000_000)
	p := w.project(t, []string{"de"}, map[string]string{"cart.save": "Save your changes", "cart.delete": "Delete"})
	fill, _, err := w.svc.RequestFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}, Filter: app.FillFilter{Keys: []string{"cart.save"}}}, "")
	if err != nil || fill.Fill.JobsCreated != 1 {
		t.Fatalf("fill = %+v, %v", fill, err)
	}
	w.work(t)
	if _, _, err := w.svc.RequestFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}, Filter: app.FillFilter{Keys: []string{"cart.delete"}}}, ""); err != nil {
		t.Fatal(err)
	}
	w.work(t)
	list, _, err := w.svc.ListSuggestions(w.developer(), app.SuggestionFilter{ProjectID: &p}, firstPageW())
	if err != nil || len(list) != 2 {
		t.Fatalf("suggestions = %+v, %v", list, err)
	}
	byKey := map[string]domain.SuggestionRecord{}
	for _, s := range list {
		byKey[s.MessageKey] = s
	}
	edited, err := w.svc.AcceptSuggestion(w.reviewer(), byKey["cart.save"].ID, app.AcceptInput{Text: "Änderungen speichern"})
	if err != nil {
		t.Fatal(err)
	}
	if d := edited.Decision; d == nil || d.Edit == nil || d.Edit.Distance == 0 || d.Edit.Ratio <= 0 {
		t.Errorf("decision = %+v", edited.Decision)
	}
	tr, err := w.localization.GetTranslation(w.reviewer(), p, "cart.save", "de")
	if err != nil || tr.Content.Text != "Änderungen speichern" || tr.Origin != "ai" {
		t.Errorf("translation = %+v, %v", tr, err)
	}
	rejected, err := w.svc.RejectSuggestion(w.reviewer(), byKey["cart.delete"].ID, "too terse", nil)
	if err != nil || rejected.Status != domain.StatusRejected || rejected.Decision.Reason != "too terse" {
		t.Errorf("reject = %+v, %v", rejected, err)
	}
	if _, err := w.localization.GetTranslation(w.reviewer(), p, "cart.delete", "de"); !errors.Is(err, localizationapp.ErrNotFound) {
		t.Errorf("a rejected suggestion writes nothing: %v", err)
	}
	m, _, err := w.svc.AcceptanceMetrics(w.developer(), p, time.Time{})
	if err != nil || len(m) != 1 || m[0].Accepted != 1 || m[0].Edited != 1 || m[0].Rejected != 1 || m[0].AcceptanceRate != 0.5 || m[0].MeanEditDistance == 0 {
		t.Errorf("metrics = %+v, %v", m, err)
	}
}

// An exact translation-memory match is reused without a model call —
// even without consent or budget — with provenance translation_memory;
// with auto_approve for environments that ship approved text it becomes
// an approved revision on its own.
func TestWiringExactTMHitNeedsNoProvider(t *testing.T) {
	w := newWiring(t, nil)
	w.configure(t, false, 0)
	p := w.project(t, []string{"de"}, map[string]string{"cart.save": "Save your changes", "order.save": "Save your changes"})
	if _, _, err := w.localization.PutTranslation(w.reviewer(), p, "cart.save", "de", localizationapp.TranslationInput{Text: "Änderungen speichern"}, nil); err != nil {
		t.Fatal(err)
	}
	w.drain(t) // Knowledge derives the TM unit
	review := domain.DefaultReviewSettings()
	review.AutoApprove, review.AutoApproveEnvironments = true, []string{"production"}
	if _, err := w.svc.PutProjectSettings(w.admin(), p, app.ProjectSettingsInput{Review: &review}, nil); err != nil {
		t.Fatal(err)
	}
	fill, _, err := w.svc.RequestFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}}, "")
	if err != nil || fill.Fill.JobsCreated != 1 {
		t.Fatalf("fill = %+v, %v", fill, err)
	}
	w.work(t)
	j := w.onlyJob(t)
	if j.State != domain.JobSucceeded || j.MessageKey != "order.save" {
		t.Fatalf("job = %+v", j.Job)
	}
	s, err := w.svc.GetSuggestion(w.developer(), *j.SuggestionID)
	if err != nil {
		t.Fatal(err)
	}
	if s.Provenance.Origin != domain.OriginTranslationMemory || len(s.Provenance.TMUnitIDs) != 1 || s.Cost != 0 ||
		s.Action != domain.ActionAutoApprove || s.Status != domain.StatusAutoApplied || s.TranslationRevision == nil {
		t.Errorf("suggestion = %+v", s)
	}
	if n := w.factory.calls.Load(); n != 0 {
		t.Errorf("%d provider calls for an exact TM hit", n)
	}
	tr, err := w.localization.GetTranslation(w.reviewer(), p, "order.save", "de")
	if err != nil || tr.Origin != "translation_memory" || tr.State != "approved" || tr.Content.Text != "Änderungen speichern" {
		t.Errorf("translation = %+v, %v", tr, err)
	}

	// Without an environment that ships approved text, auto_approve is
	// refused up front.
	review.AutoApproveEnvironments = []string{"preview-only"}
	if _, err := w.svc.PutProjectSettings(w.admin(), p, app.ProjectSettingsInput{Review: &review}, nil); !errors.Is(err, app.ErrAutoApproveIneligible) {
		t.Errorf("ineligible auto_approve: %v", err)
	}
}

// A tenant at its budget can't make a provider call: the job fails for
// good (budget_exceeded) before anything is sent.
func TestWiringBudgetExhaustedIsAHardStop(t *testing.T) {
	w := newWiring(t, nil)
	w.configure(t, true, 1_000) // 0.001 USD: less than one call may cost
	p := w.project(t, []string{"de"}, map[string]string{"cart.save": "Save your changes"})
	if _, _, err := w.svc.RequestFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}}, ""); err != nil {
		t.Fatal(err)
	}
	w.work(t)
	j := w.onlyJob(t)
	if j.State != domain.JobFailed || j.FailureCode != domain.FailureBudgetExceeded {
		t.Errorf("job = %+v", j.Job)
	}
	if n := w.factory.calls.Load(); n != 0 {
		t.Errorf("%d provider calls over budget", n)
	}
}

// A provider outage is transient: the job goes back to the queue with
// backoff and succeeds on the next attempt; the provider that saw the
// text the first time is disclosed too.
func TestWiringTransientProviderErrorIsRetried(t *testing.T) {
	w := newWiring(t, map[domain.Task][]answer{
		domain.TaskTranslate: {
			{err: &domain.ProviderError{Provider: "anthropic", Kind: domain.KindUnavailable, Status: 529}},
			{text: draft("Zur Kasse")},
		},
		domain.TaskAssess: {{text: assessment(0.9, true)}},
	})
	w.configure(t, true, 5_000_000)
	p := w.project(t, []string{"de"}, map[string]string{"cart.checkout": "Check out"})
	if _, _, err := w.svc.RequestFill(w.developer(), p, app.FillRequest{Locales: []string{"de"}}, ""); err != nil {
		t.Fatal(err)
	}
	w.work(t)
	j := w.onlyJob(t)
	if j.State != domain.JobQueued || j.Attempts != 1 || j.FailureCode != domain.FailureProviderError || !j.AvailableAt.After(time.Now()) {
		t.Fatalf("after the outage: %+v", j.Job)
	}
	if _, err := wenv.Super.Exec(context.Background(), "UPDATE intelligence_jobs SET available_at = now()"); err != nil {
		t.Fatal(err)
	}
	w.work(t)
	j = w.onlyJob(t)
	if j.State != domain.JobSucceeded || j.Attempts != 2 || j.SuggestionID == nil {
		t.Errorf("after the retry: %+v", j.Job)
	}
	if n := wcount(t, "SELECT count(*) FROM intelligence_disclosures"); n != 3 {
		t.Errorf("disclosures = %d, want the failed call, the draft and the assessment", n)
	}
}

// Duplicate events and repeated fills find the existing job; a source
// revision that outdates a translation queues the next one.
func TestWiringTriggersAreIdempotent(t *testing.T) {
	w := newWiring(t, nil)
	w.configure(t, false, 0)
	p := w.project(t, []string{"de"}, nil)
	w.autoTranslate(t, p, "de")
	w.push(t, p, map[string]string{"cart.save": "Save your changes"})
	if _, err := wenv.Super.Exec(context.Background(), `INSERT INTO outbox_events (id, tenant_id, event_type, aggregate_type, aggregate_id, actor, payload, occurred_at)
		SELECT gen_random_uuid(), tenant_id, event_type, aggregate_type, aggregate_id, actor, payload, occurred_at
		FROM outbox_events WHERE event_type = 'catalog.message.created'`); err != nil {
		t.Fatal(err)
	}
	w.drain(t)
	if jobs := w.jobs(t); len(jobs) != 1 {
		t.Fatalf("a duplicate event queued %d jobs", len(jobs))
	}
	dev := w.developer() // Idempotency-Keys are scoped to the caller
	first, replayed, err := w.svc.RequestFill(dev, p, app.FillRequest{Locales: []string{"de"}}, "fill-1")
	if err != nil || replayed || first.Fill.JobsCreated != 0 || first.Fill.JobsExisting != 1 {
		t.Fatalf("fill = %+v, %t, %v", first, replayed, err)
	}
	again, replayed, err := w.svc.RequestFill(dev, p, app.FillRequest{Locales: []string{"de"}}, "fill-1")
	if err != nil || !replayed || again.Fill.ID != first.Fill.ID {
		t.Errorf("replay = %+v, %t, %v", again, replayed, err)
	}

	// Translated, then the source moves on: the outdated event queues a
	// job for the new revision.
	if _, _, err := w.localization.PutTranslation(w.reviewer(), p, "cart.save", "de", localizationapp.TranslationInput{Text: "Speichern"}, nil); err != nil {
		t.Fatal(err)
	}
	w.drain(t)
	m, err := w.catalog.GetMessage(w.developer(), catalogdomain.ProjectID(p), "cart.save")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.catalog.ReviseSource(w.developer(), catalogdomain.ProjectID(p), "cart.save", m.Version, "Save all your changes", "mf1"); err != nil {
		t.Fatal(err)
	}
	w.drain(t)
	jobs := w.jobs(t)
	if len(jobs) != 2 || jobs[0].Trigger != domain.TriggerTranslationOutdated || jobs[0].SourceRevision != 2 {
		t.Errorf("jobs = %+v", jobs)
	}
	// The stale job of revision 1 is skipped, not translated.
	w.work(t)
	for _, j := range w.jobs(t) {
		if j.SourceRevision == 1 && (j.State != domain.JobSkipped || j.FailureCode != domain.SkipSuperseded) {
			t.Errorf("stale job = %+v", j)
		}
	}
}

// The review queue lists pending suggestions by risk: lowest score
// first, then the most risk tags — across pages.
func TestWiringReviewQueueOrdersByRisk(t *testing.T) {
	w := newWiring(t, nil)
	p := w.project(t, []string{"de", "fr"}, nil)
	tx := intelligencepg.NewTransactor(db.NewUnitOfWork(wenv.App))
	insert := func(score float64, locale string, status domain.SuggestionStatus, risk ...string) uuid.UUID {
		id := uuid.Must(uuid.NewV7())
		model, _, _ := domain.ParseMessage("x")
		r := domain.SuggestionRecord{
			ID: id, JobID: uuid.New(), ProjectID: p, MessageID: uuid.New(), MessageKey: "k." + id.String()[:8], Namespace: "default",
			Locale: locale, SourceRevision: 1, RiskTags: risk, Status: status, Version: 1, CreatedAt: time.Now(),
			Suggestion: domain.Suggestion{Message: "x", Model: model, Provenance: domain.Provenance{Origin: domain.OriginAI},
				Confidence: domain.Confidence{Score: score}, Action: domain.ActionReviewRequired},
		}
		if err := tx.InTenant(w.developer(), func(ctx context.Context, st app.Store) error { return st.InsertSuggestion(ctx, r) }); err != nil {
			t.Fatal(err)
		}
		return id
	}
	calm := insert(0.9, "de", domain.StatusPending)
	legal := insert(0.4, "de", domain.StatusPending, domain.TagLegal)
	worst := insert(0.2, "fr", domain.StatusPending)
	legalForbidden := insert(0.4, "de", domain.StatusPending, domain.TagLegal, domain.FactorTermForbidden)
	insert(0.1, "de", domain.StatusAccepted)

	var got []uuid.UUID
	page := pagination.Page{Size: 3}
	for {
		items, next, err := w.svc.ReviewQueue(w.developer(), p, nil, page)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range items {
			got = append(got, s.ID)
		}
		if next == nil {
			break
		}
		if page, err = pagination.Parse(&page.Size, next); err != nil {
			t.Fatal(err)
		}
	}
	if want := []uuid.UUID{worst, legalForbidden, legal, calm}; !slices.Equal(got, want) {
		t.Errorf("queue = %v, want %v", got, want)
	}
	de, _, _ := w.svc.ReviewQueue(w.developer(), p, []string{"de"}, firstPageW())
	if len(de) != 3 {
		t.Errorf("de queue = %d", len(de))
	}
	// Another tenant sees nothing (row-level security).
	other, err := wenv.SeedTenant(context.Background(), "globex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.GetSuggestion(authztest.Member(context.Background(), other, []string{"owner"}), calm); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("another tenant read a suggestion: %v", err)
	}
}

// Provider keys are sealed at rest and never come back; configuration
// needs intelligence.manage.
func TestWiringProviderConfiguration(t *testing.T) {
	w := newWiring(t, nil)
	ctx := w.admin()
	p, _, err := w.svc.CreateProvider(ctx, app.ProviderInput{Name: "anthropic", Kind: "anthropic", APIKey: "sk-secret-value"}, "k1")
	if err != nil || !p.HasKey || !p.Enabled {
		t.Fatalf("create = %+v, %v", p, err)
	}
	if _, replayed, err := w.svc.CreateProvider(ctx, app.ProviderInput{Name: "anthropic", Kind: "anthropic", APIKey: "sk-secret-value"}, "k1"); err != nil || !replayed {
		t.Errorf("replay: %t %v", replayed, err)
	}
	if _, _, err := w.svc.CreateProvider(ctx, app.ProviderInput{Name: "anthropic", Kind: "gemini", APIKey: "x"}, ""); !errors.Is(err, app.ErrProviderNameTaken) {
		t.Errorf("duplicate name: %v", err)
	}
	if _, _, err := w.svc.CreateProvider(w.developer(), app.ProviderInput{Name: "x", Kind: "gemini", APIKey: "x"}, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("developers don't configure providers: %v", err)
	}
	if n := wcount(t, "SELECT count(*) FROM intelligence_providers WHERE position('sk-secret-value' in encode(api_key_sealed, 'escape')) > 0"); n != 0 {
		t.Error("the key is stored in the clear")
	}
	policy := domain.RoutingPolicy{Rules: []domain.RoutingRule{{Task: domain.TaskTranslate, Routes: []domain.Route{{Provider: "anthropic", Model: "claude-sonnet-5", MaxTokens: 4000}}}}}
	if _, err := w.svc.PutRoutingPolicy(ctx, nil, policy, nil); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.DeleteProvider(ctx, p.ID); !errors.Is(err, app.ErrProviderInUse) {
		t.Errorf("deleting a routed provider: %v", err)
	}
	v := p.Version
	cleared, err := w.svc.UpdateProvider(ctx, p.ID, &v, app.ProviderPatch{ClearAPIKey: true, Models: &[]string{"claude-sonnet-5"}})
	if err != nil || cleared.HasKey || cleared.Version != 2 {
		t.Errorf("update = %+v, %v", cleared, err)
	}
	if _, err := w.svc.UpdateProvider(ctx, p.ID, &v, app.ProviderPatch{}); !errors.Is(err, app.ErrPreconditionFailed) {
		t.Errorf("stale If-Match: %v", err)
	}
	view, err := w.svc.GetRoutingPolicy(w.developer(), nil)
	if err != nil || view.Source != "tenant" || view.Record.Version != 1 {
		t.Errorf("routing = %+v, %v", view, err)
	}
}
