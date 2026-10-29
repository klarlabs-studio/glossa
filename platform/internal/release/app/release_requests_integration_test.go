//go:build integration

package app_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/delivery"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// Release approvals on Postgres (RFC 0006 §5.1): an environment's
// `approval` holds a publish or a promote as a release request and
// moves no pointer; the request deploys — through the same deployment
// path, as the approver — only once enough distinct people other than
// the requester have granted, and only if the publish gate, run again,
// passes. A forced publish still waits; a rollback never does. Who
// granted is Workflow's to say; here a ledger stands in for it.

// ledger is the Approvals port: the grants on a request, in the outbox
// spelling.
type ledger struct {
	mu     sync.Mutex
	grants []string
}

func (l *ledger) set(actors ...context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.grants = nil
	for _, a := range actors {
		l.grants = append(l.grants, actorOf(a))
	}
}

func (l *ledger) Granters(context.Context, uuid.UUID, uuid.UUID) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.grants), nil
}

func actorOf(ctx context.Context) string {
	p, _ := authz.From(ctx)
	return p.Actor.String()
}

// requireApproval makes environment require n reviewers, distinct from
// the requester, as an owner.
func (h *harness) requireApproval(t *testing.T, project uuid.UUID, environment string, n int) {
	t.Helper()
	e, err := h.svc.GetEnvironment(h.owner(), project, environment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SetEnvironmentApproval(h.owner(), project, environment, e.Version, &domain.ApprovalPolicy{
		N: n, From: domain.ApprovalParty{Role: "reviewer"}, DistinctFromRequester: true,
	}); err != nil {
		t.Fatalf("require approval: %v", err)
	}
}

func (h *harness) current(t *testing.T, project uuid.UUID, environment string) uuid.UUID {
	t.Helper()
	e, err := h.svc.GetEnvironment(h.owner(), project, environment)
	if err != nil {
		t.Fatal(err)
	}
	return e.Current
}

// held asserts err is a publish or promote held for approval and
// returns its request.
func held(t *testing.T, err error) domain.ReleaseRequest {
	t.Helper()
	var he *domain.HeldError
	if !errors.As(err, &he) || !errors.Is(err, domain.ErrApprovalRequired) {
		t.Fatalf("err = %v, want the move held for approval", err)
	}
	return he.Request
}

func outboxActors(t *testing.T, typ string) []string {
	t.Helper()
	rows, err := env.Super.Query(context.Background(), "SELECT actor FROM outbox_events WHERE event_type = $1 ORDER BY occurred_at, id", typ)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func TestPublishIntoAnApprovalEnvironmentMovesNoPointer(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	stable, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	if err != nil {
		t.Fatal(err)
	}

	// Only someone who may manage workflows changes who must approve; a
	// publisher who could switch it off would make it decorative. And
	// self-approval is not offered.
	e, _ := h.svc.GetEnvironment(h.owner(), p, "production")
	if _, err := h.svc.SetEnvironmentApproval(h.as("developer"), p, "production", e.Version, &domain.ApprovalPolicy{
		N: 1, From: domain.ApprovalParty{Role: "reviewer"}, DistinctFromRequester: true,
	}); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("a developer setting the approval: err = %v", err)
	}
	if _, err := h.svc.SetEnvironmentApproval(h.owner(), p, "production", e.Version, &domain.ApprovalPolicy{
		N: 1, From: domain.ApprovalParty{Role: "reviewer"},
	}); !errors.Is(err, domain.ErrInvalidApproval) {
		t.Fatalf("an approval without distinct_from_requester: err = %v", err)
	}
	h.requireApproval(t, p, "production", 2)
	if got, _ := h.svc.GetEnvironment(h.owner(), p, "production"); got.Approval == nil || got.Approval.N != 2 || got.Version != e.Version+1 {
		t.Fatalf("environment = %+v, want the approval stored and the version bumped", got)
	}

	owner := h.owner()
	rel, _, err := h.svc.Publish(owner, p, app.PublishInput{Environment: "production", Note: "wants approval"}, "key-1")
	req := held(t, err)
	if rel.ID == uuid.Nil || req.ReleaseID != rel.ID || req.State != domain.RequestPending || req.Action != domain.ActionPublish {
		t.Fatalf("release %s, request %+v", rel.ID, req)
	}
	if req.Requester != actorOf(owner) || req.Approval.N != 2 || !req.Verdict.Met || req.Override.Forced {
		t.Fatalf("request = %+v", req)
	}
	if got := h.current(t, p, "production"); got != stable.ID {
		t.Fatalf("production serves %s, want the previous %s: the pointer moved", got, stable.ID)
	}
	if n := count(t, "SELECT count(*) FROM release_deployments WHERE environment = 'production'"); n != 1 {
		t.Errorf("%d production deployments, want only the first", n)
	}
	if got := outboxActors(t, domain.EventRequestCreated); len(got) != 1 || got[0] != actorOf(owner) {
		t.Errorf("request created events name %v, want the requester", got)
	}
	if got := outboxActors(t, domain.EventPublished); len(got) != 1 {
		t.Errorf("%d release.published events, want only the first publish's", len(got))
	}

	// The same Idempotency-Key answers with the same request.
	_, _, err = h.svc.Publish(owner, p, app.PublishInput{Environment: "production", Note: "wants approval"}, "key-1")
	if again := held(t, err); again.ID != req.ID {
		t.Errorf("a replay answered request %s, want %s", again.ID, req.ID)
	}

	// A newer request replaces the pending one: two can never both
	// deploy.
	_, _, err = h.svc.Publish(owner, p, app.PublishInput{Environment: "production", Note: "newer"}, "")
	newer := held(t, err)
	first, err := h.svc.GetReleaseRequest(h.as("reviewer"), p, req.ID)
	if err != nil || first.State != domain.RequestWithdrawn || !strings.Contains(first.Reason, newer.ID.String()) {
		t.Fatalf("the first request = %+v (%v), want withdrawn in favour of %s", first, err, newer.ID)
	}
	pending, _, err := h.svc.ListReleaseRequests(h.as("reviewer"), p, "production", domain.RequestPending, pagination.Page{Size: 10})
	if err != nil || len(pending) != 1 || pending[0].ID != newer.ID {
		t.Fatalf("pending = %+v (%v)", pending, err)
	}

	// Staging requires nothing: its publishes move at once, as in M4.
	if _, _, err := h.svc.Publish(owner, p, app.PublishInput{Environment: "staging"}, ""); err != nil {
		t.Fatalf("a publish without approval: %v", err)
	}
}

func TestTwoDistinctApprovalsDeployAsTheLastApprover(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	stable, _, _ := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	h.requireApproval(t, p, "production", 2)
	requester := h.owner()
	rel, _, err := h.svc.Publish(requester, p, app.PublishInput{Environment: "production"}, "")
	req := held(t, err)

	first, second := h.as("reviewer"), h.as("reviewer")
	// Nothing deploys until something can say who approved.
	if _, err := h.svc.DeployRequest(second, p, req.ID); !errors.Is(err, app.ErrApprovalsUnavailable) {
		t.Fatalf("deploy with no approvals wired: err = %v", err)
	}
	l := &ledger{}
	h.svc.UseApprovals(l)

	for name, c := range map[string]struct {
		grants []context.Context
		as     context.Context
		want   error
	}{
		"the requester, granting and deploying": {[]context.Context{requester, first}, requester, domain.ErrApprovalNotMet},
		"one approval of two":                   {[]context.Context{first}, first, domain.ErrApprovalNotMet},
		"one person granting twice":             {[]context.Context{first, first}, first, domain.ErrApprovalNotMet},
		"the requester's grant counts for nothing": {
			[]context.Context{requester, first}, first, domain.ErrApprovalNotMet,
		},
		"a deployer who did not approve": {[]context.Context{first, second}, h.as("reviewer"), domain.ErrApprovalNotMet},
		"a token":                        {[]context.Context{first, second}, authztest.Token(context.Background(), h.tenant, "read", "write", "publish", "admin"), authz.ErrForbidden},
		"a developer":                    {[]context.Context{first, second}, h.as("developer"), authz.ErrForbidden},
	} {
		l.set(c.grants...)
		if _, err := h.svc.DeployRequest(c.as, p, req.ID); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
		if got := h.current(t, p, "production"); got != stable.ID {
			t.Fatalf("%s moved the pointer", name)
		}
	}

	l.set(first, requester, second)
	done, err := h.svc.DeployRequest(second, p, req.ID)
	if err != nil {
		t.Fatalf("deploy after two distinct approvals: %v", err)
	}
	if done.State != domain.RequestDeployed || done.DecidedBy != actorOf(second) {
		t.Fatalf("request = %+v", done)
	}
	if got := h.current(t, p, "production"); got != rel.ID {
		t.Fatalf("production serves %s, want the requested %s", got, rel.ID)
	}
	d := h.lastDeployment(t, p, "production")
	if d.ReleaseID != rel.ID || d.Action != domain.ActionPublish || d.By != actorOf(second) || d.Previous != stable.ID {
		t.Fatalf("deployment = %+v, want a publish by the last approver", d)
	}
	for _, typ := range []string{domain.EventRequestApproved, domain.EventRequestDeployed} {
		if got := outboxActors(t, typ); len(got) != 1 || got[0] != actorOf(second) {
			t.Errorf("%s names %v, want the last approver", typ, got)
		}
	}
	if got := outboxActors(t, domain.EventPublished); len(got) != 2 || got[1] != actorOf(second) {
		t.Errorf("release.published names %v, want the deploy's to name the last approver", got)
	}
	// The edge reads storage: the manifest names the new release.
	m, err := h.objects.Get(context.Background(), delivery.ManifestPath(p.String(), "production"), 1<<24)
	if err != nil || !strings.Contains(string(m), rel.ID.String()) {
		t.Errorf("production's manifest does not name %s (%v)", rel.ID, err)
	}

	// A redelivered deploy changes nothing.
	if again, err := h.svc.DeployRequest(second, p, req.ID); err != nil || again.State != domain.RequestDeployed {
		t.Fatalf("a second deploy: %+v, %v", again, err)
	}
	if n := count(t, "SELECT count(*) FROM release_deployments WHERE environment = 'production'"); n != 2 {
		t.Errorf("%d production deployments, want 2", n)
	}
}

func TestForcedPublishStillRequestsApproval(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	stable, _, _ := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	h.setPolicy(t, p, checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.RequiredLocales("de")},
	}})
	h.requireApproval(t, p, "production", 2)

	// Unforced, the gate refuses as before: no request is made.
	if _, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, ""); !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("an unforced publish over an unmet gate: err = %v", err)
	}
	if n := count(t, "SELECT count(*) FROM release_requests"); n != 0 {
		t.Fatalf("%d requests from a refused publish", n)
	}

	const reason = "the legal copy must ship with the campaign"
	rel, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production", Force: true, ForceReason: reason}, "")
	req := held(t, err)
	if !req.Override.Forced || req.Override.Reason != reason || req.Verdict.Met || len(req.Verdict.Unmet) == 0 {
		t.Fatalf("request = %+v, want forced with its reason and the unmet gate", req)
	}
	if got := h.current(t, p, "production"); got != stable.ID {
		t.Fatalf("a forced publish moved the pointer without approvals")
	}
	// The approvers see that it was forced and why.
	seen, err := h.svc.GetReleaseRequest(authztest.Member(context.Background(), h.tenant, []string{"reviewer"}, "de"), p, req.ID)
	if err != nil || seen.Override.Reason != reason {
		t.Fatalf("an approver reads %+v (%v)", seen, err)
	}

	// Approved, the force carries into the gate's second run, and the
	// deployment records it.
	a, b := h.as("reviewer"), h.as("reviewer")
	l := &ledger{}
	l.set(a, b)
	h.svc.UseApprovals(l)
	if _, err := h.svc.DeployRequest(b, p, req.ID); err != nil {
		t.Fatalf("deploy of the forced request: %v", err)
	}
	d := h.lastDeployment(t, p, "production")
	if d.ReleaseID != rel.ID || !d.Override.Forced || d.Override.Reason != reason {
		t.Fatalf("deployment = %+v, want the forced publish with its reason", d)
	}
}

func TestTheGateRunsAgainAtDeploy(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	stable, _, _ := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	h.requireApproval(t, p, "production", 2)
	_, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	req := held(t, err)
	if !req.Verdict.Met {
		t.Fatalf("the gate was met when the request was made: %+v", req.Verdict)
	}

	// The policy tightens while the request waits: the new policy
	// decides.
	h.setPolicy(t, p, checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.RequiredLocales("de")},
	}})
	a, b := h.as("reviewer"), h.as("reviewer")
	l := &ledger{}
	l.set(a, b)
	h.svc.UseApprovals(l)
	got, err := h.svc.DeployRequest(b, p, req.ID)
	var refused *domain.DeployRefusedError
	if !errors.As(err, &refused) || !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("deploy over a tightened gate: err = %v, want refused for policy_not_met", err)
	}
	if got.State != domain.RequestRefused || !strings.Contains(got.Reason, "de") || got.DecidedBy != actorOf(b) {
		t.Fatalf("request = %+v, want refused with why", got)
	}
	if cur := h.current(t, p, "production"); cur != stable.ID {
		t.Fatalf("a refused deploy moved the pointer")
	}
	if got := outboxActors(t, domain.EventRequestRefused); len(got) != 1 || got[0] != actorOf(b) {
		t.Errorf("refused events name %v", got)
	}
	// It is closed: a later deploy is refused, not retried into place.
	if _, err := h.svc.DeployRequest(b, p, req.ID); !errors.Is(err, domain.ErrRequestClosed) {
		t.Errorf("deploying a refused request: err = %v", err)
	}
}

func TestRollbackNeverWaitsForApproval(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	v1, _, _ := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	h.translate(t, p, "app.sub", "de", "Jeden Morgen frisch", "approved")
	h.drain(t)
	v2, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	if err != nil || v2.ID == v1.ID {
		t.Fatalf("second publish: %v", err)
	}
	h.requireApproval(t, p, "production", 2)

	e, err := h.svc.Rollback(h.owner(), p, "production", nil)
	if err != nil || e.Current != v1.ID {
		t.Fatalf("rollback = %+v (%v), want production on %s at once", e, err, v1.ID)
	}
	if n := count(t, "SELECT count(*) FROM release_requests"); n != 0 {
		t.Errorf("a rollback made %d requests", n)
	}
}

func TestPromoteIntoAnApprovalEnvironmentIsHeld(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	stable, _, _ := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	h.translate(t, p, "app.sub", "de", "Jeden Morgen frisch", "approved")
	h.drain(t)
	staged, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "staging"}, "")
	if err != nil {
		t.Fatal(err)
	}
	h.requireApproval(t, p, "production", 1)

	e, err := h.svc.Promote(h.owner(), p, "production", staged.ID, app.PromoteInput{})
	req := held(t, err)
	if e.Current != stable.ID || req.Action != domain.ActionPromote || req.ReleaseID != staged.ID {
		t.Fatalf("promote: env %+v, request %+v", e, req)
	}
	// Promoting what production already serves asks nobody.
	if _, err := h.svc.Promote(h.owner(), p, "production", stable.ID, app.PromoteInput{}); err != nil {
		t.Fatalf("promoting the current release: %v", err)
	}

	r := h.as("reviewer")
	l := &ledger{}
	l.set(r)
	h.svc.UseApprovals(l)
	if _, err := h.svc.DeployRequest(r, p, req.ID); err != nil {
		t.Fatal(err)
	}
	d := h.lastDeployment(t, p, "production")
	if d.ReleaseID != staged.ID || d.Action != domain.ActionPromote || d.By != actorOf(r) {
		t.Fatalf("deployment = %+v, want a promote by the approver", d)
	}
}

func TestDenyAndWithdraw(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	_, _, _ = h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	h.requireApproval(t, p, "production", 1)
	_, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	req := held(t, err)

	token := authztest.Token(context.Background(), h.tenant, "read", "write", "publish", "admin")
	if _, err := h.svc.DenyRequest(token, p, req.ID); !errors.Is(err, authz.ErrForbidden) {
		t.Fatalf("a token denying: err = %v", err)
	}
	no := authztest.Member(context.Background(), h.tenant, []string{"reviewer"}, "de")
	denied, err := h.svc.DenyRequest(no, p, req.ID)
	if err != nil || denied.State != domain.RequestDenied || denied.DecidedBy != actorOf(no) {
		t.Fatalf("deny = %+v (%v)", denied, err)
	}
	if _, err := h.svc.DenyRequest(no, p, req.ID); err != nil {
		t.Errorf("a redelivered denial: %v", err)
	}
	if _, err := h.svc.WithdrawRequest(h.owner(), p, req.ID, ""); !errors.Is(err, domain.ErrRequestClosed) {
		t.Errorf("withdrawing a denied request: err = %v", err)
	}

	_, _, err = h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	again := held(t, err)
	if _, err := h.svc.WithdrawRequest(h.as("reviewer"), p, again.ID, "not today"); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("a reviewer withdrawing: err = %v", err)
	}
	w, err := h.svc.WithdrawRequest(h.owner(), p, again.ID, "not today")
	if err != nil || w.State != domain.RequestWithdrawn || w.Reason != "not today" {
		t.Fatalf("withdraw = %+v (%v)", w, err)
	}
}

func TestAnotherTenantSeesNoReleaseRequests(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	_, _, _ = h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	h.requireApproval(t, p, "production", 1)
	_, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	req := held(t, err)

	other, err := env.SeedTenant(context.Background(), "globex")
	if err != nil {
		t.Fatal(err)
	}
	countIn := func(tenant tenancy.ID) int {
		var n int
		err := db.NewUnitOfWork(env.App).InTenantTx(tenancy.ContextWithTenant(context.Background(), tenant),
			func(ctx context.Context, tx *db.TenantTx) error {
				return tx.QueryRow(ctx, "SELECT count(*) FROM release_requests").Scan(&n)
			})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if mine, theirs := countIn(h.tenant), countIn(other); mine != 1 || theirs != 0 {
		t.Fatalf("release_requests under RLS: own tenant %d, another tenant %d; want 1 and 0", mine, theirs)
	}
	stranger := authztest.Member(context.Background(), other, []string{"owner"})
	if _, err := h.svc.GetReleaseRequest(stranger, p, req.ID); err == nil {
		t.Fatal("another tenant's owner read the request")
	}
	if _, err := h.svc.DeployRequest(stranger, p, req.ID); err == nil {
		t.Fatal("another tenant's owner deployed the request")
	}
}
