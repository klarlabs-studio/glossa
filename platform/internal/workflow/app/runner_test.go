package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/defaults"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Unit tests of stepping, with every port faked: what the runner does
// with an event, whatever stores it.

// approveOnGrant waits for an approval and then sets the review state
// as whoever granted it — the shape of RFC 0006 §2.5's example.
const approveOnGrant = `{
  "schema": "glossa.workflow/v1", "name": "approve-on-grant", "subject": "translation",
  "chart": { "id": "approve-on-grant", "initial": "waiting", "states": {
    "waiting": { "type": "atomic", "transitions": [
      { "event": "approval.granted", "target": "done", "actions": ["approve"] } ] },
    "done": { "type": "final" } } },
  "guards": {},
  "actions": { "approve": { "use": "set_review_state", "state": "approved" } }
}`

// assignWithDue assigns on entry with a due date and escalates on
// timer.due.
const assignWithDue = `{
  "schema": "glossa.workflow/v1", "name": "assign-with-due", "subject": "translation",
  "chart": { "id": "assign-with-due", "initial": "translating", "states": {
    "translating": { "type": "atomic", "entry": ["assign_vendor"], "transitions": [
      { "event": "assignment.completed", "target": "done" },
      { "event": "timer.due", "target": "escalated" } ] },
    "escalated": { "type": "atomic", "transitions": [ { "event": "assignment.completed", "target": "done" } ] },
    "done": { "type": "final" } } },
  "guards": {},
  "actions": { "assign_vendor": { "use": "assign", "to": { "vendor": "lingua-gmbh" }, "due": "P1D" } }
}`

type runWorld struct {
	t        *testing.T
	ctx      context.Context
	tenant   tenancy.ID
	project  uuid.UUID
	message  uuid.UUID
	store    *memStore
	bindings *fakeBindings
	tr       *fakeTranslations
	as       *fakeAssignments
	actors   *fakeActors
	now      time.Time
	runner   *app.Runner
}

func newRunWorld(t *testing.T, doc string) *runWorld {
	t.Helper()
	w := &runWorld{t: t, tenant: tenancy.NewID(), project: uuid.New(), message: uuid.New(),
		now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	w.ctx = tenancy.ContextWithTenant(context.Background(), w.tenant)
	w.store = newMemStore()
	w.bindings = &fakeBindings{}
	w.tr = &fakeTranslations{state: "needs_review", author: "person:" + uuid.NewString()}
	w.as = &fakeAssignments{}
	w.actors = &fakeActors{principals: map[outbox.Actor]authz.Principal{}}
	if doc != "" {
		w.bind(doc)
	}
	w.runner = app.NewRunner(app.RunnerDeps{
		Tx: w.store, Definitions: w.bindings, Translations: w.tr, Assignments: w.as, Actors: w.actors,
		Timers: w.store, Now: func() time.Time { return w.now },
	})
	return w
}

// bind stores doc as a version and binds it to the project.
func (w *runWorld) bind(doc string) {
	w.t.Helper()
	d, err := domain.Compile([]byte(doc))
	if err != nil {
		w.t.Fatalf("compile: %v", err)
	}
	rec := domain.DefinitionRecord{ID: uuid.New(), Name: d.Name, Subject: d.Subject, Latest: 1}
	v := domain.FirstVersion(rec, d, "test", w.now)
	w.store.versions[v.DefinitionID] = v
	w.bindings.res = &app.Resolution{
		Binding: domain.Binding{ProjectID: w.project, Subject: d.Subject, DefinitionID: rec.ID}, Version: v,
	}
}

// person registers a person actor holding roles for locales.
func (w *runWorld) person(roles []string, locales ...string) outbox.Actor {
	p, _ := authz.From(authztest.Member(w.ctx, w.tenant, roles, locales...))
	a := outbox.Actor(p.Actor.String())
	w.actors.principals[a] = p
	return a
}

func (w *runWorld) unit() app.SubjectRef {
	return app.SubjectRef{Kind: domain.SubjectTranslation, Project: w.project, ID: w.message, Locale: "de"}
}

func (w *runWorld) send(name domain.EventName, actor outbox.Actor) uuid.UUID {
	w.t.Helper()
	id := uuid.New()
	w.sendID(id, name, actor)
	return id
}

func (w *runWorld) sendID(id uuid.UUID, name domain.EventName, actor outbox.Actor) {
	w.t.Helper()
	if err := w.runner.Handle(w.ctx, app.Event{ID: id, Name: name, Actor: actor, Subjects: []app.SubjectRef{w.unit()}}); err != nil {
		w.t.Fatalf("handle %s: %v", name, err)
	}
}

func (w *runWorld) only() (domain.Instance, []app.Transition) {
	w.t.Helper()
	if len(w.store.instances) != 1 {
		w.t.Fatalf("instances = %d, want 1", len(w.store.instances))
	}
	for id, i := range w.store.instances {
		return i, w.store.transitions[id]
	}
	panic("unreachable")
}

var system = authz.SystemEventActor("localization.projection")

// waitForReview is the runner's fixture for its mechanics — replay,
// concurrency, ignored events: a revision starts an instance that waits
// for a review. It is not the default (which reacts to source changes,
// RFC 0006 §12.1) so the mechanics tests don't move when the default's
// content does.
const waitForReview = `{
  "schema": "glossa.workflow/v1", "name": "wait-for-review", "subject": "translation",
  "chart": { "id": "wait-for-review", "initial": "awaiting_review", "states": {
    "awaiting_review": { "type": "atomic", "transitions": [{ "event": "translation.reviewed", "target": "reviewed" }] },
    "reviewed": { "type": "final" } } },
  "guards": {}, "actions": {}
}`

func TestARevisionStartsAnInstanceThatEndsOnReview(t *testing.T) {
	w := newRunWorld(t, waitForReview)
	translator := w.person([]string{"translator"}, "de")
	reviewer := w.person([]string{"reviewer"}, "de")

	w.send(domain.EventTranslationRevised, translator)
	inst, log := w.only()
	if inst.State != "awaiting_review" || inst.Status != domain.StatusActive || len(log) != 1 {
		t.Fatalf("after the revision: %+v, log %+v", inst, log)
	}
	if log[0].From != domain.NotStarted || log[0].Outcome != app.TransitionApplied {
		t.Errorf("creation row = %+v", log[0])
	}

	w.send(domain.EventTranslationReviewed, reviewer)
	inst, log = w.only()
	if inst.State != "reviewed" || inst.Status != domain.StatusFinished || inst.FinishedAt == nil {
		t.Fatalf("after the review: %+v", inst)
	}
	if w.store.snapshots[inst.ID] != nil {
		t.Error("a finished instance kept its snapshot")
	}
	if len(log) != 2 || log[1].From != "awaiting_review" || log[1].To != "reviewed" || log[1].Actor != reviewer {
		t.Errorf("log = %+v", log)
	}
}

func TestAReplayedEventChangesNothing(t *testing.T) {
	w := newRunWorld(t, waitForReview)
	translator := w.person([]string{"translator"}, "de")
	id := w.send(domain.EventTranslationRevised, translator)
	w.sendID(id, domain.EventTranslationRevised, translator)
	w.sendID(id, domain.EventTranslationRevised, translator)
	if inst, log := w.only(); len(log) != 1 || inst.State != "awaiting_review" {
		t.Fatalf("a replay stepped again: %+v %+v", inst, log)
	}
}

func TestNoBindingMeansNoInstance(t *testing.T) {
	w := newRunWorld(t, "")
	w.send(domain.EventTranslationRevised, w.person([]string{"translator"}, "de"))
	if len(w.store.instances) != 0 {
		t.Fatalf("an unbound project got %d instances", len(w.store.instances))
	}
}

func TestOnlyWorkStartsAnInstance(t *testing.T) {
	w := newRunWorld(t, waitForReview)
	w.send(domain.EventTranslationReviewed, w.person([]string{"reviewer"}, "de"))
	if len(w.store.instances) != 0 {
		t.Fatal("a review on a unit nobody routed started an instance")
	}
}

func TestAnEventThatNoLongerAppliesIsIgnored(t *testing.T) {
	w := newRunWorld(t, waitForReview)
	translator := w.person([]string{"translator"}, "de")
	w.send(domain.EventTranslationRevised, translator)
	w.send(domain.EventTranslationOutdated, system)
	inst, log := w.only()
	if len(log) != 2 || log[1].Outcome != app.TransitionIgnored || inst.State != "awaiting_review" {
		t.Fatalf("log = %+v, instance %+v", log, inst)
	}
}

func TestWorkflowsOwnPrincipalNeverHoldsReview(t *testing.T) {
	w := newRunWorld(t, approveOnGrant)
	w.send(domain.EventTranslationRevised, w.person([]string{"translator"}, "de"))

	// A system actor's approval.granted: the action runs as Workflow's
	// background principal, which Localization refuses to let approve.
	w.send(domain.EventApprovalGranted, system)
	inst, log := w.only()
	last := log[len(log)-1]
	if last.Outcome != app.TransitionRefused || inst.State != "waiting" || last.To != "waiting" {
		t.Fatalf("a system approval moved the instance: %+v, %+v", inst, last)
	}
	if len(last.Actions) != 1 || last.Actions[0].Outcome != app.ActionRefused {
		t.Errorf("actions = %+v", last.Actions)
	}
	if w.tr.state == "approved" {
		t.Fatal("Workflow's principal approved a translation")
	}
	if len(w.tr.reviewers) != 1 || w.tr.reviewers[0] != authz.SystemEventActor(app.PrincipalRunner).String() {
		t.Errorf("Localization saw reviewers %v, want only %s", w.tr.reviewers, app.PrincipalRunner)
	}

	// An event recorded before events named their actors runs nothing.
	w.send(domain.EventApprovalGranted, outbox.ActorUnknown)
	inst, log = w.only()
	if last := log[len(log)-1]; last.Outcome != app.TransitionRefused || inst.State != "waiting" {
		t.Fatalf("an unknown actor's event: %+v", last)
	}
	if len(w.tr.reviewers) != 1 {
		t.Errorf("an unknown actor's action reached Localization: %v", w.tr.reviewers)
	}

	// A translator's grant does not approve either; a reviewer's does.
	w.send(domain.EventApprovalGranted, w.person([]string{"translator"}, "de"))
	if inst, _ = w.only(); inst.State != "waiting" || w.tr.state == "approved" {
		t.Fatal("a translator's approval approved")
	}
	reviewer := w.person([]string{"reviewer"}, "de")
	w.send(domain.EventApprovalGranted, reviewer)
	inst, log = w.only()
	if inst.Status != domain.StatusFinished || w.tr.state != "approved" {
		t.Fatalf("a reviewer's approval: %+v, state %s", inst, w.tr.state)
	}
	if got := w.tr.reviewers[len(w.tr.reviewers)-1]; got != reviewer.String() {
		t.Errorf("approved as %s, want the reviewer %s", got, reviewer)
	}
}

func TestTheRunnersGrant(t *testing.T) {
	w := newRunWorld(t, approveOnGrant)
	w.send(domain.EventTranslationRevised, w.person([]string{"translator"}, "de"))
	w.send(domain.EventApprovalGranted, system)
	held := w.tr.held[0]
	for _, p := range []authz.Permission{authz.CatalogRead, authz.TranslationsRead, authz.TranslationsWrite} {
		if !slices.Contains(held, p) {
			t.Errorf("%s holds %v, missing %s", app.PrincipalRunner, held, p)
		}
	}
	if slices.Contains(held, authz.TranslationsReview) || len(held) != 3 {
		t.Errorf("%s holds %v; it must hold exactly catalog.read, translations.read and translations.write", app.PrincipalRunner, held)
	}
}

func TestATimerIsRaisedAsTheSchedulerAndMovesTheInstance(t *testing.T) {
	w := newRunWorld(t, assignWithDue)
	w.send(domain.EventTranslationRevised, w.person([]string{"developer"}))
	inst, _ := w.only()
	if inst.State != "translating" || inst.Timer == nil || len(w.as.assigned) != 1 {
		t.Fatalf("after the start: %+v, assigned %d", inst, len(w.as.assigned))
	}
	if a := w.as.assigned[0]; a.Params.Due.String() != "P1D" || a.InstanceID != inst.ID || len(a.Units) != 1 || a.Units[0].Locale != "de" {
		t.Errorf("assigned %+v", a)
	}
	if !inst.Timer.DueAt.Equal(w.now.Add(24 * time.Hour)) {
		t.Errorf("timer due %v", inst.Timer.DueAt)
	}

	if n, err := w.runner.RaiseDueTimers(w.ctx); err != nil || n != 0 {
		t.Fatalf("raised %d early (%v)", n, err)
	}
	w.now = w.now.Add(25 * time.Hour)
	n, err := w.runner.SweepTimers(context.Background())
	if err != nil || n != 1 || len(w.store.published) != 1 {
		t.Fatalf("sweep raised %d (%v), published %d", n, err, len(w.store.published))
	}
	e := w.store.published[0]
	if e.Actor != authz.SystemEventActor(app.PrincipalScheduler) || e.Type != app.EventInstanceTimerDue {
		t.Fatalf("published %+v", e)
	}
	payload, _ := json.Marshal(e.Payload)
	ev, err := app.EventOf(outbox.Delivery{EventID: uuid.New(), Type: e.Type, Actor: e.Actor, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.runner.Handle(w.ctx, ev); err != nil {
		t.Fatal(err)
	}
	if inst, _ = w.only(); inst.State != "escalated" || inst.Timer != nil {
		t.Fatalf("after timer.due: %+v", inst)
	}
	// Overdue was due too, but the state that set it is gone.
	w.now = w.now.Add(48 * time.Hour)
	if n, err := w.runner.RaiseDueTimers(w.ctx); err != nil || n != 0 {
		t.Errorf("a cancelled timer was raised: %d %v", n, err)
	}
}

func TestAnUnwiredActionLeavesTheInstanceUnstarted(t *testing.T) {
	w := newRunWorld(t, assignWithDue)
	w.as.err = app.ErrUnavailable
	w.send(domain.EventTranslationRevised, w.person([]string{"developer"}))
	inst, log := w.only()
	if inst.Started() || log[0].Outcome != app.TransitionRefused || log[0].Actions[0].Outcome != app.ActionFailed {
		t.Fatalf("instance %+v, log %+v", inst, log)
	}
	w.as.err = nil
	w.send(domain.EventTranslationOutdated, system)
	if inst, _ = w.only(); inst.State != "translating" {
		t.Fatalf("the next event did not start it: %+v", inst)
	}
}

func TestConcurrentEventsOnOneSubjectSerialize(t *testing.T) {
	w := newRunWorld(t, waitForReview)
	translator := w.person([]string{"translator"}, "de")
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.runner.Handle(w.ctx, app.Event{ID: uuid.New(), Name: domain.EventTranslationRevised,
				Actor: translator, Subjects: []app.SubjectRef{w.unit()}}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, log := w.only(); len(log) != 20 {
		t.Fatalf("20 events, %d transitions", len(log))
	}
}

// ── fakes ───────────────────────────────────────────────────────────

type fakeBindings struct {
	res   *app.Resolution
	reads int
}

func (f *fakeBindings) Bindings(context.Context, uuid.UUID) ([]domain.Binding, error) {
	f.reads++
	if f.res == nil {
		return nil, nil
	}
	return []domain.Binding{f.res.Binding}, nil
}

func (f *fakeBindings) Resolve(ctx context.Context, _ domain.Target) (app.Resolution, bool, error) {
	if err := authz.Require(ctx, authz.WorkflowsRead); err != nil {
		return app.Resolution{}, false, err
	}
	if f.res == nil {
		return app.Resolution{}, false, nil
	}
	return *f.res, true, nil
}

// fakeTranslations stands in for Localization, including its rule:
// approving needs translations.review for the locale.
type fakeTranslations struct {
	mu        sync.Mutex
	state     string
	author    string
	reviewers []string
	held      [][]authz.Permission
}

func (f *fakeTranslations) Unit(context.Context, uuid.UUID, uuid.UUID, string) (app.UnitFacts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return app.UnitFacts{Found: true, Key: "home.title", ReviewState: f.state, Origin: "human", Author: f.author}, nil
}

func (f *fakeTranslations) Review(ctx context.Context, _, _ uuid.UUID, locale, state string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, _ := authz.From(ctx)
	f.reviewers = append(f.reviewers, p.Actor.String())
	f.held = append(f.held, p.Grant.Permissions())
	l, _ := authz.ParseLocale(locale)
	perm := authz.TranslationsWrite
	if state == "approved" || state == "rejected" {
		perm = authz.TranslationsReview
	}
	if err := authz.RequireFor(ctx, perm, l); err != nil {
		return err
	}
	f.state = state
	return nil
}

type fakeAssignments struct {
	mu        sync.Mutex
	err       error
	assigned  []app.WorkflowAssign
	actors    []string
	requested []app.WorkflowApproval
	approvers []string
}

func (f *fakeAssignments) AssignForInstance(ctx context.Context, in app.WorkflowAssign) (domain.Assignment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return domain.Assignment{}, f.err
	}
	p, _ := authz.From(ctx)
	f.assigned = append(f.assigned, in)
	f.actors = append(f.actors, p.Actor.String())
	return domain.Assignment{ID: uuid.New()}, nil
}

func (f *fakeAssignments) RequestApprovalForInstance(_ context.Context, in app.WorkflowApproval) (domain.Approval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requested = append(f.requested, in)
	return domain.Approval{ID: uuid.New()}, f.err
}

func (f *fakeAssignments) Approvers(context.Context, uuid.UUID, domain.ApprovalSubject) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.approvers), nil
}

type fakeActors struct {
	principals map[outbox.Actor]authz.Principal
	reads      int
}

func (f *fakeActors) Principal(_ context.Context, a outbox.Actor) (authz.Principal, bool, error) {
	f.reads++
	p, ok := f.principals[a]
	return p, ok, nil
}

func (f *fakeActors) Held(p authz.Principal, _ string) []string {
	var out []string
	for _, perm := range p.Grant.Permissions() {
		out = append(out, string(perm))
	}
	return out
}

// memStore is an in-memory InstanceStore. A transaction holds one lock
// for its whole length, which serializes like the instance row lock
// does for the events of one subject, and rolls back on error.
type memStore struct {
	mu          sync.Mutex
	instances   map[uuid.UUID]domain.Instance
	snapshots   map[uuid.UUID][]byte
	transitions map[uuid.UUID][]app.Transition
	versions    map[uuid.UUID]domain.Version
	// later are a definition's versions after the one in versions, by
	// number (rebase tests).
	later map[uuid.UUID]map[int]domain.Version
	// definitions are the tenant-wide definitions by name.
	definitions map[string]domain.DefinitionRecord
	published   []outbox.Event
	seeded      bool
}

func newMemStore() *memStore {
	return &memStore{instances: map[uuid.UUID]domain.Instance{}, snapshots: map[uuid.UUID][]byte{},
		transitions: map[uuid.UUID][]app.Transition{}, versions: map[uuid.UUID]domain.Version{},
		definitions: map[string]domain.DefinitionRecord{}, later: map[uuid.UUID]map[int]domain.Version{}}
}

func (m *memStore) InTenant(ctx context.Context, fn func(context.Context, app.InstanceStore) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx := &memTx{m: m, instances: clone(m.instances), snapshots: clone(m.snapshots),
		transitions: clone(m.transitions), published: slices.Clone(m.published), seeded: m.seeded}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	m.instances, m.snapshots, m.transitions, m.published, m.seeded =
		tx.instances, tx.snapshots, tx.transitions, tx.published, tx.seeded
	return nil
}

func (m *memStore) TenantsWithDueTimers(context.Context, time.Time, int) ([]tenancy.ID, error) {
	return []tenancy.ID{tenantOfTest}, nil
}

// tenantOfTest is never consulted by memStore; the sweep needs one id.
var tenantOfTest = tenancy.NewID()

func clone[K comparable, V any](in map[K]V) map[K]V {
	out := make(map[K]V, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

type memTx struct {
	m           *memStore
	instances   map[uuid.UUID]domain.Instance
	snapshots   map[uuid.UUID][]byte
	transitions map[uuid.UUID][]app.Transition
	published   []outbox.Event
	seeded      bool
}

func (t *memTx) sorted(keep func(domain.Instance) bool) []domain.Instance {
	var out []domain.Instance
	for _, i := range t.instances {
		if keep(i) {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID.String() < out[b].ID.String() })
	return out
}

func (t *memTx) LockActive(_ context.Context, s app.SubjectRef) ([]domain.Instance, error) {
	return t.sorted(func(i domain.Instance) bool {
		return i.Status == domain.StatusActive && i.Kind == s.Kind && i.SubjectID == s.ID && i.Locale == s.Locale
	}), nil
}

func (t *memTx) LockActiveInProject(_ context.Context, p uuid.UUID, k domain.SubjectKind, _ int) ([]domain.Instance, error) {
	return t.sorted(func(i domain.Instance) bool { return i.Status == domain.StatusActive && i.Project == p && i.Kind == k }), nil
}

func (t *memTx) LockInstance(_ context.Context, id uuid.UUID) (domain.Instance, error) {
	i, ok := t.instances[id]
	if !ok {
		return domain.Instance{}, app.ErrNotFound
	}
	return i, nil
}

func (t *memTx) InsertInstance(_ context.Context, i domain.Instance) (bool, error) {
	for _, o := range t.instances {
		if o.Status == domain.StatusActive && o.Definition == i.Definition && o.SubjectID == i.SubjectID && o.Locale == i.Locale {
			return false, nil
		}
	}
	t.instances[i.ID] = i
	return true, nil
}

func (t *memTx) SaveInstance(_ context.Context, i domain.Instance, snapshot []byte) error {
	t.instances[i.ID], t.snapshots[i.ID] = i, snapshot
	return nil
}

func (t *memTx) Snapshot(_ context.Context, id uuid.UUID) ([]byte, error) {
	return t.snapshots[id], nil
}

func (t *memTx) HasTransition(_ context.Context, instance, event uuid.UUID) (bool, error) {
	return slices.ContainsFunc(t.transitions[instance], func(tr app.Transition) bool { return tr.EventID == event }), nil
}

func (t *memTx) AppendTransition(_ context.Context, instance uuid.UUID, tr app.Transition) error {
	t.transitions[instance] = append(slices.Clone(t.transitions[instance]), tr)
	return nil
}

func (t *memTx) Version(_ context.Context, definition uuid.UUID, n int) (domain.Version, error) {
	if v, ok := t.m.later[definition][n]; ok {
		return v, nil
	}
	v, ok := t.m.versions[definition]
	if !ok || v.Number != n {
		return domain.Version{}, app.ErrNotFound
	}
	return v, nil
}

func (t *memTx) LatestVersion(_ context.Context, definition uuid.UUID) (int, error) {
	v, ok := t.m.versions[definition]
	if !ok {
		return 0, app.ErrNotFound
	}
	latest := v.Number
	for n := range t.m.later[definition] {
		latest = max(latest, n)
	}
	return latest, nil
}

func (t *memTx) RebaseInstance(_ context.Context, i domain.Instance, snapshot []byte) error {
	if cur, ok := t.instances[i.ID]; !ok || cur.Status != domain.StatusActive {
		return app.ErrNotFound
	}
	t.instances[i.ID], t.snapshots[i.ID] = i, snapshot
	return nil
}

func (t *memTx) DeleteFinished(_ context.Context, cutoff time.Time, limit int) (int, error) {
	n := 0
	for id, i := range t.instances {
		if n < limit && i.Status == domain.StatusFinished && i.FinishedAt != nil && i.FinishedAt.Before(cutoff) {
			delete(t.instances, id)
			delete(t.transitions, id)
			n++
		}
	}
	return n, nil
}

func (t *memTx) LockDueTimers(_ context.Context, now time.Time, _ int) ([]domain.Instance, error) {
	return t.sorted(func(i domain.Instance) bool {
		if i.Status != domain.StatusActive || i.Timer == nil {
			return false
		}
		_, _, due := i.Timer.Due(now)
		return due
	}), nil
}

func (t *memTx) Publish(_ context.Context, e outbox.Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	t.published = append(t.published, e)
	return nil
}

func (t *memTx) SeedDefault(context.Context, *domain.DefinitionRecord, *domain.Version, time.Time) (bool, error) {
	if t.seeded {
		return false, errors.Join(app.ErrConflict)
	}
	t.seeded = true
	return true, nil
}

func (t *memTx) Seeded(context.Context) (bool, error) { return t.seeded, nil }

func (t *memTx) HasTenantDefinition(context.Context, string) (bool, error) { return false, nil }

func (t *memTx) TenantDefinition(_ context.Context, name string) (domain.DefinitionRecord, error) {
	rec, ok := t.m.definitions[name]
	if !ok {
		return domain.DefinitionRecord{}, app.ErrNotFound
	}
	return rec, nil
}

func (t *memTx) InsertDefinition(_ context.Context, rec domain.DefinitionRecord, v domain.Version) error {
	if _, ok := t.m.definitions[rec.Name]; ok {
		return app.ErrConflict
	}
	t.m.definitions[rec.Name], t.m.versions[rec.ID] = rec, v
	return nil
}

// The default definition (RFC 0006 §12.1, as the owner decided): a
// source change sends an approved translation back to review and asks
// one reviewer, and that reviewer's approval — never its author's —
// approves it as the reviewer.
func TestTheDefaultReReviewsAfterASourceChange(t *testing.T) {
	w := newRunWorld(t, string(defaults.Review()))
	w.tr.state = "approved"
	reviewer := w.person([]string{"reviewer"}, "de")

	// The projection catching up names a system actor: Workflow's own
	// principal holds translations.write, which is what needs_review
	// takes, and never review.
	w.send(domain.EventTranslationOutdated, system)
	inst, _ := w.only()
	if inst.State != "reviewing" || w.tr.state != "needs_review" {
		t.Fatalf("after the source change: instance %s, translation %s; want reviewing, needs_review", inst.State, w.tr.state)
	}
	if len(w.as.requested) != 1 || w.as.requested[0].Params.N != 1 || w.as.requested[0].Params.From.Role != "reviewer" {
		t.Fatalf("approvals requested = %+v, want one from a reviewer", w.as.requested)
	}

	// The reviewer grants: the guard counts them, and the approve action
	// runs as them.
	w.as.approvers = []string{string(reviewer)}
	w.send(domain.EventApprovalGranted, reviewer)
	inst, log := w.only()
	if inst.Status != domain.StatusFinished || w.tr.state != "approved" {
		t.Fatalf("after the approval: instance %+v, translation %s", inst, w.tr.state)
	}
	if last := log[len(log)-1]; last.Actor != reviewer {
		t.Errorf("approved by %s, want the reviewer", last.Actor)
	}
}

// A translator's own revision is not a source change: the default
// finishes at once, so instances count work in flight and not every
// translation ever written (§2.5).
func TestTheDefaultLeavesNothingBehindForARevision(t *testing.T) {
	w := newRunWorld(t, string(defaults.Review()))
	w.send(domain.EventTranslationRevised, w.person([]string{"translator"}, "de"))
	if inst, _ := w.only(); inst.Status != domain.StatusFinished {
		t.Fatalf("a revision left an instance %s in %s", inst.Status, inst.State)
	}
	if len(w.as.requested) != 0 {
		t.Errorf("a revision asked for approval: %+v", w.as.requested)
	}
}

// A CI token — GitHub OIDC's push path, the usual way source arrives —
// holds only catalog permissions, and the runner resolves no principal
// for it, so every action of its events is refused. The default's
// demotion to needs_review falls back to Workflow's own principal for
// it (§2.3, amended in wave 3), and the log says so; approving still
// runs only as the actor.
func TestACITokensSourceChangeStillSendsItBackToReview(t *testing.T) {
	w := newRunWorld(t, string(defaults.Review()))
	w.tr.state = "approved"
	ci := outbox.Actor("token:" + uuid.NewString()) // resolves to no principal, as a CI token does

	w.send(domain.EventTranslationOutdated, ci)
	inst, log := w.only()
	if inst.State != "reviewing" || w.tr.state != "needs_review" {
		t.Fatalf("after a CI push: instance %s, translation %s", inst.State, w.tr.state)
	}
	demoted := log[len(log)-1].Actions[0]
	if demoted.Outcome != app.ActionDone || !strings.Contains(demoted.Detail, app.PrincipalRunner) {
		t.Fatalf("demotion = %+v, want done and saying it ran as Workflow", demoted)
	}
	if last := log[len(log)-1]; last.Actor != ci {
		t.Errorf("the transition names %s, want the token that caused it", last.Actor)
	}

	// The same token's approval.granted cannot approve: the fallback is
	// for demotion only.
	w.as.approvers = []string{"person:" + uuid.NewString()}
	w.send(domain.EventApprovalGranted, ci)
	if w.tr.state != "needs_review" {
		t.Fatalf("a token's event approved the translation: %s", w.tr.state)
	}
	if _, log := w.only(); log[len(log)-1].Outcome != app.TransitionRefused {
		t.Errorf("the approve step = %+v, want refused", log[len(log)-1])
	}
}

// Whoever may write the locale demotes as themselves: a developer
// holds translations.write, so no fallback and nothing in the detail.
func TestADevelopersSourceChangeDemotesAsTheDeveloper(t *testing.T) {
	w := newRunWorld(t, string(defaults.Review()))
	w.tr.state = "approved"
	w.send(domain.EventTranslationOutdated, w.person([]string{"developer"}))
	_, log := w.only()
	if d := log[len(log)-1].Actions[0]; d.Outcome != app.ActionDone || d.Detail != "" || w.tr.state != "needs_review" {
		t.Fatalf("demotion = %+v, translation %s", d, w.tr.state)
	}
}
