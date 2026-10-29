//go:build integration

package app_test

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	releasepg "go.klarlabs.de/glossa/platform/internal/release/adapters/postgres"
	releasesources "go.klarlabs.de/glossa/platform/internal/release/adapters/sources"
	releaseapp "go.klarlabs.de/glossa/platform/internal/release/app"
	releasedomain "go.klarlabs.de/glossa/platform/internal/release/domain"
	widentity "go.klarlabs.de/glossa/platform/internal/workflow/adapters/identity"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/postgres"
	wrelease "go.klarlabs.de/glossa/platform/internal/workflow/adapters/release"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/sources"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/defaults"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// Release approvals end to end (RFC 0006 §5.1, §12.3's steps below the
// API): Release, Workflow's runner, its WorkService and Identity's real
// members on Postgres, events through the real outbox. A publish into
// an environment requiring two reviewers moves no pointer; the
// requester cannot approve their own request, a token cannot approve at
// all; after one approval the environment still serves the previous
// release; after the second, the seeded release-approval definition
// deploys it as the second approver; a denial ends a request.

type releaseRun struct {
	*runHarness
	w       *workHarness
	release *releaseapp.Service
	work    *app.WorkService
}

func newReleaseRun(t *testing.T) *releaseRun {
	t.Helper()
	h := newRunHarness(t)
	w := newWorkHarness(t, h.tenant)
	key, err := releasedomain.ParseSigningKey("test-2026", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := releasedomain.NewSigner([]releasedomain.SigningKey{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rel := releaseapp.New(releasepg.NewTransactor(db.NewUnitOfWork(env.App)), releasesources.New(h.catalog, h.localization),
		objectstore.NewMemory(), signer)
	requests := wrelease.NewRequests(rel)
	work := app.NewWorkService(postgres.NewWorkTransactor(w.uow), widentity.NewDirectory(w.ids),
		sources.NewAuthors(h.catalog, h.localization), app.WithReleaseRequests(requests))
	rel.UseApprovals(wrelease.NewLedger(work))
	h.releases = requests
	h.subscribers = append(h.subscribers, rel.Subscribe)
	h.wire(t, work)
	return &releaseRun{runHarness: h, w: w, release: rel, work: work}
}

// member is a real member of the tenant, signed in, registered with the
// runner's actor resolver so actions run with their grant.
func (r *releaseRun) member(email string, roles []string, locales ...string) context.Context {
	r.t().Helper()
	m := r.w.member(email, roles, locales, identity.VendorID{})
	ctx := r.w.as(m)
	p, _ := authz.From(ctx)
	r.actors.principals[outbox.Actor(p.Actor.String())] = p
	return ctx
}

func (r *releaseRun) t() *testing.T { return r.w.t }

func (r *releaseRun) serving(project uuid.UUID, ctx context.Context) uuid.UUID {
	r.t().Helper()
	e, err := r.release.GetEnvironment(ctx, project, "production")
	if err != nil {
		r.t().Fatal(err)
	}
	return e.Current
}

// approvalOf is the approval the request's instance asked for.
func approvalOf(t *testing.T, request uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := env.Super.QueryRow(context.Background(),
		"SELECT id FROM workflow_approvals WHERE subject_kind = 'release_request' AND subject_id = $1 ORDER BY created_at DESC LIMIT 1",
		request).Scan(&id); err != nil {
		t.Fatalf("no approval was requested for release request %s: %v", request, err)
	}
	return id
}

func TestTwoDistinctApprovalsDeployAReleaseRequestAsTheLastApprover(t *testing.T) {
	r := newReleaseRun(t)
	project, _ := r.project(t)
	// The requester is an owner who is also a reviewer: eligible, so only
	// four-eyes can stop them.
	requester := r.member("olga@example.com", []string{"owner", "reviewer"})
	first := r.member("rita@example.com", []string{"reviewer"}, "de")
	second := r.member("ravi@example.com", []string{"reviewer"}, "de")

	stable, _, err := r.release.Publish(requester, project, releaseapp.PublishInput{Environment: "production"}, "")
	if err != nil {
		t.Fatal(err)
	}
	e, _ := r.release.GetEnvironment(requester, project, "production")
	if _, err := r.release.SetEnvironmentApproval(requester, project, "production", e.Version, &releasedomain.ApprovalPolicy{
		N: 2, From: releasedomain.ApprovalParty{Role: "reviewer"}, DistinctFromRequester: true,
	}); err != nil {
		t.Fatal(err)
	}

	rel, _, err := r.release.Publish(requester, project, releaseapp.PublishInput{Environment: "production", Note: "wants approval"}, "")
	var he *releasedomain.HeldError
	if !errors.As(err, &he) {
		t.Fatalf("publish into production = %v, want held for approval", err)
	}
	request := he.Request.ID
	r.drain(t)
	if got := r.serving(project, requester); got != stable.ID {
		t.Fatalf("the publish moved the pointer to %s", got)
	}

	// The request runs, unbound, on the seeded release-approval
	// definition, and asked the environment's two reviewers.
	insts := r.list(t, project)
	if len(insts) != 1 || insts[0].Kind != domain.SubjectReleaseRequest || insts[0].State != "pending" {
		t.Fatalf("instances = %+v", insts)
	}
	defs, err := r.defs.Definitions(r.manager(t), project)
	if err != nil {
		t.Fatal(err)
	}
	seeded := false
	for _, d := range defs {
		seeded = seeded || d.Name == defaults.ReleaseApprovalName && d.ID == insts[0].Definition
	}
	if !seeded {
		t.Fatalf("the instance does not run on the seeded %s: %+v", defaults.ReleaseApprovalName, defs)
	}
	approval := approvalOf(t, request)

	// Four-eyes: the requester's own approval is refused; a token's is
	// refused whatever its scopes.
	if _, err := r.work.Decide(requester, approval, domain.VerdictGranted, ""); !errors.Is(err, domain.ErrOwnText) {
		t.Fatalf("the requester approving: err = %v, want refused as their own", err)
	}
	token := authztest.Token(context.Background(), r.tenant, "read", "write", "publish", "admin")
	if _, err := r.work.Decide(token, approval, domain.VerdictGranted, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("a token approving: err = %v", err)
	}

	if _, err := r.work.Decide(first, approval, domain.VerdictGranted, ""); err != nil {
		t.Fatalf("the first approval: %v", err)
	}
	r.drain(t)
	if got := r.serving(project, requester); got != stable.ID {
		t.Fatalf("one approval moved the pointer to %s", got)
	}

	if _, err := r.work.Decide(second, approval, domain.VerdictGranted, ""); err != nil {
		t.Fatalf("the second approval: %v", err)
	}
	r.drain(t)
	if got := r.serving(project, requester); got != rel.ID {
		t.Fatalf("after two approvals production serves %s, want %s", got, rel.ID)
	}
	ds, _, err := r.release.ListDeployments(requester, project, "production", pageOf(1))
	if err != nil || ds[0].ReleaseID != rel.ID || ds[0].By != actorOf(second) || ds[0].Action != releasedomain.ActionPublish {
		t.Fatalf("deployment = %+v (%v), want the publish by the second approver", ds, err)
	}
	got, err := r.release.GetReleaseRequest(requester, project, request)
	if err != nil || got.State != releasedomain.RequestDeployed || got.DecidedBy != actorOf(second) {
		t.Fatalf("request = %+v (%v)", got, err)
	}
	done, err := r.instances.GetInstance(r.ctx(), insts[0].ID)
	if err != nil || done.Status != app.InstanceFinished || done.State != "deployed" {
		t.Fatalf("instance = %+v (%v), log %+v", done, err, r.log(t, insts[0].ID))
	}
	// The deploy ran as the second approver: the transition log says so.
	var deployRow *app.TransitionView
	for _, tr := range r.log(t, insts[0].ID) {
		if tr.To == "approved" {
			deployRow = &tr
		}
	}
	if deployRow == nil || deployRow.Actor != actorOf(second) || deployRow.Actions[0].Outcome != app.ActionDone {
		t.Fatalf("the approving transition = %+v", deployRow)
	}
}

func TestADenialEndsAReleaseRequest(t *testing.T) {
	r := newReleaseRun(t)
	project, _ := r.project(t)
	requester := r.member("olga@example.com", []string{"owner"})
	reviewer := r.member("rita@example.com", []string{"reviewer"})
	stable, _, _ := r.release.Publish(requester, project, releaseapp.PublishInput{Environment: "production"}, "")
	e, _ := r.release.GetEnvironment(requester, project, "production")
	if _, err := r.release.SetEnvironmentApproval(requester, project, "production", e.Version, &releasedomain.ApprovalPolicy{
		N: 1, From: releasedomain.ApprovalParty{Role: "reviewer"}, DistinctFromRequester: true,
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err := r.release.Publish(requester, project, releaseapp.PublishInput{Environment: "production"}, "")
	var he *releasedomain.HeldError
	if !errors.As(err, &he) {
		t.Fatalf("publish = %v", err)
	}
	r.drain(t)
	if _, err := r.work.Decide(reviewer, approvalOf(t, he.Request.ID), domain.VerdictDenied, "not this week"); err != nil {
		t.Fatal(err)
	}
	r.drain(t)
	got, err := r.release.GetReleaseRequest(requester, project, he.Request.ID)
	if err != nil || got.State != releasedomain.RequestDenied || got.DecidedBy != actorOf(reviewer) {
		t.Fatalf("request = %+v (%v), want denied by the reviewer", got, err)
	}
	if cur := r.serving(project, requester); cur != stable.ID {
		t.Fatalf("a denied request moved the pointer")
	}
	inst := r.list(t, project)[0]
	if inst.Status != app.InstanceFinished || inst.State != "denied" {
		t.Fatalf("instance = %+v", inst)
	}
}
