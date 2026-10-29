package httpapi_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/apiv1/apiconv"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/workflow/adapters/httpapi"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// releases is Release's side of release requests, as Workflow reads it:
// the facts of the requests a project has.
type releases struct {
	project  uuid.UUID
	requests map[uuid.UUID]app.ReleaseRequestFacts
}

func (r *releases) Request(_ context.Context, project, id uuid.UUID) (app.ReleaseRequestFacts, error) {
	f, ok := r.requests[id]
	if !ok || project != r.project {
		return app.ReleaseRequestFacts{}, app.ErrUnavailable
	}
	return f, nil
}

func (r *releases) Deploy(context.Context, uuid.UUID, uuid.UUID) (string, error) { return "", nil }
func (r *releases) Deny(context.Context, uuid.UUID, uuid.UUID) error             { return nil }

// decideReleaseRequest (RFC 0006 §5.1): a decision reached by the
// release request rather than by its approval — human-only,
// environment-scoped, four-eyes against the requester, and only on a
// pending request whose workflow has asked.
func TestDecidingAReleaseRequestOverTheAPI(t *testing.T) {
	f := newFixture(t, false)
	requester := f.person([]string{"developer"}, nil, uuid.Nil)
	p, _ := authz.From(requester)
	pending, closed, unasked := uuid.New(), uuid.New(), uuid.New()
	rel := &releases{project: f.project, requests: map[uuid.UUID]app.ReleaseRequestFacts{
		pending: {Environment: "production", Requester: p.Actor.String(), State: "pending", Required: 2, From: domain.Party{Role: "reviewer"}},
		closed:  {Environment: "production", Requester: p.Actor.String(), State: "withdrawn", Required: 2, From: domain.Party{Role: "reviewer"}},
		unasked: {Environment: "production", Requester: p.Actor.String(), State: "pending", Required: 2, From: domain.Party{Role: "reviewer"}},
	}}
	clock := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	work := app.NewWorkService(f.work, f.dir, f.authors, app.WithWorkClock(func() time.Time { return clock }),
		app.WithReleaseRequests(rel))
	api := httpapi.New(nil, nil, nil, work)

	// The workflow asks two reviewers, as its request_approval_as_required
	// would, for the pending request and the one later withdrawn.
	for _, id := range []uuid.UUID{pending, closed} {
		if _, err := work.RequestApprovalForInstance(f.owner, app.WorkflowApproval{
			InstanceID: uuid.New(), ProjectID: f.project,
			Subject: domain.ApprovalSubject{Kind: domain.SubjectReleaseRequest, ID: id},
			Params:  domain.RequestApproval{N: 2, From: domain.Party{Role: "reviewer"}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	decide := func(ctx context.Context, project, request string, decision string) response {
		resp, err := api.DecideReleaseRequest(ctx, apiv1.DecideReleaseRequestRequestObject{
			Tenant: f.tenant.String(), Project: project, ReleaseRequest: request,
			Body: &apiv1.CreateApprovalDecision{Decision: apiv1.CreateApprovalDecisionDecision(decision), Reason: apiconv.Ptr("ship it")},
		})
		return render(t, resp, err)
	}
	project := f.project.String()
	decide(f.anonymous, project, pending.String(), "granted").want(t, http.StatusUnauthorized, "unauthenticated")
	// No token approves a release, whatever its scopes (§9.3).
	decide(authztest.Token(context.Background(), f.tenant, "read", "write", "publish", "admin", "workflows"), project, pending.String(), "granted").
		want(t, http.StatusForbidden, "person_required")
	decide(f.reader, project, pending.String(), "granted").want(t, http.StatusForbidden, "forbidden")
	// The requester never approves their own release.
	reviewerRequester := f.person([]string{"reviewer"}, nil, uuid.Nil)
	rp, _ := authz.From(reviewerRequester)
	rel.requests[pending] = app.ReleaseRequestFacts{Environment: "production", Requester: rp.Actor.String(), State: "pending",
		Required: 2, From: domain.Party{Role: "reviewer"}}
	decide(reviewerRequester, project, pending.String(), "granted").want(t, http.StatusForbidden, "own_text")

	first := f.person([]string{"reviewer"}, []string{"de"}, uuid.Nil)
	second := f.person([]string{"reviewer"}, nil, uuid.Nil)
	decide(first, project, uuid.NewString(), "granted").want(t, http.StatusNotFound, "not_found")
	decide(first, f.other.String(), pending.String(), "granted").want(t, http.StatusNotFound, "not_found")
	decide(first, project, "not-an-id", "granted").want(t, http.StatusNotFound, "not_found")
	decide(first, project, closed.String(), "granted").want(t, http.StatusConflict, "release_request_closed")
	decide(first, project, unasked.String(), "granted").want(t, http.StatusConflict, "approval_not_requested")
	decide(first, project, pending.String(), "perhaps").want(t, http.StatusUnprocessableEntity, "invalid_approval")

	// A reviewer limited to de approves a release: it ships every locale,
	// and approvals.decide is environment-scoped for it (§4.2).
	var got apiv1.Approval
	decide(first, project, pending.String(), "granted").want(t, http.StatusCreated, "").decode(t, &got)
	if got.Subject != "release_request" || got.SubjectId != pending.String() || got.State != "pending" || len(got.Decisions) != 1 {
		t.Fatalf("after one grant = %+v", got)
	}
	decide(first, project, pending.String(), "granted").want(t, http.StatusCreated, "").decode(t, &got)
	if len(got.Decisions) != 1 {
		t.Fatalf("one person counted twice: %+v", got.Decisions)
	}
	decide(second, project, pending.String(), "granted").want(t, http.StatusCreated, "").decode(t, &got)
	if got.State != "granted" || len(got.Decisions) != 2 {
		t.Fatalf("after two grants = %+v", got)
	}
	decide(f.person([]string{"reviewer"}, nil, uuid.Nil), project, pending.String(), "denied").
		want(t, http.StatusConflict, "approval_closed")

	// Without Release wired, nothing is decided.
	bare := httpapi.New(nil, nil, nil, app.NewWorkService(f.work, f.dir, f.authors))
	resp, err := bare.DecideReleaseRequest(first, apiv1.DecideReleaseRequestRequestObject{
		Tenant: f.tenant.String(), Project: project, ReleaseRequest: pending.String(),
		Body: &apiv1.CreateApprovalDecision{Decision: "granted"},
	})
	render(t, resp, err).want(t, http.StatusForbidden, "forbidden")
}
