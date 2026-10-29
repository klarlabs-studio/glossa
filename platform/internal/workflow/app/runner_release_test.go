package app_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/defaults"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Release requests on the runner (RFC 0006 §5.1), every port faked: a
// request runs on the seeded release approval definition without a
// binding, asks whom its environment requires, and is deployed as the
// last approver once enough distinct people — never the requester —
// have granted.

// fakeReleases stands in for Release: the facts of one request and the
// deploys and denials asked of it, with who asked.
type fakeReleases struct {
	mu        sync.Mutex
	facts     app.ReleaseRequestFacts
	deployErr error
	deployers []string
	deniers   []string
}

func (f *fakeReleases) Request(ctx context.Context, _, _ uuid.UUID) (app.ReleaseRequestFacts, error) {
	if err := authz.Require(ctx, authz.ReleasesRead); err != nil {
		return app.ReleaseRequestFacts{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.facts, nil
}

func (f *fakeReleases) Deploy(ctx context.Context, _, _ uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, _ := authz.From(ctx)
	f.deployers = append(f.deployers, p.Actor.String())
	if f.deployErr != nil {
		return "", f.deployErr
	}
	f.facts.State = "deployed"
	return "deployed", nil
}

func (f *fakeReleases) Deny(ctx context.Context, _, _ uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, _ := authz.From(ctx)
	f.deniers = append(f.deniers, p.Actor.String())
	f.facts.State = "denied"
	return nil
}

type releaseWorld struct {
	*runWorld
	releases  *fakeReleases
	request   uuid.UUID
	requester outbox.Actor
}

// newReleaseWorld is a project with no binding at all and a request
// needing two reviewers.
func newReleaseWorld(t *testing.T) *releaseWorld {
	t.Helper()
	base := newRunWorld(t, "")
	w := &releaseWorld{runWorld: base, request: uuid.New()}
	w.requester = w.person([]string{"developer"})
	w.releases = &fakeReleases{facts: app.ReleaseRequestFacts{
		Environment: "production", Requester: string(w.requester), State: "pending",
		Required: 2, From: domain.Party{Role: "reviewer"},
	}}
	w.runner = app.NewRunner(app.RunnerDeps{
		Tx: w.store, Definitions: w.bindings, Assignments: w.as, Actors: w.actors, Releases: w.releases,
		Timers: w.store, Now: func() time.Time { return w.now },
	})
	return w
}

func (w *releaseWorld) send(name domain.EventName, actor outbox.Actor) {
	w.t.Helper()
	ref := app.SubjectRef{Kind: domain.SubjectReleaseRequest, Project: w.project, ID: w.request}
	if err := w.runner.Handle(w.ctx, app.Event{ID: uuid.New(), Name: name, Actor: actor, Subjects: []app.SubjectRef{ref}}); err != nil {
		w.t.Fatalf("handle %s: %v", name, err)
	}
}

func TestAReleaseRequestRunsOnTheSeededDefinitionAndDeploysAsTheLastApprover(t *testing.T) {
	w := newReleaseWorld(t)
	w.send(domain.EventReleaseRequestCreated, w.requester)

	rec, ok := w.store.definitions[defaults.ReleaseApprovalName]
	if !ok || rec.Subject != domain.SubjectReleaseRequest {
		t.Fatalf("the release approval definition was not seeded: %+v", w.store.definitions)
	}
	inst, _ := w.only()
	if inst.Definition != rec.ID || inst.State != "pending" {
		t.Fatalf("instance = %+v, want pending on the seeded definition", inst)
	}
	if len(w.as.requested) != 1 {
		t.Fatalf("approvals requested = %+v", w.as.requested)
	}
	if r := w.as.requested[0]; r.Params.N != 2 || r.Params.From.Role != "reviewer" || r.Subject.Kind != domain.SubjectReleaseRequest {
		t.Fatalf("asked %+v, want the environment's two reviewers", r)
	}

	first, second := w.person([]string{"reviewer"}, "de"), w.person([]string{"reviewer"}, "de")
	// The requester's grant never counts, and one reviewer is not two.
	w.as.approvers = []string{string(w.requester), string(first)}
	w.send(domain.EventApprovalGranted, first)
	if inst, _ := w.only(); inst.State != "pending" || len(w.releases.deployers) != 0 {
		t.Fatalf("after one approval: %s, deploys %v", inst.State, w.releases.deployers)
	}
	// The same person twice is still one.
	w.as.approvers = []string{string(first), string(first)}
	w.send(domain.EventApprovalGranted, first)
	if len(w.releases.deployers) != 0 {
		t.Fatalf("one person granting twice deployed: %v", w.releases.deployers)
	}

	w.as.approvers = []string{string(first), string(second)}
	w.send(domain.EventApprovalGranted, second)
	inst, log := w.only()
	if inst.State != "approved" || len(w.releases.deployers) != 1 || w.releases.deployers[0] != string(second) {
		t.Fatalf("after two approvals: %s, deployed by %v; want approved, by the second approver", inst.State, w.releases.deployers)
	}
	if last := log[len(log)-1]; last.Actor != second || last.Actions[0].Outcome != app.ActionDone {
		t.Errorf("last row = %+v", last)
	}

	w.send(domain.EventReleaseRequestDeployed, second)
	if inst, _ := w.only(); inst.Status != domain.StatusFinished || inst.State != "deployed" {
		t.Fatalf("after the deploy: %+v", inst)
	}

	// A second request reuses the seeded definition rather than seeding
	// another.
	w.request = uuid.New()
	w.send(domain.EventReleaseRequestCreated, w.requester)
	if len(w.store.definitions) != 1 {
		t.Errorf("definitions = %d, want the one seeded", len(w.store.definitions))
	}
}

// A deploy Release refuses — the gate, run again, says no — leaves the
// instance where it was, and Release's refused event ends it.
func TestARefusedDeployEndsRefused(t *testing.T) {
	w := newReleaseWorld(t)
	w.releases.deployErr = fmt.Errorf("%w: the environment's check policy is not met", app.ErrRefused)
	w.send(domain.EventReleaseRequestCreated, w.requester)
	a, b := w.person([]string{"reviewer"}), w.person([]string{"reviewer"})
	w.as.approvers = []string{string(a), string(b)}
	w.send(domain.EventApprovalGranted, b)
	inst, log := w.only()
	if inst.State != "pending" || log[len(log)-1].Outcome != app.TransitionRefused {
		t.Fatalf("after a refused deploy: %s, %+v", inst.State, log[len(log)-1])
	}
	w.send(domain.EventReleaseRequestRefused, b)
	if inst, _ := w.only(); inst.Status != domain.StatusFinished || inst.State != "refused" {
		t.Fatalf("after Release refused it: %+v", inst)
	}
}

// One denial ends the request denied, denied as the person who denied.
func TestADenialDeniesTheRequestAsTheDenier(t *testing.T) {
	w := newReleaseWorld(t)
	w.send(domain.EventReleaseRequestCreated, w.requester)
	no := w.person([]string{"reviewer"})
	w.send(domain.EventApprovalDenied, no)
	if inst, _ := w.only(); inst.Status != domain.StatusFinished || inst.State != "denied" {
		t.Fatalf("after a denial: %+v", inst)
	}
	if len(w.releases.deniers) != 1 || w.releases.deniers[0] != string(no) || len(w.releases.deployers) != 0 {
		t.Fatalf("denied by %v, deployed by %v", w.releases.deniers, w.releases.deployers)
	}
}

// A release request names a binding's definition over the default when
// one is bound, so a tenant can extend the process.
func TestABoundDefinitionReplacesTheReleaseDefault(t *testing.T) {
	w := newReleaseWorld(t)
	w.bind(string(defaults.ReleaseApproval()))
	w.send(domain.EventReleaseRequestCreated, w.requester)
	if len(w.store.definitions) != 0 {
		t.Errorf("seeded a default although a binding names a definition")
	}
	if inst, _ := w.only(); inst.Definition != w.bindings.res.Version.DefinitionID {
		t.Errorf("instance runs on %s, want the bound definition", inst.Definition)
	}
}
