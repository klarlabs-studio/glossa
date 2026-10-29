package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The policy surface against a Catalog that holds the document and a
// store that holds the history. These ask the narrower question — what
// the service reads, validates, previews and writes, and in which order
// — without a container, so the answer is there on every `go test ./...`.

func writeCtx(t *testing.T) context.Context {
	t.Helper()
	return authztest.Token(t.Context(), tenancy.NewID(), "read", "write")
}

// policyStore is a store with one failing run of `main` and one clean
// run of a feature branch, which is what an impact preview needs to
// have anything to say.
func policyStore() *fakeStore {
	s := &fakeStore{}
	feature := domain.CheckRun{
		ID: uuid.New(), Ref: "feature/pay", Trigger: domain.TriggerPullRequest,
		Conclusion: domain.ConclusionSuccess, Layers: []domain.Layer{domain.LayerTerminology},
		StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(),
	}
	s.runs = []domain.CheckRun{feature}
	s.run = feature
	s.rows = []app.FindingRecord{{
		Finding: domain.New(domain.Finding{
			Layer: domain.LayerTerminology, Code: "term_forbidden", Severity: domain.Warning,
			Locus: domain.Locus{Key: "checkout.pay", Locale: "de", Namespace: "legal"},
		}),
		SortKey: "1\x01terminology\x01de\x01checkout.pay",
	}}
	return s
}

// TestSavePolicyNumbersTheVersionAndRecordsWhoSavedIt: a save is a new
// version, it goes to Catalog — where the pull-request check and
// `glossa check` already read it — and Quality keeps the record of who
// took the decision (RFC 0005 §4.2, §4.3).
func TestSavePolicyNumbersTheVersionAndRecordsWhoSavedIt(t *testing.T) {
	store := policyStore()
	svc, catalog, project := serviceAndCatalog(store)
	catalog.policy = checkpolicy.Policy{Schema: checkpolicy.Schema, Version: 2, FailOn: checkpolicy.Error}

	saved, err := svc.SavePolicy(writeCtx(t), project, app.SavePolicy{
		Policy: checkpolicy.Policy{FailOn: checkpolicy.Warning},
	})
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if saved.DryRun {
		t.Error("a save that was not a dry run says it was")
	}
	if saved.State.Policy.Version != 3 {
		t.Errorf("version = %d, want 3", saved.State.Policy.Version)
	}
	if catalog.saved == nil || catalog.saved.Version != 3 {
		t.Fatalf("Catalog was handed %+v; the document that grades is Catalog's", catalog.saved)
	}
	if catalog.savedIfMatch != 1 {
		t.Errorf("if-match = %d, want the project version the read saw", catalog.savedIfMatch)
	}
	if len(store.versions) != 1 || store.versions[0].Version != 3 {
		t.Fatalf("history = %+v, want one row for version 3", store.versions)
	}
	if store.versions[0].CreatedBy == "" || store.versions[0].CreatedAt.IsZero() {
		t.Error("a version records who saved it and when: a policy is a decision, and decisions have authors")
	}
	if store.versions[0].Policy.Previous != nil {
		t.Error("a stored version keeps no history of its own; the history is the sequence of rows")
	}
	// And the live document now reads back with that record on it.
	state, err := svc.CheckPolicy(readCtx(t), project)
	if err != nil {
		t.Fatalf("CheckPolicy: %v", err)
	}
	if state.CreatedBy != store.versions[0].CreatedBy || state.Policy.Version != 3 {
		t.Errorf("CheckPolicy = %+v, want version 3 with its author", state)
	}
}

// TestSavePolicyDryRunStoresNothing: the whole point of `dry_run` is
// that somebody can see the forty red pull requests before causing
// them (RFC 0005 §4.3).
func TestSavePolicyDryRunStoresNothing(t *testing.T) {
	store := policyStore()
	svc, catalog, project := serviceAndCatalog(store)

	saved, err := svc.SavePolicy(writeCtx(t), project, app.SavePolicy{
		DryRun: true,
		Policy: checkpolicy.Policy{Rules: []checkpolicy.Rule{
			{Selector: checkpolicy.Selector{Layer: "terminology"}, Severity: checkpolicy.Error},
		}},
	})
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if !saved.DryRun {
		t.Error("a dry run says it was one")
	}
	if catalog.saved != nil || len(store.versions) != 0 {
		t.Fatalf("a dry run stored something: catalog = %+v, versions = %+v", catalog.saved, store.versions)
	}
	if saved.State.Policy.Version != 1 {
		t.Errorf("version = %d: a dry run still says which version this would be", saved.State.Policy.Version)
	}
}

// TestSavePolicyPreviewsWhatWouldNewlyFail: the preview is measured
// against the runs that exist, and an open pull request among them is
// the number §4.3 asks it to show.
func TestSavePolicyPreviewsWhatWouldNewlyFail(t *testing.T) {
	store := policyStore()
	svc, catalog, project := serviceAndCatalog(store)
	catalog.open = map[string]int{"feature/pay": 41}

	saved, err := svc.SavePolicy(writeCtx(t), project, app.SavePolicy{
		DryRun: true,
		Policy: checkpolicy.Policy{Rules: []checkpolicy.Rule{
			{Selector: checkpolicy.Selector{Layer: "terminology", Namespace: "legal"}, Severity: checkpolicy.Error},
		}},
	})
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if saved.Impact.Runs != 1 || saved.Impact.Targets != 1 {
		t.Errorf("impact covered %d runs and %d findings, want 1 and 1", saved.Impact.Runs, saved.Impact.Targets)
	}
	if saved.Impact.Raised != 1 || saved.Impact.NewlyFailing != 1 {
		t.Errorf("raised = %d, newly failing = %d, want 1 and 1", saved.Impact.Raised, saved.Impact.NewlyFailing)
	}
	if got := saved.Impact.NewlyFailingRefs; len(got) != 1 || got[0] != "feature/pay" {
		t.Errorf("newly failing refs = %v, want [feature/pay]", got)
	}
	if saved.Impact.OpenPullRequests != 1 {
		t.Errorf("open pull requests = %d, want 1: that is the number of people who would wake up to a red PR",
			saved.Impact.OpenPullRequests)
	}
	if len(saved.Impact.Rules) != 1 || saved.Impact.Rules[0].NewlyFailing != 1 {
		t.Errorf("per-rule impact = %+v, want the one rule accounting for the one failure", saved.Impact.Rules)
	}
}

// pullRequestLinks answers where a project's pull requests are.
type pullRequestLinks struct {
	project uuid.UUID
	urls    map[int]string
	err     error
	asked   []int
}

func (l *pullRequestLinks) PullRequestURLs(_ context.Context, project uuid.UUID, numbers []int) (map[int]string, error) {
	l.asked = append(l.asked, numbers...)
	if l.err != nil {
		return nil, l.err
	}
	out := map[int]string{}
	if project != l.project {
		return out, nil
	}
	for _, n := range numbers {
		if u, ok := l.urls[n]; ok {
			out[n] = u
		}
	}
	return out, nil
}

// terminologyRaise is a candidate that turns the stored legal
// terminology warning into an error.
func terminologyRaise() app.SavePolicy {
	return app.SavePolicy{DryRun: true, Policy: checkpolicy.Policy{Rules: []checkpolicy.Rule{
		{Selector: checkpolicy.Selector{Layer: "terminology", Namespace: "legal"}, Severity: checkpolicy.Error},
	}}}
}

// TestSavePolicyNamesThePullRequestsItWouldBreak: the preview names the
// pull request that would newly fail — its number and where it is —
// rather than a branch and a count somebody has to look up (RFC 0005
// §4.3, §12.4).
func TestSavePolicyNamesThePullRequestsItWouldBreak(t *testing.T) {
	svc, catalog, project := serviceAndCatalog(policyStore())
	catalog.open = map[string]int{"feature/pay": 41}
	links := &pullRequestLinks{project: project, urls: map[int]string{41: "https://github.com/acme/shop/pull/41"}}
	svc.SetPullRequestLinks(links)

	saved, err := svc.SavePolicy(writeCtx(t), project, terminologyRaise())
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	want := []app.PullRequestImpact{{Ref: "feature/pay", Number: 41, URL: "https://github.com/acme/shop/pull/41"}}
	if !slices.Equal(saved.Impact.PullRequests, want) {
		t.Errorf("pull requests = %+v, want %+v", saved.Impact.PullRequests, want)
	}
	// Asked about the ones it names and nothing else.
	if !slices.Equal(links.asked, []int{41}) {
		t.Errorf("asked about %v, want [41]", links.asked)
	}
}

// Without a place to point — no GitHub integration, or a caller who may
// not read it — the pull request is still named by its number: the
// link is the extra, never the verdict.
func TestSavePolicyNamesThePullRequestWithoutALink(t *testing.T) {
	for name, links := range map[string]app.PullRequestLinks{
		"no integration": nil,
		"no permission":  &pullRequestLinks{err: &authz.DeniedError{Permission: authz.IntegrationRead}},
	} {
		t.Run(name, func(t *testing.T) {
			svc, catalog, project := serviceAndCatalog(policyStore())
			catalog.open = map[string]int{"feature/pay": 41}
			if links != nil {
				svc.SetPullRequestLinks(links)
			}
			saved, err := svc.SavePolicy(writeCtx(t), project, terminologyRaise())
			if err != nil {
				t.Fatalf("SavePolicy: %v", err)
			}
			want := []app.PullRequestImpact{{Ref: "feature/pay", Number: 41}}
			if !slices.Equal(saved.Impact.PullRequests, want) {
				t.Errorf("pull requests = %+v, want %+v", saved.Impact.PullRequests, want)
			}
		})
	}
	// Any other failure is a failure: a preview that silently lost its
	// links would look like one that had none to give.
	svc, catalog, project := serviceAndCatalog(policyStore())
	catalog.open = map[string]int{"feature/pay": 41}
	svc.SetPullRequestLinks(&pullRequestLinks{err: errors.New("the database is gone")})
	if _, err := svc.SavePolicy(writeCtx(t), project, terminologyRaise()); err == nil {
		t.Error("SavePolicy succeeded with the link lookup failing")
	}
}

// TestSavePolicyGraceDefaultsToPinning: the default has to protect the
// people who did not cause the change, so a save that says nothing
// about a grace still pins.
func TestSavePolicyGraceDefaultsToPinning(t *testing.T) {
	svc, catalog, project := serviceAndCatalog(policyStore())
	catalog.policy = checkpolicy.Policy{Schema: checkpolicy.Schema, Version: 1}

	saved, err := svc.SavePolicy(writeCtx(t), project, app.SavePolicy{Policy: checkpolicy.Policy{}})
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if saved.State.Policy.GraceUntil == nil || saved.State.Policy.Previous == nil {
		t.Fatalf("a save with no grace_days pinned nothing: %+v", saved.State.Policy)
	}
	want := saved.State.Policy.EffectiveFrom.Add(checkpolicy.DefaultGrace)
	if !saved.State.Policy.GraceUntil.Equal(want) {
		t.Errorf("grace until %v, want %v (the documented default)", saved.State.Policy.GraceUntil, want)
	}

	none := time.Duration(0)
	saved, err = svc.SavePolicy(writeCtx(t), project, app.SavePolicy{Policy: checkpolicy.Policy{}, Grace: &none})
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if saved.State.Policy.GraceUntil != nil || saved.State.Policy.Previous != nil {
		t.Errorf("a zero grace pinned something: %+v", saved.State.Policy)
	}
}

// TestSavePolicyRefusesALostRace: a write prepared against a document
// somebody else has since replaced would drop what they said, so it is
// refused and the caller reads the policy again.
func TestSavePolicyRefusesALostRace(t *testing.T) {
	svc, catalog, project := serviceAndCatalog(policyStore())
	catalog.conflict = true

	_, err := svc.SavePolicy(writeCtx(t), project, app.SavePolicy{Policy: checkpolicy.Policy{}})
	if !errors.Is(err, app.ErrPolicyConflict) {
		t.Errorf("SavePolicy = %v, want ErrPolicyConflict", err)
	}
}

// TestPolicyPermissions: reading a policy is a catalog read and writing
// one is a catalog write — the permission that already carries the
// authority to change what a project's check concludes.
func TestPolicyPermissions(t *testing.T) {
	svc, _, project := serviceAndCatalog(policyStore())
	read := readCtx(t)

	if _, err := svc.SavePolicy(read, project, app.SavePolicy{Policy: checkpolicy.Policy{}}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("SavePolicy with catalog.read = %v, want forbidden", err)
	}
	none := context.Background()
	if _, err := svc.CheckPolicy(none, project); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("CheckPolicy without a principal = %v, want unauthenticated", err)
	}
	if _, _, err := svc.ListPolicyVersions(none, project, pageOf(10)); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("ListPolicyVersions without a principal = %v, want unauthenticated", err)
	}
}

// TestPolicyVersionsAreTheRecord: the history is what a version list
// reads, newest first, and one version reads back as it was saved.
func TestPolicyVersionsAreTheRecord(t *testing.T) {
	store := policyStore()
	svc, _, project := serviceAndCatalog(store)
	for range 3 {
		if _, err := svc.SavePolicy(writeCtx(t), project, app.SavePolicy{Policy: checkpolicy.Policy{}}); err != nil {
			t.Fatalf("SavePolicy: %v", err)
		}
	}

	vs, next, err := svc.ListPolicyVersions(readCtx(t), project, pageOf(2))
	if err != nil {
		t.Fatalf("ListPolicyVersions: %v", err)
	}
	if len(vs) != 2 || vs[0].Version != 3 || vs[1].Version != 2 {
		t.Fatalf("page = %+v, want versions 3 and 2 newest first", vs)
	}
	if next == nil {
		t.Fatal("a full page issued no cursor")
	}
	page, err := pagination.Parse(nil, next)
	if err != nil {
		t.Fatalf("the token this list issued was refused: %v", err)
	}
	rest, _, err := svc.ListPolicyVersions(readCtx(t), project, pagination.Page{Size: 2, After: page.After})
	if err != nil {
		t.Fatalf("ListPolicyVersions: %v", err)
	}
	if len(rest) != 1 || rest[0].Version != 1 {
		t.Fatalf("second page = %+v, want version 1", rest)
	}

	v, err := svc.PolicyVersion(readCtx(t), project, 2)
	if err != nil || v.Version != 2 {
		t.Fatalf("PolicyVersion(2) = %+v, %v", v, err)
	}
	if _, err := svc.PolicyVersion(readCtx(t), project, 99); !errors.Is(err, app.ErrPolicyVersionNotFound) {
		t.Errorf("PolicyVersion(99) = %v, want ErrPolicyVersionNotFound", err)
	}
}

// TestPolicyOfAnUnknownProjectIsNotFound: an unknown project is a 404
// and not the default policy, or a caller would read a policy for
// something that does not exist.
func TestPolicyOfAnUnknownProjectIsNotFound(t *testing.T) {
	svc, _, _ := serviceAndCatalog(policyStore())
	if _, err := svc.CheckPolicy(readCtx(t), uuid.New()); !errors.Is(err, app.ErrProjectNotFound) {
		t.Errorf("CheckPolicy of an unknown project = %v, want ErrProjectNotFound", err)
	}
}

// TestSavePolicyRefusesADocumentItCannotGrade: what the evaluator
// refuses on the way out, the write refuses on the way in.
func TestSavePolicyRefusesADocumentItCannotGrade(t *testing.T) {
	svc, catalog, project := serviceAndCatalog(policyStore())

	_, err := svc.SavePolicy(writeCtx(t), project, app.SavePolicy{
		Policy: checkpolicy.Policy{Rules: []checkpolicy.Rule{
			{Selector: checkpolicy.Selector{Layer: "no-such-layer"}, Severity: checkpolicy.Error},
		}},
	})
	if !errors.Is(err, checkpolicy.ErrUnknownLayer) {
		t.Errorf("SavePolicy = %v, want ErrUnknownLayer", err)
	}
	if catalog.saved != nil {
		t.Error("a document that could never be graded was stored")
	}
	if _, err := svc.SavePolicy(writeCtx(t), project, app.SavePolicy{
		Policy: checkpolicy.Policy{Version: 9},
	}); !errors.Is(err, domain.ErrPolicyBookkeeping) {
		t.Errorf("SavePolicy naming its own version = %v, want ErrPolicyBookkeeping", err)
	}
}
