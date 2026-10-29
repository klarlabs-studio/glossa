//go:build integration

package app_test

import (
	"context"
	"errors"
	identitypg "go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	localizationidentity "go.klarlabs.de/glossa/platform/internal/localization/adapters/identity"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	catalogpg "go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres"
	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	catalogport "go.klarlabs.de/glossa/platform/internal/localization/adapters/catalog"
	localizationpg "go.klarlabs.de/glossa/platform/internal/localization/adapters/postgres"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	qualitycatalog "go.klarlabs.de/glossa/platform/internal/quality/adapters/catalog"
	qualitypg "go.klarlabs.de/glossa/platform/internal/quality/adapters/postgres"
	qualityapp "go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/identity"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/sources"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// The instance runner end to end on Postgres: Catalog, Localization and
// Quality are the real services, events travel through the real outbox,
// and instances are migration 0043's tables under forced RLS. Only the
// actor resolution (Identity) and assignments (a parallel slice) are
// stand-ins.

type runHarness struct {
	deps         app.RunnerDeps
	tenant       tenancy.ID
	catalog      *catalogapp.Service
	localization *localizationapp.Service
	defs         *app.Service
	runner       *app.Runner
	instances    *postgres.Instances
	dispatcher   *outbox.Dispatcher
	actors       *fakeActors
	assignments  *fakeAssignments
	findings     *sources.Findings
	// releases and subscribers are what a test wiring Release adds: the
	// runner's port onto release requests, and Release's own outbox
	// subscriptions on the same dispatcher.
	releases    app.ReleaseRequests
	subscribers []func(*outbox.Registry) error
	mu          sync.Mutex
	clock       time.Time
}

// wire builds the runner with assignments and subscribes it, with
// Localization, to a dispatcher the test drives.
func (h *runHarness) wire(t *testing.T, assignments app.AssignmentsPort) {
	t.Helper()
	uow := db.NewUnitOfWork(env.App)
	h.deps = app.RunnerDeps{
		Tx: h.instances, Definitions: h.defs, Timers: h.instances,
		Translations: sources.NewTranslations(h.catalog, h.localization),
		Findings:     h.findings,
		Assignments:  assignments, Actors: h.actors, Releases: h.releases,
		Now: func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.clock },
	}
	h.runner = app.NewRunner(h.deps)
	reg := outbox.NewRegistry()
	if err := h.localization.Subscribe(reg); err != nil {
		t.Fatal(err)
	}
	for _, sub := range h.subscribers {
		if err := sub(reg); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.runner.Subscribe(reg); err != nil {
		t.Fatal(err)
	}
	var err error
	h.dispatcher, err = outbox.NewDispatcher(outbox.NewPostgresStore(uow), reg, outbox.DispatcherConfig{
		BatchSize: 100, MaxAttempts: 3, PollInterval: 10 * time.Millisecond, Lease: time.Minute,
		HandlerTimeout: 10 * time.Second, InlineAttempts: 1, InlineBackoff: time.Millisecond,
	}, outbox.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
}

// useWork replaces the stand-in assignments with a real WorkService.
func (h *runHarness) useWork(t *testing.T, work *app.WorkService) { h.wire(t, work) }

func newRunHarness(t *testing.T) *runHarness {
	t.Helper()
	if err := env.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	return runHarnessFor(t, "acme")
}

func runHarnessFor(t *testing.T, slug string) *runHarness {
	t.Helper()
	tenant, err := env.SeedTenant(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	h := &runHarness{tenant: tenant, clock: time.Now().UTC(),
		actors: &fakeActors{principals: map[outbox.Actor]authz.Principal{}}, assignments: &fakeAssignments{}}
	h.catalog = catalogapp.New(catalogpg.NewTransactor(uow))
	h.localization = localizationapp.New(localizationpg.NewTransactor(uow), catalogport.New(h.catalog),
		localizationapp.WithReviewers(localizationidentity.NewReviewers(identitypg.NewTransactor(uow, nil))))
	quality := qualityapp.NewService(qualitypg.NewTransactor(uow), qualitycatalog.New(h.catalog))
	h.defs = app.New(postgres.NewTransactor(uow), identity.Permissions{})
	h.instances = postgres.NewInstances(uow)
	h.findings = sources.NewFindings(quality)
	h.wire(t, h.assignments)
	return h
}

func (h *runHarness) ctx() context.Context {
	return tenancy.ContextWithTenant(context.Background(), h.tenant)
}

// as is a member of the tenant, registered with the actor resolver so
// the runner gives their events' actions their grant.
func (h *runHarness) as(roles []string, locales ...string) (context.Context, outbox.Actor) {
	ctx := authztest.Member(context.Background(), h.tenant, roles, locales...)
	p, _ := authz.From(ctx)
	a := outbox.Actor(p.Actor.String())
	h.actors.principals[a] = p
	return ctx, a
}

func (h *runHarness) manager(t *testing.T) context.Context {
	t.Helper()
	ctx, err := authz.Background(h.ctx(), "test.manager", app.PermWorkflowsManage, app.PermWorkflowsRead)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func (h *runHarness) drain(t *testing.T) {
	t.Helper()
	for range 50 {
		n, err := h.dispatcher.ProcessBatch(context.Background())
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if n == 0 {
			var dead int
			var last string
			_ = env.Super.QueryRow(context.Background(),
				"SELECT count(*), coalesce(max(last_error), '') FROM outbox_events WHERE status = 'dead'").Scan(&dead, &last)
			if dead > 0 {
				t.Fatalf("%d events dead-lettered: %s", dead, last)
			}
			return
		}
	}
	t.Fatal("the outbox did not drain")
}

// project creates a project needing review with a de locale and one
// message, and returns it and the message id.
func (h *runHarness) project(t *testing.T) (uuid.UUID, uuid.UUID) {
	t.Helper()
	dev, _ := h.as([]string{"developer"})
	settings := catalogdomain.Settings{DefaultSyntax: mfcontent.MF1, ReviewRequired: true}
	p, _, err := h.catalog.CreateProject(dev, catalogapp.NewProject{Slug: "shop", Name: "Shop", SourceLocale: "en", Settings: &settings}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.localization.AddLocale(dev, p.ID.UUID(), "de"); err != nil {
		t.Fatal(err)
	}
	h.source(t, p.ID.UUID(), "Welcome")
	m, err := h.catalog.MessagesByKeys(dev, p.ID, []string{"home.title"})
	if err != nil {
		t.Fatal(err)
	}
	return p.ID.UUID(), uuid.UUID(m["home.title"].ID)
}

func (h *runHarness) source(t *testing.T, project uuid.UUID, text string) {
	t.Helper()
	dev, _ := h.as([]string{"developer"})
	res, err := h.catalog.UpsertMessages(dev, catalogdomain.ProjectID(project), []catalogapp.UpsertItem{{Key: "home.title", Text: text}})
	if err != nil || res[0].Error != nil {
		t.Fatalf("push: %v %+v", err, res)
	}
	h.drain(t)
}

// bindDefault binds the tenant's seeded default definition.
func (h *runHarness) bindDefault(t *testing.T, project uuid.UUID) {
	t.Helper()
	if err := h.runner.EnsureDefault(h.ctx()); err != nil {
		t.Fatal(err)
	}
	defs, err := h.defs.Definitions(h.manager(t), project)
	if err != nil || len(defs) != 1 || defs[0].Name != "review" {
		t.Fatalf("seeded definitions = %+v (%v)", defs, err)
	}
	if _, err := h.defs.Bind(h.manager(t), app.NewBinding{ProjectID: project, DefinitionID: defs[0].ID}); err != nil {
		t.Fatal(err)
	}
}

func (h *runHarness) bindDoc(t *testing.T, project uuid.UUID, doc string) {
	t.Helper()
	saved, err := h.defs.CreateDefinition(h.manager(t), app.NewDefinition{Document: []byte(doc)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.defs.Bind(h.manager(t), app.NewBinding{ProjectID: project, DefinitionID: saved.Definition.ID}); err != nil {
		t.Fatal(err)
	}
}

func (h *runHarness) list(t *testing.T, project uuid.UUID) []app.InstanceView {
	t.Helper()
	got, _, err := h.instances.ListInstances(h.ctx(), app.InstanceFilter{Project: project})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (h *runHarness) log(t *testing.T, id uuid.UUID) []app.TransitionView {
	t.Helper()
	got, err := h.instances.ListTransitions(h.ctx(), id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (h *runHarness) translate(t *testing.T, ctx context.Context, project uuid.UUID, text string) localizationapp.TranslationView {
	t.Helper()
	var ifMatch *int
	if cur, err := h.localization.GetTranslation(ctx, project, "home.title", "de"); err == nil {
		ifMatch = &cur.Revision
	}
	v, _, err := h.localization.PutTranslation(ctx, project, "home.title", "de", localizationapp.TranslationInput{Text: text}, ifMatch)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestTheRunnerFromRevisionToReview(t *testing.T) {
	h := newRunHarness(t)
	project, _ := h.project(t)
	h.bindDoc(t, project, waitForReview)

	translator, translatorActor := h.as([]string{"translator"}, "de")
	tr := h.translate(t, translator, project, "Willkommen")
	if tr.State != "needs_review" {
		t.Fatalf("the project needs review, the translation is %s", tr.State)
	}
	h.drain(t)
	got := h.list(t, project)
	if len(got) != 1 || got[0].State != "awaiting_review" || got[0].Status != app.InstanceActive || got[0].Locale != "de" {
		t.Fatalf("after the revision: %+v", got)
	}
	first := got[0]

	reviewer, reviewerActor := h.as([]string{"reviewer"}, "de")
	if _, err := h.localization.ReviewTranslation(reviewer, project, "home.title", "de", "approved", tr.Revision); err != nil {
		t.Fatal(err)
	}
	h.drain(t)
	done, err := h.instances.GetInstance(h.ctx(), first.ID)
	if err != nil || done.Status != app.InstanceFinished || done.State != "reviewed" {
		t.Fatalf("after the review: %+v (%v)", done, err)
	}
	log := h.log(t, first.ID)
	if len(log) != 2 {
		t.Fatalf("log = %+v", log)
	}
	if log[0].Seq != 1 || log[0].From != "" || log[0].To != "awaiting_review" || log[0].Event != "translation.revised" ||
		log[0].Actor != translatorActor.String() || log[0].Outcome != app.TransitionApplied {
		t.Errorf("first row = %+v", log[0])
	}
	if log[1].Seq != 2 || log[1].To != "reviewed" || log[1].Actor != reviewerActor.String() || log[1].Outcome != app.TransitionApplied {
		t.Errorf("second row = %+v", log[1])
	}
	var snapshot []byte
	if err := env.Super.QueryRow(context.Background(), "SELECT snapshot FROM workflow_instances WHERE id = $1", first.ID).Scan(&snapshot); err != nil || snapshot != nil {
		t.Errorf("a finished instance kept its snapshot %s (%v)", snapshot, err)
	}

	// A source change leaves the translation outdated: work again, and
	// a new instance, the finished one's log untouched.
	h.source(t, project, "Welcome!")
	got = h.list(t, project)
	if len(got) != 2 || got[0].Status != app.InstanceActive || got[0].ID == first.ID {
		t.Fatalf("after the source change: %+v", got)
	}
	if again := h.log(t, got[0].ID); len(again) != 1 || again[0].Event != "translation.outdated" {
		t.Errorf("new instance's log = %+v", again)
	}
	if len(h.log(t, first.ID)) != 2 {
		t.Error("the finished instance's log changed")
	}
}

func TestTheRunnerIsIdempotentOnTheOutboxEvent(t *testing.T) {
	h := newRunHarness(t)
	project, _ := h.project(t)
	h.bindDoc(t, project, waitForReview)
	translator, _ := h.as([]string{"translator"}, "de")
	h.translate(t, translator, project, "Willkommen")
	h.drain(t)

	// Deliver the revision again, exactly as the outbox recorded it.
	var (
		id      uuid.UUID
		actor   string
		payload []byte
	)
	if err := env.Super.QueryRow(context.Background(), `SELECT id, actor, payload FROM outbox_events
		WHERE event_type = 'localization.translation.revised'`).Scan(&id, &actor, &payload); err != nil {
		t.Fatal(err)
	}
	ev, err := app.EventOf(outbox.Delivery{EventID: id, TenantID: h.tenant, Type: "localization.translation.revised",
		Actor: outbox.Actor(actor), Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := h.runner.Handle(h.ctx(), ev); err != nil {
			t.Fatal(err)
		}
	}
	got := h.list(t, project)
	if len(got) != 1 || len(h.log(t, got[0].ID)) != 1 {
		t.Fatalf("a replayed event stepped again: %+v", got)
	}
}

func TestConcurrentEventsOnOneSubjectSerializeOnPostgres(t *testing.T) {
	h := newRunHarness(t)
	project, message := h.project(t)
	h.bindDoc(t, project, waitForReview)
	_, translator := h.as([]string{"translator"}, "de")
	unit := app.SubjectRef{Kind: domain.SubjectTranslation, Project: project, ID: message, Locale: "de"}

	// Two runners on one database, as two replicas: each steps one
	// event at a time, so what keeps the subject's eight events in one
	// instance and one log is the database — the instance row lock and
	// the unique active index — and not anything in-process.
	replicas := []*app.Runner{h.runner, app.NewRunner(h.deps)}
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- replicas[i%len(replicas)].Handle(h.ctx(), app.Event{ID: uuid.New(), Name: domain.EventTranslationRevised,
				Actor: translator, Subjects: []app.SubjectRef{unit}})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got := h.list(t, project)
	if len(got) != 1 {
		t.Fatalf("%d first triggers made %d instances", n, len(got))
	}
	log := h.log(t, got[0].ID)
	if len(log) != n {
		t.Fatalf("%d events, %d transitions", n, len(log))
	}
	for i, r := range log {
		if r.Seq != int64(i+1) {
			t.Errorf("row %d has seq %d", i, r.Seq)
		}
	}
}

func TestASystemApprovalIsRefusedByLocalization(t *testing.T) {
	h := newRunHarness(t)
	project, message := h.project(t)
	h.bindDoc(t, project, approveOnGrant)
	translator, _ := h.as([]string{"translator"}, "de")
	h.translate(t, translator, project, "Willkommen")
	h.drain(t)
	unit := app.SubjectRef{Kind: domain.SubjectTranslation, Project: project, ID: message, Locale: "de"}
	send := func(actor outbox.Actor) {
		t.Helper()
		if err := h.runner.Handle(h.ctx(), app.Event{ID: uuid.New(), Name: domain.EventApprovalGranted,
			Actor: actor, Subjects: []app.SubjectRef{unit}}); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string {
		v, err := h.localization.GetTranslation(translator, project, "home.title", "de")
		if err != nil {
			t.Fatal(err)
		}
		return string(v.State)
	}

	send(authz.SystemEventActor("some.background.job"))
	send(outbox.ActorUnknown)
	inst := h.list(t, project)[0]
	log := h.log(t, inst.ID)
	if inst.State != "waiting" || read() != "needs_review" {
		t.Fatalf("a system approval moved something: instance %s, translation %s", inst.State, read())
	}
	for _, r := range log[1:] {
		if r.Outcome != app.TransitionRefused || r.To != "waiting" || r.Actions[0].Outcome != app.ActionRefused {
			t.Errorf("row %+v", r)
		}
	}

	_, reviewer := h.as([]string{"reviewer"}, "de")
	send(reviewer)
	if inst, _ := h.instances.GetInstance(h.ctx(), inst.ID); inst.Status != app.InstanceFinished || read() != "approved" {
		t.Fatalf("a reviewer's approval: %+v, translation %s", inst, read())
	}
	revs, _, err := h.localization.TranslationRevisions(translator, project, "home.title", "de", pageOf(1))
	if err != nil || revs[0].Provenance.By != reviewer.String() {
		t.Fatalf("approved by %+v (%v), want the reviewer %s", revs, err, reviewer)
	}
}

func TestTimersAreRaisedBySweep(t *testing.T) {
	h := newRunHarness(t)
	project, _ := h.project(t)
	h.bindDoc(t, project, assignWithDue)
	translator, _ := h.as([]string{"translator"}, "de")
	h.translate(t, translator, project, "Willkommen")
	h.drain(t)
	inst := h.list(t, project)[0]
	if inst.State != "translating" || len(h.assignments.assigned) != 1 {
		t.Fatalf("after the start: %+v, %d assignments", inst, len(h.assignments.assigned))
	}
	if n, err := h.runner.SweepTimers(context.Background()); err != nil || n != 0 {
		t.Fatalf("swept %d early (%v)", n, err)
	}
	h.mu.Lock()
	h.clock = h.clock.Add(25 * time.Hour)
	h.mu.Unlock()
	if n, err := h.runner.SweepTimers(context.Background()); err != nil || n != 1 {
		t.Fatalf("swept %d (%v)", n, err)
	}
	h.drain(t)
	got, _ := h.instances.GetInstance(h.ctx(), inst.ID)
	log := h.log(t, inst.ID)
	last := log[len(log)-1]
	if got.State != "escalated" || last.Event != "timer.due" || last.Actor != authz.SystemEventActor(app.PrincipalScheduler).String() {
		t.Fatalf("after the sweep: %+v, last %+v", got, last)
	}
}

func TestAnotherTenantSeesNoInstances(t *testing.T) {
	h := newRunHarness(t)
	project, _ := h.project(t)
	h.bindDefault(t, project)
	translator, _ := h.as([]string{"translator"}, "de")
	h.translate(t, translator, project, "Willkommen")
	h.drain(t)
	mine := h.list(t, project)
	if len(mine) != 1 {
		t.Fatalf("instances = %+v", mine)
	}

	other := runHarnessFor(t, "globex")
	if got, _, err := other.instances.ListInstances(other.ctx(), app.InstanceFilter{}); err != nil || len(got) != 0 {
		t.Fatalf("another tenant lists %+v (%v)", got, err)
	}
	if _, err := other.instances.GetInstance(other.ctx(), mine[0].ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("another tenant reads the instance: %v", err)
	}
	if _, err := other.instances.ListTransitions(other.ctx(), mine[0].ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("another tenant reads the log: %v", err)
	}
	// The application role may not rewrite history.
	if _, err := env.App.Exec(context.Background(), "UPDATE workflow_transitions SET outcome = 'applied'"); err == nil {
		t.Error("glossa_app may update the transition log")
	}
	if _, err := env.App.Exec(context.Background(), "DELETE FROM workflow_transitions"); err == nil {
		t.Error("glossa_app may delete the transition log")
	}
}

func pageOf(n int) pagination.Page { return pagination.Page{Size: n} }
