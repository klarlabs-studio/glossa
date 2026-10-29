//go:build integration

package app_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/apiv1"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/release/adapters/httpapi"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// The publish gate through the HTTP edge (RFC 0005 §4.1, §12.5).
//
// The override was built at both ends and joined at neither:
// `app.Service.Publish` implemented it, `openapi.yaml` published `force`
// and `force_reason` on the publish body, the database had the audited
// column with its CHECK — and the handler built
// `app.PublishInput{Environment, Note}` and dropped both fields. So the
// gate could refuse and nobody could override it through the API, which
// is the only surface there is: `glossa release` has no flag for it. The
// promote path had the same hole, with a comment saying the spec had yet
// to carry the fields it already carried.
//
// These tests therefore go through the edge, with the app-level ones in
// publish_gate_integration_test.go left where they are: each of those
// passed throughout, which is exactly why they could not see this.

// api is Release's HTTP edge over this harness's service.
func (h *harness) api() *httpapi.API { return httpapi.New(h.svc) }

// publish calls the edge as a request would.
func (h *harness) publish(t *testing.T, project uuid.UUID, body apiv1.PublishRelease) (apiv1.Release, error) {
	t.Helper()
	resp, err := h.api().PublishRelease(h.owner(), apiv1.PublishReleaseRequestObject{
		Tenant: h.tenant.String(), Project: project.String(), Body: &body,
	})
	if err != nil {
		return apiv1.Release{}, err
	}
	created, ok := resp.(apiv1.PublishRelease201JSONResponse)
	if !ok {
		t.Fatalf("PublishRelease answered %T, want a 201", resp)
	}
	return created.Body, nil
}

// problemCode is the problem code an edge error carries.
func problemCode(t *testing.T, err error) problem.Code {
	t.Helper()
	var p *problem.Details
	if !errors.As(err, &p) {
		t.Fatalf("error = %v (%T), want a problem", err, err)
	}
	return p.Code
}

// gatedProject is a project production refuses: German is one message
// short of complete, and the environment requires it.
func gatedProject(t *testing.T, h *harness) uuid.UUID {
	t.Helper()
	p := gated(t, h)
	h.setPolicy(t, p, checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.RequiredLocales("de")},
	}})
	return p
}

func TestPublishThroughTheAPICanBeForcedWithAReason(t *testing.T) {
	h := newHarness(t)
	p := gatedProject(t, h)
	const reason = "the launch is tomorrow; the German copy lands on Thursday"

	// Unforced, the gate refuses.
	if _, err := h.publish(t, p, apiv1.PublishRelease{Environment: "production"}); err == nil {
		t.Fatal("the gate let an incomplete release through")
	} else if got := problemCode(t, err); got != "policy_not_met" {
		t.Fatalf("code = %q, want policy_not_met", got)
	}

	// A force with no reason is refused for want of one — and is
	// refused as that, not as the gate again. The two are different
	// answers to different questions, and a caller that gets
	// `policy_not_met` back from a forced publish has been told the
	// override does not exist.
	_, err := h.publish(t, p, apiv1.PublishRelease{Environment: "production", Force: ptr(true)})
	if err == nil {
		t.Fatal("a forced publish with no reason went out")
	}
	if got := problemCode(t, err); got != "force_reason_required" {
		t.Fatalf("code = %q, want force_reason_required", got)
	}

	// With a reason it goes out.
	rel, err := h.publish(t, p, apiv1.PublishRelease{
		Environment: "production", Force: ptr(true), ForceReason: ptr(reason),
	})
	if err != nil {
		t.Fatalf("forced publish through the API: %v", err)
	}

	// And the exception is on the record, which is the whole reason the
	// override has a reason at all: a gate with no escape hatch gets
	// routed around by switching the requirement off, and that leaves
	// nothing behind.
	d := h.lastDeployment(t, p, "production")
	if d.ReleaseID.String() != rel.Id || !d.Override.Forced || d.Override.Reason != reason {
		t.Fatalf("deployment = %+v, want the forced publish with its reason", d)
	}
}

// The history the API serves says it too. An override recorded in a
// column nobody can read back makes the deployment history look policed
// while the exception stays invisible.
func TestTheDeploymentHistoryReportsTheOverride(t *testing.T) {
	h := newHarness(t)
	p := gatedProject(t, h)
	const reason = "signed off in the release meeting"
	if _, err := h.publish(t, p, apiv1.PublishRelease{
		Environment: "production", Force: ptr(true), ForceReason: ptr(reason),
	}); err != nil {
		t.Fatalf("forced publish: %v", err)
	}

	resp, err := h.api().ListDeployments(h.owner(), apiv1.ListDeploymentsRequestObject{
		Tenant: h.tenant.String(), Project: p.String(), Environment: "production",
	})
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	list, ok := resp.(apiv1.ListDeployments200JSONResponse)
	if !ok || len(list.Items) == 0 {
		t.Fatalf("ListDeployments answered %T", resp)
	}
	d := list.Items[0]
	if !d.Forced || d.ForceReason == nil || *d.ForceReason != reason {
		t.Fatalf("deployment = %+v, want forced with its reason", d)
	}
}

// Promotion is gated exactly as publishing is, and overridable on
// exactly the same terms: a release production refused may not reach
// production by way of staging, and the way through is the same
// reasoned override.
func TestPromoteThroughTheAPICanBeForcedWithAReason(t *testing.T) {
	h := newHarness(t)
	p := gatedProject(t, h)
	const reason = "the same launch, by the same decision"

	stg, err := h.publish(t, p, apiv1.PublishRelease{Environment: "staging"})
	if err != nil {
		t.Fatalf("publish to staging: %v", err)
	}

	promote := func(body apiv1.Promotion) (apiv1.PromoteReleaseResponseObject, error) {
		return h.api().PromoteRelease(h.owner(), apiv1.PromoteReleaseRequestObject{
			Tenant: h.tenant.String(), Project: p.String(), Environment: "production", Body: &body,
		})
	}
	if _, err := promote(apiv1.Promotion{ReleaseId: stg.Id}); err == nil {
		t.Fatal("the gate let staging's release into production")
	} else if got := problemCode(t, err); got != "policy_not_met" {
		t.Fatalf("code = %q, want policy_not_met", got)
	}
	if _, err := promote(apiv1.Promotion{ReleaseId: stg.Id, Force: ptr(true)}); err == nil {
		t.Fatal("a forced promote with no reason went through")
	} else if got := problemCode(t, err); got != "force_reason_required" {
		t.Fatalf("code = %q, want force_reason_required", got)
	}

	if _, err := promote(apiv1.Promotion{ReleaseId: stg.Id, Force: ptr(true), ForceReason: ptr(reason)}); err != nil {
		t.Fatalf("forced promote through the API: %v", err)
	}
	d := h.lastDeployment(t, p, "production")
	if d.Action != domain.ActionPromote || !d.Override.Forced || d.Override.Reason != reason {
		t.Fatalf("deployment = %+v, want the forced promote with its reason", d)
	}
}

// A publish that meets the gate records no override, however the caller
// asked: "forced" in the history means the policy was overridden, never
// that somebody passed a flag. The edge must not turn the flag into the
// record on its own.
func TestForceOnAPublishThatMeetsTheGateRecordsNothing(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	// No environment requires anything, so nothing is overridden.
	h.setPolicy(t, p, checkpolicy.Policy{RequireComplete: []string{}})

	if _, err := h.publish(t, p, apiv1.PublishRelease{
		Environment: "production", Force: ptr(true), ForceReason: ptr("belt and braces"),
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if d := h.lastDeployment(t, p, "production"); d.Override.Forced || d.Override.Reason != "" {
		t.Errorf("override = %+v on a publish that met the gate", d.Override)
	}
}
