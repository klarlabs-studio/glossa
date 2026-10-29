//go:build integration

package app_test

import (
	"errors"
	"testing"
	"time"

	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The policy surface against Postgres and the real Catalog: the
// document goes where every reader of the policy already looks, and the
// record of how it got there is Quality's own append-only table
// (migration 0031, RFC 0005 §4.3).

// TestPolicyStartsAtTheDocumentedDefault: a project that has never
// saved one reads as the default — every locale required, errors fail —
// at version 0, rather than as an error or an empty document.
func TestPolicyStartsAtTheDocumentedDefault(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")

	state, err := h.svc.CheckPolicy(h.developer(), project)
	if err != nil {
		t.Fatalf("CheckPolicy: %v", err)
	}
	if state.Policy.Version != 0 || state.CreatedBy != "" {
		t.Errorf("state = %+v, want version 0 with no author", state)
	}
	if !state.Policy.Requires("de") || !state.Policy.Fails(checkpolicy.Error) {
		t.Error("the default stopped being every locale required and errors failing")
	}
	if _, _, err := h.svc.ListPolicyVersions(h.developer(), project, firstPage()); err != nil {
		t.Fatalf("ListPolicyVersions: %v", err)
	}
}

// TestSavePolicyIsReadableEverywhere: the saved document is what
// Catalog stores — which is what `glossa check` and the pull-request
// check read — and the version is Quality's record of who saved it.
func TestSavePolicyIsReadableEverywhere(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")

	saved, err := h.svc.SavePolicy(h.developer(), project, app.SavePolicy{
		Policy: checkpolicy.Policy{
			FailOn: checkpolicy.Warning,
			Rules: []checkpolicy.Rule{
				{Selector: checkpolicy.Selector{Layer: "terminology", Namespace: "legal"}, Severity: checkpolicy.Error},
				{Selector: checkpolicy.Selector{Layer: "source"}, Severity: checkpolicy.Off},
			},
			Environments: map[string]checkpolicy.Environment{
				"production": {RequireComplete: checkpolicy.RequiredLocales("en"), RequireReview: "approved"},
			},
		},
	})
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if saved.State.Policy.Version != 1 {
		t.Fatalf("version = %d, want 1", saved.State.Policy.Version)
	}

	// Catalog has it, with the rules and the environment intact.
	p, err := h.catalog.GetProject(h.developer(), catalogdomain.ProjectID(project))
	if err != nil {
		t.Fatal(err)
	}
	stored := p.Settings.Policy()
	if stored.Version != 1 || len(stored.Rules) != 2 || stored.ReviewIn("production") != "approved" {
		t.Fatalf("Catalog stores %+v, want the document that grades", stored)
	}
	if stored.FailOn != checkpolicy.Warning {
		t.Errorf("fail_on = %q, want warning", stored.FailOn)
	}
	if !checkpolicy.Advisory("linguistic") || stored.Computes("source", "") {
		t.Error("a rule that switched a layer off did not survive the round trip")
	}

	// And Quality has the record, with an author and a time.
	vs, _, err := h.svc.ListPolicyVersions(h.developer(), project, firstPage())
	if err != nil {
		t.Fatalf("ListPolicyVersions: %v", err)
	}
	if len(vs) != 1 || vs[0].Version != 1 || vs[0].CreatedBy == "" || vs[0].CreatedAt.IsZero() {
		t.Fatalf("history = %+v, want one authored row", vs)
	}
	if len(vs[0].Policy.Rules) != 2 {
		t.Errorf("stored version = %+v, want the document as it was saved", vs[0].Policy)
	}
	one, err := h.svc.PolicyVersion(h.developer(), project, 1)
	if err != nil || !one.Policy.Equal(vs[0].Policy) {
		t.Fatalf("PolicyVersion(1) = %+v, %v", one, err)
	}
}

// TestSavePolicySupersedesAndPins: every save is a new version, and a
// save with a grace keeps the version it replaced behind it, so a pull
// request opened before it keeps grading against what its author saw
// (RFC 0005 §4.3).
func TestSavePolicySupersedesAndPins(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")

	for range 3 {
		if _, err := h.svc.SavePolicy(h.developer(), project, app.SavePolicy{
			Policy: checkpolicy.Policy{FailOn: checkpolicy.Error},
		}); err != nil {
			t.Fatalf("SavePolicy: %v", err)
		}
		h.clock.advance(time.Hour)
	}

	state, err := h.svc.CheckPolicy(h.developer(), project)
	if err != nil {
		t.Fatalf("CheckPolicy: %v", err)
	}
	if state.Policy.Version != 3 {
		t.Fatalf("version = %d, want 3", state.Policy.Version)
	}
	if state.Policy.Previous == nil || state.Policy.Previous.Version != 2 {
		t.Fatalf("previous = %+v, want version 2: the history in the document is one version deep",
			state.Policy.Previous)
	}
	opened := state.Policy.EffectiveFrom.Add(-time.Hour)
	if got := state.Policy.Effective(opened, h.clock.now()); got.Version != 2 {
		t.Errorf("a pull request opened before the save grades against version %d, want 2", got.Version)
	}
	if got := state.Policy.Effective(time.Time{}, h.clock.now()); got.Version != 3 {
		t.Errorf("a run that is no pull request's grades against version %d, want 3", got.Version)
	}

	vs, _, err := h.svc.ListPolicyVersions(h.developer(), project, firstPage())
	if err != nil {
		t.Fatalf("ListPolicyVersions: %v", err)
	}
	if len(vs) != 3 || vs[0].Version != 3 || vs[2].Version != 1 {
		t.Fatalf("history = %+v, want three rows newest first", vs)
	}
	// Every version the record keeps is the version itself; the chain
	// belongs to the live document.
	for _, v := range vs {
		if v.Policy.Previous != nil {
			t.Errorf("version %d stored a history of its own", v.Version)
		}
	}
}

// TestDryRunStoresNothingAndNamesThePullRequest: the preview is the
// whole point — somebody sees the red pull requests before causing
// them — and it must leave the project exactly as it found it.
func TestDryRunStoresNothingAndNamesThePullRequest(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	record(t, h, project, "feature/pay",
		finding(domain.LayerTerminology, "term_forbidden",
			domain.Locus{Key: "checkout.pay", Locale: "de", Namespace: "legal"}, domain.Warning))

	candidate := app.SavePolicy{
		DryRun: true,
		Policy: checkpolicy.Policy{Rules: []checkpolicy.Rule{
			{Selector: checkpolicy.Selector{Layer: "terminology"}, Severity: checkpolicy.Error},
		}},
	}
	saved, err := h.svc.SavePolicy(h.developer(), project, candidate)
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if !saved.DryRun {
		t.Error("a dry run did not say it was one")
	}
	if saved.Impact.Runs != 1 || saved.Impact.Raised != 1 || saved.Impact.NewlyFailing != 1 {
		t.Errorf("impact = %+v, want the one warning raised to a failure", saved.Impact)
	}
	if got := saved.Impact.NewlyFailingRefs; len(got) != 1 || got[0] != "feature/pay" {
		t.Errorf("newly failing refs = %v, want [feature/pay]", got)
	}

	state, err := h.svc.CheckPolicy(h.developer(), project)
	if err != nil {
		t.Fatalf("CheckPolicy: %v", err)
	}
	if state.Policy.Version != 0 || len(state.Policy.Rules) != 0 {
		t.Errorf("a dry run changed the policy: %+v", state.Policy)
	}
	vs, _, err := h.svc.ListPolicyVersions(h.developer(), project, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Errorf("a dry run wrote %d versions", len(vs))
	}
}

// TestWaivedFindingsSurviveEveryCandidate: a waived finding is already
// accepted and can never fail a run, so no policy a preview measures
// can turn it into one.
func TestWaivedFindingsSurviveEveryCandidate(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	f := finding(domain.LayerTerminology, "term_forbidden",
		domain.Locus{Key: "checkout.pay", Locale: "de"}, domain.Warning, at(1))
	record(t, h, project, "main", f)
	if _, _, err := h.svc.CreateWaiver(h.developer(), project, app.CreateWaiver{
		Fingerprint: f.Fingerprint, Reason: "Login is the German term",
	}); err != nil {
		t.Fatalf("CreateWaiver: %v", err)
	}

	saved, err := h.svc.SavePolicy(h.developer(), project, app.SavePolicy{
		DryRun: true,
		Policy: checkpolicy.Policy{Rules: []checkpolicy.Rule{
			{Selector: checkpolicy.Selector{Layer: "terminology"}, Severity: checkpolicy.Error},
		}},
	})
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if saved.Impact.NewlyFailing != 0 || saved.Impact.Targets != 0 {
		t.Errorf("impact = %+v, want a waived finding to be outside every candidate's reach", saved.Impact)
	}
}

// TestCIImportsAPolicyThatNamesLocales is `glossa policy import` from
// CI, the whole way down: a CI token holds catalog.read and
// catalog.write and nothing else, and a require_complete that names
// locales has to save with exactly that. Validating those locales
// against Localization is the write's own invariant check, not a read
// the caller has to be entitled to separately — but it is still a
// check, so a locale the project lacks is refused.
func TestCIImportsAPolicyThatNamesLocales(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	h.addLocale(t, project, "de")
	ci := h.ci()

	saved, err := h.svc.SavePolicy(ci, project, app.SavePolicy{
		Policy: checkpolicy.Policy{RequireComplete: []string{"de"}, FailOn: checkpolicy.Error},
	})
	if err != nil {
		t.Fatalf("CI imports a policy naming locales: %v", err)
	}
	if !saved.State.Policy.Requires("de") || saved.State.Policy.Requires("fr") {
		t.Errorf("saved policy requires %v", saved.State.Policy.RequireComplete)
	}
	state, err := h.svc.CheckPolicy(ci, project)
	if err != nil || !state.Policy.Requires("de") {
		t.Errorf("CI reads back %+v, %v", state.Policy, err)
	}

	_, err = h.svc.SavePolicy(ci, project, app.SavePolicy{
		Policy: checkpolicy.Policy{RequireComplete: []string{"ja"}, FailOn: checkpolicy.Error},
	})
	if !errors.Is(err, checkpolicy.ErrUnknownLocale) {
		t.Errorf("CI names a locale the project lacks = %v, want ErrUnknownLocale", err)
	}
}

// TestPolicyWriteNeedsCatalogWrite: changing what a project's check
// concludes is a catalog write; reading the policy is a catalog read.
func TestPolicyWriteNeedsCatalogWrite(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")

	_, err := h.svc.SavePolicy(h.translator(), project, app.SavePolicy{Policy: checkpolicy.Policy{}})
	if !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("SavePolicy as a translator = %v, want forbidden", err)
	}
	if _, err := h.svc.CheckPolicy(h.translator(), project); err != nil {
		t.Errorf("CheckPolicy as a translator = %v, want it allowed: a translator may read the catalog", err)
	}
}

// TestPolicyIsTenantScoped: another tenant's project is not found, and
// its versions are not readable — the table is under forced RLS like
// every other one Quality owns.
func TestPolicyIsTenantScoped(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	if _, err := h.svc.SavePolicy(h.developer(), project,
		app.SavePolicy{Policy: checkpolicy.Policy{}}); err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}

	other := harnessFor(t, "other")
	if _, err := other.svc.CheckPolicy(other.developer(), project); !errors.Is(err, app.ErrProjectNotFound) {
		t.Errorf("another tenant read the policy: %v", err)
	}
	if _, _, err := other.svc.ListPolicyVersions(other.developer(), project, firstPage()); err == nil {
		t.Error("another tenant listed the versions")
	}
}
