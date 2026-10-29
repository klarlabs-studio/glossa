//go:build integration

package app_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
)

// setPolicy stores the project's check-policy document, which is where
// the publish gate reads its requirements from.
func (h *harness) setPolicy(t *testing.T, project uuid.UUID, doc checkpolicy.Policy) {
	t.Helper()
	ctx := h.owner()
	id := catalogdomain.ProjectID(project)
	p, err := h.catalog.GetProject(ctx, id)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	settings := p.Settings
	settings.CheckPolicy = &doc
	if _, err := h.catalog.UpdateProject(ctx, id, p.Version, catalogdomain.ProjectChange{Settings: &settings}); err != nil {
		t.Fatalf("save policy: %v", err)
	}
}

// gated is a project with two messages, German complete in neither
// sense the policy could ask for: one of the two keys, approved.
func gated(t *testing.T, h *harness) uuid.UUID {
	t.Helper()
	p := h.project(t, []string{"de", "fr"}, map[string]string{
		"app.title": "Brotwerk",
		"app.sub":   "Fresh every morning",
	})
	h.translate(t, p, "app.title", "de", "Brotwerk", "approved")
	h.drain(t)
	return p
}

// lastDeployment is the newest entry in an environment's history.
func (h *harness) lastDeployment(t *testing.T, project uuid.UUID, environment string) domain.Deployment {
	t.Helper()
	ds, _, err := h.svc.ListDeployments(h.owner(), project, environment, pagination.Page{Size: 10})
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	if len(ds) == 0 {
		t.Fatalf("%s has no deployments", environment)
	}
	return ds[0]
}

func TestPublishRefusedBelowTheEnvironmentsCompleteness(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	h.setPolicy(t, p, checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.RequiredLocales("de")},
	}})

	_, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	if !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("Publish = %v, want ErrPolicyNotMet", err)
	}
	var notMet *domain.PolicyNotMetError
	if !errors.As(err, &notMet) || len(notMet.Unmet) != 1 || notMet.Unmet[0].Locale != "de" {
		t.Fatalf("refusal = %+v, want de named", err)
	}
	// Nothing was written: not the release, not the pointer, not even
	// the artifacts.
	if n := count(t, "SELECT count(*) FROM release_releases"); n != 0 {
		t.Errorf("%d releases recorded by a refused publish", n)
	}
	if got := len(h.objects.Keys()); got != 0 {
		t.Errorf("%d objects uploaded by a refused publish", got)
	}

	// The same project publishes to staging, which the policy does not
	// name: a document that says nothing about an environment gates
	// nothing there.
	if _, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "staging"}, ""); err != nil {
		t.Fatalf("publish to an ungated environment: %v", err)
	}
}

func TestForcedPublishWithoutAReasonIsRefused(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	h.setPolicy(t, p, checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.RequiredLocales("de")},
	}})

	for _, reason := range []string{"", "   "} {
		_, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production", Force: true, ForceReason: reason}, "")
		if !errors.Is(err, domain.ErrForceNeedsReason) {
			t.Fatalf("Publish(force, reason=%q) = %v, want ErrForceNeedsReason", reason, err)
		}
	}
	if n := count(t, "SELECT count(*) FROM release_releases"); n != 0 {
		t.Errorf("%d releases recorded by a refused force", n)
	}
}

func TestForcedPublishRecordsItsReason(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	h.setPolicy(t, p, checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.RequiredLocales("de")},
	}})
	const reason = "the launch is tomorrow; the German copy lands on Thursday"

	rel, _, err := h.svc.Publish(h.owner(), p,
		app.PublishInput{Environment: "production", Force: true, ForceReason: reason}, "")
	if err != nil {
		t.Fatalf("forced publish: %v", err)
	}

	// Read back through the application: the environment's history says
	// the publish went out against the policy, and why.
	d := h.lastDeployment(t, p, "production")
	if d.ReleaseID != rel.ID || d.Action != domain.ActionPublish {
		t.Fatalf("deployment = %+v, want the forced publish", d)
	}
	if !d.Override.Forced || d.Override.Reason != reason {
		t.Fatalf("override = %+v, want forced with the reason", d.Override)
	}
	// And durably: the row itself carries it, so the audit outlives the
	// process that wrote it.
	if n := count(t, "SELECT count(*) FROM release_deployments WHERE forced AND force_reason = $1", reason); n != 1 {
		t.Errorf("%d rows carry the reason, want 1", n)
	}

	// A publish that meets the gate records no override, however the
	// caller asked: "forced" in the history means the policy was
	// overridden, never that somebody passed a flag.
	h.translate(t, p, "app.sub", "de", "Jeden Morgen frisch", "approved")
	h.drain(t)
	if _, _, err := h.svc.Publish(h.owner(), p,
		app.PublishInput{Environment: "production", Force: true, ForceReason: "belt and braces"}, ""); err != nil {
		t.Fatalf("publish once de is complete: %v", err)
	}
	if d := h.lastDeployment(t, p, "production"); d.Override.Forced || d.Override.Reason != "" {
		t.Errorf("override = %+v on a publish that met the gate", d.Override)
	}
}

func TestEnvironmentWithoutRequireCompleteInheritsTheDocuments(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	// German is complete; French has nothing. The document requires
	// German, and the environment block names only a review state.
	h.translate(t, p, "app.sub", "de", "Jeden Morgen frisch", "approved")
	h.drain(t)
	h.setPolicy(t, p, checkpolicy.Policy{
		RequireComplete: []string{"de"},
		Environments: map[string]checkpolicy.Environment{
			"production": {RequireReview: checkpolicy.ReviewApproved},
		},
	})

	// "I did not say" is not "every locale": French is untranslated and
	// the publish goes through.
	if _, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, ""); err != nil {
		t.Fatalf("Publish = %v, want the document's [de] inherited, not every locale", err)
	}

	// Saying `require_complete: null` is how a project asks for every
	// locale, and then French does stop it.
	h.setPolicy(t, p, checkpolicy.Policy{
		RequireComplete: []string{"de"},
		Environments: map[string]checkpolicy.Environment{
			"production": {RequireComplete: checkpolicy.AllLocales()},
		},
	})
	_, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, "")
	if !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("Publish = %v, want ErrPolicyNotMet for fr", err)
	}
}

func TestPublishEnforcesRequireReview(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	// Completeness is not the question here: no locale is required.
	h.setPolicy(t, p, checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"development": {RequireComplete: checkpolicy.RequiredLocales(), RequireReview: checkpolicy.ReviewApproved},
		"production":  {RequireComplete: checkpolicy.RequiredLocales(), RequireReview: checkpolicy.ReviewApproved},
	}})

	// development ships drafts and needs_review, so its releases have
	// not reached approved.
	_, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "development"}, "")
	if !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("Publish to development = %v, want ErrPolicyNotMet", err)
	}
	// production ships approved text only, so it has.
	if _, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{Environment: "production"}, ""); err != nil {
		t.Fatalf("Publish to production = %v, want nil", err)
	}
	// And the refusal is overridable like any other, with a reason.
	if _, _, err := h.svc.Publish(h.owner(), p, app.PublishInput{
		Environment: "development", Force: true, ForceReason: "a designer needs the draft on the staging cluster",
	}, ""); err != nil {
		t.Fatalf("forced publish to development: %v", err)
	}
	if d := h.lastDeployment(t, p, "development"); !d.Override.Forced {
		t.Errorf("deployment = %+v, want forced", d)
	}
}

// A gate only publish honours is not a gate: a release production
// refuses must not reach production by way of staging.
func TestPromoteCannotWalkAroundThePublishGate(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	h.setPolicy(t, p, checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.RequiredLocales("de")},
	}})
	ctx := h.owner()

	// Straight at production: refused.
	if _, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, ""); !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("Publish to production = %v, want ErrPolicyNotMet", err)
	}
	// Staging is not named by the policy, so the same release is built
	// and published there. Staging ships approved text only, exactly as
	// production does, so nothing but the gate stands between them.
	stg, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "staging"}, "")
	if err != nil {
		t.Fatalf("publish to staging: %v", err)
	}

	// The long way round is refused too.
	_, err = h.svc.Promote(ctx, p, "production", stg.ID, app.PromoteInput{})
	if !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("Promote into production = %v, want ErrPolicyNotMet", err)
	}
	if n := count(t, "SELECT count(*) FROM release_deployments WHERE environment = 'production'"); n != 0 {
		t.Errorf("%d production deployments after a refused promote", n)
	}

	// Forcing it needs a reason, on the same terms a publish does.
	if _, err := h.svc.Promote(ctx, p, "production", stg.ID, app.PromoteInput{Force: true}); !errors.Is(err, domain.ErrForceNeedsReason) {
		t.Fatalf("forced promote without a reason = %v, want ErrForceNeedsReason", err)
	}

	const reason = "the German copy lands Thursday; marketing needs the page today"
	if _, err := h.svc.Promote(ctx, p, "production", stg.ID,
		app.PromoteInput{Force: true, ForceReason: reason}); err != nil {
		t.Fatalf("forced promote: %v", err)
	}
	d := h.lastDeployment(t, p, "production")
	if d.Action != domain.ActionPromote || d.ReleaseID != stg.ID {
		t.Fatalf("deployment = %+v, want the promotion", d)
	}
	if !d.Override.Forced || d.Override.Reason != reason {
		t.Fatalf("override = %+v, want forced with the reason", d.Override)
	}
	if n := count(t,
		"SELECT count(*) FROM release_deployments WHERE environment = 'production' AND forced AND force_reason = $1",
		reason); n != 1 {
		t.Errorf("%d production rows carry the reason, want 1", n)
	}
}

func TestPromoteRecordsNoOverrideWhenItMeetsTheGate(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	h.translate(t, p, "app.sub", "de", "Jeden Morgen frisch", "approved")
	h.drain(t)
	h.setPolicy(t, p, checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.RequiredLocales("de")},
	}})
	ctx := h.owner()

	stg, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "staging"}, "")
	if err != nil {
		t.Fatalf("publish to staging: %v", err)
	}
	if _, err := h.svc.Promote(ctx, p, "production", stg.ID,
		app.PromoteInput{Force: true, ForceReason: "belt and braces"}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if d := h.lastDeployment(t, p, "production"); d.Override.Forced || d.Override.Reason != "" {
		t.Errorf("override = %+v on a promote that met the gate", d.Override)
	}
}

// Covers stops a release holding review states the destination
// excludes. It cannot stop one whose destination happens to ship those
// states but whose policy block asks for approved text; the gate can.
func TestPromoteEnforcesRequireReviewWhereCoversCannot(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	ctx := h.owner()

	// Built before the policy names anything, so both publishes go
	// through: preview ships drafts, production approved text only.
	prev, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "preview"}, "")
	if err != nil {
		t.Fatalf("publish to preview: %v", err)
	}
	prod, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, "")
	if err != nil {
		t.Fatalf("publish to production: %v", err)
	}

	// development ships everything, so Covers lets either release in.
	h.setPolicy(t, p, checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"development": {RequireComplete: checkpolicy.RequiredLocales(), RequireReview: checkpolicy.ReviewApproved},
	}})
	if _, err := h.svc.Promote(ctx, p, "development", prev.ID, app.PromoteInput{}); !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("Promote a preview release = %v, want ErrPolicyNotMet", err)
	}
	if _, err := h.svc.Promote(ctx, p, "development", prod.ID, app.PromoteInput{}); err != nil {
		t.Fatalf("Promote an approved-only release = %v, want nil", err)
	}
}

// Rolling back never makes an environment newly serve text that did not
// pass the gate: its targets are releases it already served. Tightening
// a policy must not strand an environment on the release that broke it.
func TestRollbackIsNotGated(t *testing.T) {
	h := newHarness(t)
	p := gated(t, h)
	ctx := h.owner()

	first, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, "first")
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	h.push(t, p, map[string]string{"app.cta": "Order now"})
	if _, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, "second"); err != nil {
		t.Fatalf("second publish: %v", err)
	}

	// The policy tightens to something neither release meets.
	h.setPolicy(t, p, checkpolicy.Policy{Environments: map[string]checkpolicy.Environment{
		"production": {RequireComplete: checkpolicy.AllLocales()},
	}})
	if _, _, err := h.svc.Publish(ctx, p, app.PublishInput{Environment: "production"}, ""); !errors.Is(err, domain.ErrPolicyNotMet) {
		t.Fatalf("publish under the tightened policy = %v, want ErrPolicyNotMet", err)
	}

	env, err := h.svc.Rollback(ctx, p, "production", nil)
	if err != nil {
		t.Fatalf("Rollback = %v, want nil: the way back is not a way in", err)
	}
	if env.Current != first.ID {
		t.Fatalf("production serves %s, want the first release", env.Current)
	}
	if d := h.lastDeployment(t, p, "production"); d.Action != domain.ActionRollback || d.Override.Forced {
		t.Errorf("deployment = %+v, want an unforced rollback", d)
	}
}
