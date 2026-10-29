package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// The instance runner (RFC 0006 §2.5): an outbox subscriber that steps
// workflow instances. For each event it resolves the binding, loads or
// creates the subject's instance, restores its snapshot, sends it the
// event, runs the actions the transition asks for as the actor whose
// event caused it, stores the new snapshot and appends to the
// transition log — in the handler's tenant transaction, with the
// instance row locked and the outbox event id as the idempotency key.

// Background principals. Their names are stored as actors; never
// rename them.
const (
	// PrincipalRunner is Workflow's own background principal: what a
	// transition's actions run as when the event that caused it was the
	// platform's (a system actor — another context's background work,
	// or Workflow's own scheduler). It holds catalog.read,
	// translations.read and translations.write, and never
	// translations.review (§2.5, §14 decision 5): a workflow cannot
	// create a way to approve text.
	PrincipalRunner = "workflow.runner"
	// PrincipalReader reads what guards look at — the subject snapshot,
	// the bindings — and nothing else. It is not an actor: nothing it
	// does is written anywhere.
	PrincipalReader = "workflow.reader"
	// PrincipalScheduler is the actor of timer.due and timer.overdue.
	PrincipalScheduler = "workflow.scheduler"
	// PrincipalSeed is who the seeded default definition was created
	// by.
	PrincipalSeed = "workflow.seed"
)

// runnerPermissions are PrincipalRunner's grant (§2.5). A test pins
// that translations.review is not among them.
var runnerPermissions = []authz.Permission{authz.CatalogRead, authz.TranslationsRead, authz.TranslationsWrite}

// readerPermissions are PrincipalReader's.
var readerPermissions = []authz.Permission{
	authz.CatalogRead, authz.TranslationsRead, authz.IntelligenceRead, authz.WorkflowsRead, authz.AssignmentsRead,
	authz.ReleasesRead,
}

// startEvents are the events that start an instance where a subject has
// none: work entering a unit. The others — a review, an approval, an
// assignment, a check run, a timer — only move an instance that exists;
// a review on a unit nobody routed is M4's behaviour, not a workflow.
var startEvents = []domain.EventName{
	domain.EventTranslationRevised, domain.EventTranslationOutdated, domain.EventSuggestionCreated,
	domain.EventReleaseRequestCreated,
}

// maxProjectFanOut bounds the instances one project-wide event steps.
const maxProjectFanOut = 500

// Event is one vocabulary event as the runner receives it from the
// outbox (see subscribers.go for the mapping).
type Event struct {
	// ID is the outbox event id: the idempotency key.
	ID    uuid.UUID
	Name  domain.EventName
	Actor outbox.Actor
	// Subjects are the subjects the event is about.
	Subjects []SubjectRef
	// Project and Kind are set instead for a project-wide event
	// (check_run.recorded): every active instance of that kind in the
	// project.
	Project uuid.UUID
	Kind    domain.SubjectKind
	// Instance is set instead for an event about one instance: a timer
	// raised for it, or an assignment or approval it asked for.
	// TimerState is the state that set a timer; an instance that has
	// left it ignores the timer.
	Instance   uuid.UUID
	TimerState string
}

// Bindings is what the runner asks about bindings; *Service implements
// it under the caller's workflows.read.
type Bindings interface {
	Bindings(ctx context.Context, project uuid.UUID) ([]domain.Binding, error)
	Resolve(ctx context.Context, t domain.Target) (Resolution, bool, error)
}

var _ Bindings = (*Service)(nil)

// RunnerDeps are the runner's collaborators. Nil ports degrade safely:
// a guard reading one sees nothing, and an action needing one is
// ErrUnavailable.
type RunnerDeps struct {
	Tx           InstanceTransactor
	Definitions  Bindings
	Translations Translations
	Findings     Findings
	Suggestions  Suggestions
	Assignments  AssignmentsPort
	// Releases is Release's side of a release request (RFC 0006 §5.1).
	Releases ReleaseRequests
	Actors   Actors
	Timers   TimerScanner
	// Retention finds the tenants whose finished instances are past
	// their retention; SweepRetention needs it.
	Retention RetentionScanner
	// Metrics records transitions and the instance and assignment
	// gauges (RFC 0006 §10.1); Workload counts the gauges. Either may be
	// nil, and then nothing is recorded.
	Metrics  RunnerMetrics
	Workload WorkloadScanner
	Logger   *slog.Logger
	Now      func() time.Time
}

// Runner steps workflow instances.
type Runner struct {
	d RunnerDeps
	// definitions caches compiled versions; a version is immutable.
	definitions sync.Map // uuid(version id) → *domain.Definition
	// seeded remembers the tenants whose default is seeded.
	seeded sync.Map // tenancy.ID → struct{}
	// stepping admits one Handle at a time in this process. A step holds
	// its transaction's connection while it reads the subject and runs
	// actions through other contexts, each of which takes a connection
	// of its own (§2.5 keeps an action's outcome in the transition row,
	// so those calls happen inside the step). Hold-and-wait on one pool
	// deadlocks once as many steps hold connections as the pool has:
	// on a CI runner whose pool is four, eight concurrent first triggers
	// did. With one step at a time, a pool of two can never deadlock.
	// It costs nothing the dispatcher was using — it delivers one event
	// at a time — and replicas still step in parallel, each on its own
	// pool, serialized per subject by the instance row lock.
	stepping chan struct{}
}

// NewRunner returns a runner.
func NewRunner(d RunnerDeps) *Runner {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Runner{d: d, stepping: make(chan struct{}, 1)}
}

func (r *Runner) now() time.Time { return r.d.Now().UTC() }

// acting is who a transition's actions run as.
type acting struct {
	principal authz.Principal
	// ok is false when the actor holds nothing — an event from before
	// events named their actors, or an actor with no grant left in the
	// tenant. Every action needing a principal is then refused without
	// being attempted.
	ok bool
	// permissions returns what the actor holds for a locale.
	permissions func(locale string) []string
	// why says why ok is false.
	why string
}

// in returns ctx acting as the actor: the request-less equivalent of
// the actor's own call.
func (a acting) in(ctx context.Context) context.Context { return authz.WithPrincipal(ctx, a.principal) }

// Handle steps every instance ev concerns. It is the outbox handler:
// idempotent on ev.ID, and an error means "deliver again".
func (r *Runner) Handle(ctx context.Context, ev Event) error {
	if _, ok := tenancy.FromContext(ctx); !ok {
		return outbox.Permanent(errors.New("workflow: an event without a tenant"))
	}
	select {
	case r.stepping <- struct{}{}:
		defer func() { <-r.stepping }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := r.EnsureDefault(ctx); err != nil {
		return err
	}
	reader, err := authz.Background(ctx, PrincipalReader, readerPermissions...)
	if err != nil {
		return outbox.Permanent(err)
	}
	act, err := r.actingAs(ctx, ev.Actor)
	if err != nil {
		return err
	}
	switch {
	case ev.Instance != uuid.Nil:
		return r.handleInstance(ctx, reader, act, ev)
	case ev.Project != uuid.Nil:
		return r.handleProject(ctx, reader, act, ev)
	}
	for _, s := range ev.Subjects {
		if err := r.handleSubject(ctx, reader, act, ev, s); err != nil {
			return err
		}
	}
	return nil
}

// actingAs builds the principal actions run as (§2.5): a person's or a
// token's own grant, Workflow's background principal for the
// platform's own events, and nobody for an event whose actor was never
// recorded.
func (r *Runner) actingAs(ctx context.Context, actor outbox.Actor) (acting, error) {
	held := func(p authz.Principal) func(string) []string {
		return func(locale string) []string {
			if r.d.Actors == nil {
				return nil
			}
			return r.d.Actors.Held(p, locale)
		}
	}
	none := func(string) []string { return nil }
	switch actor.Kind() {
	case outbox.ActorKindSystem:
		bg, err := authz.Background(ctx, PrincipalRunner, runnerPermissions...)
		if err != nil {
			return acting{}, outbox.Permanent(err)
		}
		p, _ := authz.From(bg)
		return acting{principal: p, ok: true, permissions: held(p)}, nil
	case outbox.ActorKindPerson, outbox.ActorKindToken:
		if r.d.Actors == nil {
			return acting{permissions: none, why: "this deployment cannot resolve who the actor is"}, nil
		}
		p, ok, err := r.principal(ctx, actor)
		if err != nil {
			return acting{}, err
		}
		if !ok {
			return acting{permissions: none, why: actor.String() + " holds nothing in this tenant any more"}, nil
		}
		return acting{principal: p, ok: true, permissions: held(p)}, nil
	}
	return acting{permissions: none,
		why: "the event was recorded before events named their actors, so no action that needs a permission runs for it"}, nil
}

// handleSubject steps the subject's active instances, starting one
// under the binding that applies when ev starts work and none of that
// definition is active. A release request is always bound: when no
// binding names a definition for it, it runs on the tenant's release
// approval default (RFC 0006 §5.1).
func (r *Runner) handleSubject(ctx, reader context.Context, act acting, ev Event, s SubjectRef) error {
	start := slices.Contains(startEvents, ev.Name)
	bound := start && s.Kind == domain.SubjectReleaseRequest
	if start && !bound {
		bs, err := r.bindings(reader, s.Project)
		if err != nil {
			return err
		}
		bound = slices.ContainsFunc(bs, func(b domain.Binding) bool { return b.Subject == s.Kind })
	}
	return r.inTenant(ctx, func(txCtx, ctx context.Context, st InstanceStore) error {
		instances, err := st.LockActive(txCtx, s)
		if err != nil {
			return err
		}
		if len(instances) == 0 && !bound {
			return nil // no workflow here: M4's behaviour (§2.2)
		}
		subject, err := r.loadSubject(reader, s)
		if err != nil {
			return err
		}
		if bound {
			if instances, err = r.startIfNeeded(txCtx, reader, st, s, subject, instances); err != nil {
				return err
			}
		}
		for _, inst := range instances {
			if err := r.step(txCtx, ctx, st, act, ev, inst, subject); err != nil {
				return err
			}
		}
		return nil
	})
}

// startIfNeeded creates the instance of the binding's definition for s
// unless one is active. Two first triggers racing meet on the unique
// active index; the loser locks and steps the winner's instance.
func (r *Runner) startIfNeeded(
	ctx, reader context.Context, st InstanceStore, s SubjectRef, subject loaded, instances []domain.Instance,
) ([]domain.Instance, error) {
	res, found, err := r.d.Definitions.Resolve(reader, domain.Target{
		ProjectID: s.Project, Subject: s.Kind, Locale: s.Locale, Namespace: subject.Subject.Namespace,
	})
	if err == nil && !found && s.Kind == domain.SubjectReleaseRequest {
		res.Version, found, err = r.releaseDefault(ctx, st)
	}
	if err != nil || !found {
		return instances, err
	}
	if slices.ContainsFunc(instances, func(i domain.Instance) bool { return i.Definition == res.Version.DefinitionID }) {
		return instances, nil
	}
	id, err := uuid.NewV7()
	if err != nil {
		return nil, err
	}
	now := r.now()
	inst := domain.Instance{
		ID: id, Project: s.Project, Definition: res.Version.DefinitionID, Version: res.Version.Number,
		Kind: s.Kind, SubjectID: s.ID, Locale: s.Locale, State: domain.NotStarted, Status: domain.StatusActive,
		CreatedAt: now, UpdatedAt: now,
	}
	inserted, err := st.InsertInstance(ctx, inst)
	if err != nil {
		return nil, err
	}
	if inserted {
		return append(instances, inst), nil
	}
	return st.LockActive(ctx, s)
}

// handleProject steps every active instance of a kind in a project.
func (r *Runner) handleProject(ctx, reader context.Context, act acting, ev Event) error {
	return r.inTenant(ctx, func(txCtx, ctx context.Context, st InstanceStore) error {
		instances, err := st.LockActiveInProject(txCtx, ev.Project, ev.Kind, maxProjectFanOut)
		if err != nil {
			return err
		}
		if len(instances) == maxProjectFanOut {
			r.d.Logger.WarnContext(ctx, "workflow: a project-wide event reached only the first instances",
				slog.String("event", string(ev.Name)), slog.Int("instances", maxProjectFanOut))
		}
		for _, inst := range instances {
			subject, err := r.loadSubject(reader, refOf(inst))
			if err != nil {
				return err
			}
			if err := r.step(txCtx, ctx, st, act, ev, inst, subject); err != nil {
				return err
			}
		}
		return nil
	})
}

// handleInstance steps the one instance an event names: the one a
// timer was raised for, or the one that asked for an assignment or an
// approval.
func (r *Runner) handleInstance(ctx, reader context.Context, act acting, ev Event) error {
	return r.inTenant(ctx, func(txCtx, ctx context.Context, st InstanceStore) error {
		inst, err := st.LockInstance(txCtx, ev.Instance)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil || inst.Status != domain.StatusActive {
			return err
		}
		subject, err := r.loadSubject(reader, refOf(inst))
		if err != nil {
			return err
		}
		return r.step(txCtx, ctx, st, act, ev, inst, subject)
	})
}

func refOf(i domain.Instance) SubjectRef {
	return SubjectRef{Kind: i.Kind, Project: i.Project, ID: i.SubjectID, Locale: i.Locale}
}

// step moves one locked instance with ev, once.
func (r *Runner) step(ctx, outer context.Context, st InstanceStore, act acting, ev Event, inst domain.Instance, subject loaded) error {
	done, err := st.HasTransition(ctx, inst.ID, ev.ID)
	if err != nil || done {
		return err
	}
	def, err := r.definition(ctx, st, inst)
	if err != nil {
		return err
	}
	step := domain.Step{Subject: subject.Subject, Trigger: domain.Trigger{
		Event: ev.Name, Actor: ev.Actor.String(), Permissions: act.permissions(inst.Locale),
	}}
	t := Transition{From: inst.State, Event: ev.Name, Actor: ev.Actor, EventID: ev.ID, At: r.now()}
	to, effects, applies, err := r.compute(ctx, st, def, inst, step, ev, &t)
	if err != nil {
		return err
	}
	if !applies {
		t.To, t.Outcome = inst.State, TransitionIgnored
		counted(outer, t.Outcome)
		return st.AppendTransition(ctx, inst.ID, t)
	}
	outcomes, timer, refused, err := r.run(ctx, outer, act, effects, inst, subject, ev, to)
	if err != nil {
		return err
	}
	t.Actions = outcomes
	if refused {
		t.To, t.Outcome = inst.State, TransitionRefused
		counted(outer, t.Outcome)
		return st.AppendTransition(ctx, inst.ID, t)
	}
	t.To, t.Outcome = to, TransitionApplied
	snapshot := r.advance(&inst, def, to, timer)
	if err := st.SaveInstance(ctx, inst, snapshot); err != nil {
		return err
	}
	counted(outer, t.Outcome)
	return st.AppendTransition(ctx, inst.ID, t)
}

// compute finds where ev takes the instance and the effects on the
// way, without carrying any out. An instance not yet started enters its
// chart first (running the initial state's entry actions), then takes
// the event if it applies there.
func (r *Runner) compute(
	ctx context.Context, st InstanceStore, def *domain.Definition, inst domain.Instance, step domain.Step, ev Event, t *Transition,
) (string, []domain.Effect, bool, error) {
	if ev.Name == domain.EventTimerDue || ev.Name == domain.EventTimerOverdue {
		if inst.State != ev.TimerState {
			return "", nil, false, nil // raised for a state the instance has left
		}
	}
	if !inst.Started() {
		state, entered := def.Start(step)
		t.Guards = guardViews(def.GuardOutcomes(state, step))
		effects := entered.Effects
		if to, moved, ok, err := def.Advance(state, domain.Step{Subject: step.Subject, Trigger: step.Trigger}); err != nil {
			return "", nil, false, err
		} else if ok {
			state, effects = to, append(effects, moved.Effects...)
		}
		return state, effects, true, nil
	}
	raw, err := st.Snapshot(ctx, inst.ID)
	if err != nil {
		return "", nil, false, err
	}
	from, err := def.RestoreState(raw)
	if err != nil {
		return "", nil, false, outbox.Permanent(err)
	}
	t.Guards = guardViews(def.GuardOutcomes(from, step))
	to, moved, ok, err := def.Advance(from, step)
	if err != nil {
		return "", nil, false, outbox.Permanent(err)
	}
	return to, moved.Effects, ok, nil
}

// advance applies a transition to inst and returns the snapshot to
// store: nil once the instance is finished. Leaving a state cancels the
// timer it set; an action with a due period sets the new one.
func (r *Runner) advance(inst *domain.Instance, def *domain.Definition, to string, timer *domain.Timer) []byte {
	now := r.now()
	if to != inst.State {
		inst.Timer = nil
	}
	if timer != nil {
		inst.Timer = timer
	}
	inst.State, inst.UpdatedAt = to, now
	if def.Final(to) {
		inst.Status, inst.FinishedAt, inst.Timer = domain.StatusFinished, &now, nil
		return nil
	}
	return def.Snapshot(to)
}

func guardViews(gs []domain.GuardOutcome) []GuardOutcome {
	out := make([]GuardOutcome, len(gs))
	for i, g := range gs {
		out[i] = GuardOutcome{Guard: g.Guard, Passed: g.Passed}
	}
	return out
}

// definition loads the version an instance runs on, compiled once per
// process.
func (r *Runner) definition(ctx context.Context, st InstanceStore, inst domain.Instance) (*domain.Definition, error) {
	key := fmt.Sprintf("%s/%d", inst.Definition, inst.Version)
	if d, ok := r.definitions.Load(key); ok {
		return d.(*domain.Definition), nil
	}
	v, err := st.Version(ctx, inst.Definition, inst.Version)
	if err != nil {
		return nil, err
	}
	d, err := v.Load()
	if err != nil {
		return nil, outbox.Permanent(err)
	}
	r.definitions.Store(key, d)
	return d, nil
}
