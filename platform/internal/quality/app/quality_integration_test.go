//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

func firstPage() pagination.Page { return pagination.Page{Size: pagination.DefaultPageSize} }

// record stores a run with its findings.
func record(t *testing.T, h *harness, project uuid.UUID, ref string, fs ...domain.Finding) domain.CheckRun {
	t.Helper()
	run, err := h.svc.RecordCheckRun(h.developer(), app.RecordRun{
		Project: project, Ref: ref, Commit: sha("abc"), Trigger: domain.TriggerCLI,
		Layers:   []domain.Layer{domain.LayerParity, domain.LayerCompleteness, domain.LayerTerminology},
		Findings: fs,
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	return run
}

func list(t *testing.T, h *harness, project uuid.UUID, q app.FindingQuery) app.Findings {
	t.Helper()
	got, err := h.svc.ListFindings(h.developer(), project, q, firstPage())
	if err != nil {
		t.Fatalf("list findings: %v", err)
	}
	return got
}

// TestRecordRunStoresItsFindings is the shape every surface reads: one
// run, its counts, and its findings in a stable order.
func TestRecordRunStoresItsFindings(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	fs := []domain.Finding{
		finding(domain.LayerTerminology, "term_forbidden", domain.Locus{Key: "checkout.pay", Locale: "de"}, domain.Warning),
		finding(domain.LayerParity, "missing-argument", domain.Locus{Key: "checkout.total", Locale: "fr"}, domain.Error, subject("amount")),
	}
	run := record(t, h, project, "main", fs...)

	if run.Counts != (domain.Counts{Errors: 1, Warnings: 1}) {
		t.Errorf("counts = %+v", run.Counts)
	}
	if run.Conclusion != domain.ConclusionFailure {
		t.Errorf("conclusion = %q, want failure (an error fails the default policy)", run.Conclusion)
	}
	if run.Commit != sha("abc") || run.Ref != "main" || run.CreatedBy == "" {
		t.Errorf("run = %+v", run)
	}

	got, err := h.svc.GetCheckRun(h.developer(), project, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Counts != run.Counts || len(got.Layers) != 3 || got.Layers[0] != domain.LayerParity {
		t.Errorf("stored run = %+v", got)
	}

	page := list(t, h, project, app.FindingQuery{Run: run.ID})
	if len(page.Items) != 2 {
		t.Fatalf("%d findings, want 2", len(page.Items))
	}
	// Errors before warnings, and the wire schema is named on every one.
	if page.Items[0].Severity != domain.Error || page.Items[1].Severity != domain.Warning {
		t.Errorf("order = %q, %q", page.Items[0].Severity, page.Items[1].Severity)
	}
	if page.Items[0].Schema != domain.Schema || page.Items[0].Subject != "amount" {
		t.Errorf("finding = %+v", page.Items[0].Finding)
	}
	if page.Counts != (domain.Counts{Errors: 1, Warnings: 1}) {
		t.Errorf("counts = %+v", page.Counts)
	}
	if page.Run == nil || page.Run.ID != run.ID {
		t.Errorf("the list doesn't say which run it read")
	}
}

// TestWaiverNeverDeletesAFinding is RFC 0005 §2.3 and §14 decision 5:
// a waived finding is still computed, still listed and counted on its
// own, and it comes back when its source revision moves.
func TestWaiverNeverDeletesAFinding(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	locus := domain.Locus{Key: "checkout.pay", Locale: "de"}
	at7 := finding(domain.LayerTerminology, "term_missing", locus, domain.Error, at(7))
	run := record(t, h, project, "main", at7)
	if run.Counts.Errors != 1 {
		t.Fatalf("counts = %+v", run.Counts)
	}

	w, created, err := h.svc.CreateWaiver(h.developer(), project, app.CreateWaiver{
		Fingerprint: at7.Fingerprint, Reason: "Login is the German term",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created || w.SourceRevision != 7 {
		t.Fatalf("waiver = %+v (created %v): it should default to the finding's source revision", w.Waiver, created)
	}

	// Still there, at severity waived, naming its waiver — not hidden.
	page := list(t, h, project, app.FindingQuery{Run: run.ID})
	if len(page.Items) != 1 {
		t.Fatalf("%d findings, want the waived one to still be reported", len(page.Items))
	}
	if page.Items[0].Severity != domain.Waived || page.Items[0].Waiver != w.ID.String() {
		t.Errorf("finding = %+v", page.Items[0].Finding)
	}
	if page.Counts != (domain.Counts{Waived: 1}) {
		t.Errorf("counts = %+v: waived is counted on its own", page.Counts)
	}
	// The run's own verdict is history and does not move.
	stored, err := h.svc.GetCheckRun(h.developer(), project, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Counts.Errors != 1 || stored.Conclusion != domain.ConclusionFailure {
		t.Errorf("the stored run changed: %+v", stored)
	}

	// The waiver reaches a run recorded after it, too.
	next := record(t, h, project, "main", at7)
	if next.Counts != (domain.Counts{Waived: 1}) || next.Conclusion != domain.ConclusionSuccess {
		t.Errorf("a later run = %+v / %q", next.Counts, next.Conclusion)
	}

	// The source moves: the German somebody waived is not the German
	// that now ships, so the finding comes back as an ordinary one.
	at9 := finding(domain.LayerTerminology, "term_missing", locus, domain.Error, at(9))
	if at9.Fingerprint != at7.Fingerprint {
		t.Fatalf("the source revision must not be part of the fingerprint")
	}
	moved := record(t, h, project, "main", at9)
	if moved.Counts != (domain.Counts{Errors: 1}) || moved.Conclusion != domain.ConclusionFailure {
		t.Errorf("after the source moved: %+v / %q", moved.Counts, moved.Conclusion)
	}
	back := list(t, h, project, app.FindingQuery{Run: moved.ID})
	if len(back.Items) != 1 || back.Items[0].Severity != domain.Error {
		t.Errorf("finding = %+v", back.Items)
	}
}

// TestWaiverRevocationBringsFindingsBack: revoking is not deleting.
func TestWaiverRevocationBringsFindingsBack(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	f := finding(domain.LayerStyle, "informal-address", domain.Locus{Key: "a.b", Locale: "de"}, domain.Warning)
	run := record(t, h, project, "main", f)
	w, _, err := h.svc.CreateWaiver(h.developer(), project, app.CreateWaiver{Fingerprint: f.Fingerprint, Reason: "by design"})
	if err != nil {
		t.Fatal(err)
	}
	if got := list(t, h, project, app.FindingQuery{Run: run.ID}); got.Counts.Waived != 1 {
		t.Fatalf("counts = %+v", got.Counts)
	}
	if err := h.svc.RevokeWaiver(h.developer(), project, w.ID); err != nil {
		t.Fatal(err)
	}
	got := list(t, h, project, app.FindingQuery{Run: run.ID})
	if got.Counts != (domain.Counts{Warnings: 1}) || got.Items[0].Severity != domain.Warning {
		t.Errorf("after revoking: counts %+v, severity %q", got.Counts, got.Items[0].Severity)
	}
	// Revoking twice is not an error, and the revoked waiver is history.
	if err := h.svc.RevokeWaiver(h.developer(), project, w.ID); err != nil {
		t.Errorf("revoking twice: %v", err)
	}
	ws, _, err := h.svc.ListWaivers(h.developer(), project, app.WaiverFilter{}, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].Active || ws[0].RevokedAt == nil || ws[0].Reason != "by design" {
		t.Errorf("waivers = %+v", ws)
	}
}

// TestWaiverReasonIsRequired: there is no way to waive without one.
func TestWaiverReasonIsRequired(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	f := finding(domain.LayerSource, "ambiguous-short", domain.Locus{Key: "a.b"}, domain.Warning)
	for _, reason := range []string{"", "   ", "\t\n "} {
		if _, _, err := h.svc.CreateWaiver(h.developer(), project, app.CreateWaiver{
			Fingerprint: f.Fingerprint, Reason: reason,
		}); !errors.Is(err, domain.ErrReasonRequired) {
			t.Errorf("reason %q: err = %v, want ErrReasonRequired", reason, err)
		}
	}
	if _, _, err := h.svc.CreateWaiver(h.developer(), project, app.CreateWaiver{
		Fingerprint: "not-a-fingerprint", Reason: "because",
	}); !errors.Is(err, domain.ErrInvalidFingerprint) {
		t.Errorf("err = %v, want ErrInvalidFingerprint", err)
	}
}

// TestWaiverScopeAndExpiry: a branch waiver reaches its branch only,
// and an expired one stops accepting.
func TestWaiverScopeAndExpiry(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	f := finding(domain.LayerLength, "max-length-exceeded", domain.Locus{Key: "a.b", Locale: "ja"}, domain.Error)
	expires := h.clock.now().Add(48 * time.Hour)
	if _, _, err := h.svc.CreateWaiver(h.developer(), project, app.CreateWaiver{
		Fingerprint: f.Fingerprint, Reason: "the button is two lines by design", Scope: domain.WaiverBranch,
		Ref: "feature/checkout", ExpiresAt: &expires,
	}); err != nil {
		t.Fatal(err)
	}
	onBranch := record(t, h, project, "feature/checkout", f)
	if onBranch.Counts != (domain.Counts{Waived: 1}) {
		t.Errorf("on its branch: %+v", onBranch.Counts)
	}
	onMain := record(t, h, project, "main", f)
	if onMain.Counts != (domain.Counts{Errors: 1}) {
		t.Errorf("on another branch a branch waiver must not reach: %+v", onMain.Counts)
	}
	if got := list(t, h, project, app.FindingQuery{Run: onBranch.ID}); got.Counts.Waived != 1 {
		t.Errorf("read on its branch: %+v", got.Counts)
	}

	h.clock.advance(72 * time.Hour)
	if got := list(t, h, project, app.FindingQuery{Run: onBranch.ID}); got.Counts != (domain.Counts{Errors: 1}) {
		t.Errorf("an expired waiver still accepts: %+v", got.Counts)
	}
	past := h.clock.now().Add(-time.Hour)
	if _, _, err := h.svc.CreateWaiver(h.developer(), project, app.CreateWaiver{
		Fingerprint: f.Fingerprint, Reason: "no", ExpiresAt: &past,
	}); !errors.Is(err, domain.ErrExpiryInThePast) {
		t.Errorf("err = %v, want ErrExpiryInThePast", err)
	}
}

// TestWaivingTwiceRestatesTheReason: one live waiver per finding and
// reach, so the list doesn't fill with duplicates.
func TestWaivingTwiceRestatesTheReason(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	f := finding(domain.LayerTerminology, "term_missing", domain.Locus{Key: "a.b", Locale: "de"}, domain.Warning)
	record(t, h, project, "main", f)
	first, created, err := h.svc.CreateWaiver(h.developer(), project, app.CreateWaiver{Fingerprint: f.Fingerprint, Reason: "first"})
	if err != nil || !created {
		t.Fatalf("first: %v (created %v)", err, created)
	}
	again, created, err := h.svc.CreateWaiver(h.developer(), project, app.CreateWaiver{Fingerprint: f.Fingerprint, Reason: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if created || again.ID != first.ID || again.Reason != "second" {
		t.Errorf("again = %+v (created %v), want the same waiver restated", again.Waiver, created)
	}
	ws, _, err := h.svc.ListWaivers(h.developer(), project, app.WaiverFilter{}, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 {
		t.Fatalf("%d waivers, want 1", len(ws))
	}
	// The list says what the waiver accepts.
	if ws[0].Accepts.Layer != string(domain.LayerTerminology) || ws[0].Accepts.Key != "a.b" || ws[0].Accepts.Locale != "de" {
		t.Errorf("accepts = %+v", ws[0].Accepts)
	}
}

// TestFindingFilters: every filter the dashboards and Studio need.
func TestFindingFilters(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	fs := []domain.Finding{
		finding(domain.LayerParity, "missing-argument", domain.Locus{Key: "a.one", Locale: "fr", Namespace: "checkout"}, domain.Error),
		finding(domain.LayerTerminology, "term_forbidden", domain.Locus{Key: "a.two", Locale: "de", Namespace: "legal"}, domain.Error),
		finding(domain.LayerStyle, "informal-address", domain.Locus{Key: "a.three", Locale: "de", Namespace: "legal"}, domain.Warning),
	}
	run := record(t, h, project, "main", fs...)
	waived := fs[2]
	if _, _, err := h.svc.CreateWaiver(h.developer(), project, app.CreateWaiver{
		Fingerprint: waived.Fingerprint, Reason: "house style",
	}); err != nil {
		t.Fatal(err)
	}

	no := false
	yes := true
	for _, tc := range []struct {
		name   string
		filter app.FindingFilter
		want   []string
	}{
		{"layer", app.FindingFilter{Layer: string(domain.LayerStyle)}, []string{"a.three"}},
		{"severity", app.FindingFilter{Severity: string(domain.Error)}, []string{"a.one", "a.two"}},
		{"severity waived", app.FindingFilter{Severity: string(domain.Waived)}, []string{"a.three"}},
		{"locale", app.FindingFilter{Locale: "de"}, []string{"a.two", "a.three"}},
		{"namespace", app.FindingFilter{Namespace: "legal"}, []string{"a.two", "a.three"}},
		{"code", app.FindingFilter{Code: "term_forbidden"}, []string{"a.two"}},
		{"message", app.FindingFilter{Key: "a.one"}, []string{"a.one"}},
		{"waived", app.FindingFilter{Waived: &yes}, []string{"a.three"}},
		{"not waived", app.FindingFilter{Waived: &no}, []string{"a.one", "a.two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := list(t, h, project, app.FindingQuery{Run: run.ID, Filter: tc.filter})
			var keys []string
			for _, f := range got.Items {
				keys = append(keys, f.Locus.Key)
			}
			if len(keys) != len(tc.want) {
				t.Fatalf("keys = %v, want %v", keys, tc.want)
			}
			for _, w := range tc.want {
				found := false
				for _, k := range keys {
					found = found || k == w
				}
				if !found {
					t.Errorf("keys = %v, want %v", keys, tc.want)
				}
			}
		})
	}

	if _, err := h.svc.ListFindings(h.developer(), project, app.FindingQuery{
		Run: run.ID, Filter: app.FindingFilter{Layer: "nonsense"},
	}, firstPage()); !errors.Is(err, app.ErrInvalidQuery) {
		t.Errorf("an unknown layer: err = %v, want ErrInvalidQuery", err)
	}
}

// TestFindingsPaginateStably: the cursor is the order's own key.
func TestFindingsPaginateStably(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	var fs []domain.Finding
	for i := range 25 {
		fs = append(fs, finding(domain.LayerCompleteness, "missing-translation",
			domain.Locus{Key: "key." + string(rune('a'+i%25)), Locale: "fr"}, domain.Warning, subject(string(rune('a'+i)))))
	}
	run := record(t, h, project, "main", fs...)

	seen := map[string]bool{}
	page := pagination.Page{Size: 7}
	for pages := 0; ; pages++ {
		if pages > 10 {
			t.Fatal("pagination does not terminate")
		}
		got, err := h.svc.ListFindings(h.developer(), project, app.FindingQuery{Run: run.ID}, page)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range got.Items {
			if seen[f.Fingerprint] {
				t.Fatalf("finding %s came back twice", f.Fingerprint)
			}
			seen[f.Fingerprint] = true
		}
		if got.Next == nil {
			break
		}
		if page, err = pagination.Parse(&page.Size, got.Next); err != nil {
			t.Fatalf("next token rejected: %v", err)
		}
	}
	if len(seen) != 25 {
		t.Errorf("saw %d findings, want 25", len(seen))
	}
}

// TestCheckRunListFilters: branch, commit, conclusion and trigger.
func TestCheckRunListFilters(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	clean := finding(domain.LayerSource, "manual-plural", domain.Locus{Key: "a.b"}, domain.Warning)
	broken := finding(domain.LayerParity, "missing-argument", domain.Locus{Key: "a.c", Locale: "fr"}, domain.Error)

	h.clock.advance(time.Minute)
	green := record(t, h, project, "main", clean)
	h.clock.advance(time.Minute)
	red := record(t, h, project, "feature/x", broken)

	runs, _, err := h.svc.ListCheckRuns(h.developer(), project, app.RunFilter{}, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != red.ID {
		t.Fatalf("runs = %v, want the newest first", runs)
	}
	for _, tc := range []struct {
		name   string
		filter app.RunFilter
		want   uuid.UUID
	}{
		{"branch", app.RunFilter{Ref: "main"}, green.ID},
		{"commit", app.RunFilter{Commit: sha("abc")}, red.ID},
		{"conclusion", app.RunFilter{Conclusion: string(domain.ConclusionSuccess)}, green.ID},
		{"trigger", app.RunFilter{Trigger: string(domain.TriggerCLI)}, red.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := h.svc.ListCheckRuns(h.developer(), project, tc.filter, firstPage())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 || got[0].ID != tc.want {
				t.Errorf("first run = %v, want %v", got, tc.want)
			}
		})
	}
	if _, _, err := h.svc.ListCheckRuns(h.developer(), project, app.RunFilter{Conclusion: "maybe"}, firstPage()); !errors.Is(err, app.ErrInvalidQuery) {
		t.Errorf("err = %v, want ErrInvalidQuery", err)
	}

	// Findings without a run read the newest run of the branch.
	if got := list(t, h, project, app.FindingQuery{Ref: "main"}); got.Run == nil || got.Run.ID != green.ID {
		t.Errorf("branch = main read %v, want %v", got.Run, green.ID)
	}
	if got := list(t, h, project, app.FindingQuery{}); got.Run == nil || got.Run.ID != red.ID {
		t.Errorf("without a filter the newest run should be read")
	}
}

// TestFindingsWithoutARunAreAnEmptyList, not a 404: a project nobody has
// checked yet has no findings, which is not the same as not existing.
func TestFindingsWithoutARunAreAnEmptyList(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	got := list(t, h, project, app.FindingQuery{})
	if got.Run != nil || len(got.Items) != 0 || got.Counts != (domain.Counts{}) {
		t.Errorf("got = %+v", got)
	}
	if _, err := h.svc.ListFindings(h.developer(), project, app.FindingQuery{Run: uuid.Must(uuid.NewV7())}, firstPage()); !errors.Is(err, app.ErrCheckRunNotFound) {
		t.Errorf("a named run that isn't there: err = %v", err)
	}
	if _, err := h.svc.ListFindings(h.developer(), uuid.Must(uuid.NewV7()), app.FindingQuery{}, firstPage()); !errors.Is(err, app.ErrProjectNotFound) {
		t.Errorf("an unknown project: err = %v", err)
	}
}

// TestPermissions: reads need catalog.read, writes catalog.write.
func TestPermissions(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	f := finding(domain.LayerParity, "missing-argument", domain.Locus{Key: "a.b", Locale: "fr"}, domain.Error)
	run := record(t, h, project, "main", f)

	// A translator reads (catalog.read comes with every role) …
	if _, err := h.svc.ListFindings(h.translator(), project, app.FindingQuery{Run: run.ID}, firstPage()); err != nil {
		t.Errorf("a translator cannot read findings: %v", err)
	}
	// … and cannot waive.
	if _, _, err := h.svc.CreateWaiver(h.translator(), project, app.CreateWaiver{
		Fingerprint: f.Fingerprint, Reason: "no",
	}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("a translator waived a finding: %v", err)
	}
	// A CI token with the write scope may, because `glossa check` and
	// the pull-request check act with exactly that (RFC 0004 §6.3).
	ci := authztest.Token(context.Background(), h.tenant, "write")
	if _, _, err := h.svc.CreateWaiver(ci, project, app.CreateWaiver{Fingerprint: f.Fingerprint, Reason: "known"}); err != nil {
		t.Errorf("a write token cannot waive: %v", err)
	}
	// No principal at all reads nothing.
	if _, err := h.svc.ListFindings(context.Background(), project, app.FindingQuery{}, firstPage()); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("err = %v, want ErrUnauthenticated", err)
	}
}

// TestTenantsAreIsolated: another tenant's project is simply not there.
func TestTenantsAreIsolated(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	f := finding(domain.LayerParity, "missing-argument", domain.Locus{Key: "a.b", Locale: "fr"}, domain.Error)
	run := record(t, h, project, "main", f)

	other := harnessFor(t, "globex")
	if _, err := other.svc.GetCheckRun(other.developer(), project, run.ID); !errors.Is(err, app.ErrProjectNotFound) {
		t.Errorf("another tenant read a run: %v", err)
	}
	if _, err := other.svc.ListFindings(other.developer(), project, app.FindingQuery{Run: run.ID}, firstPage()); !errors.Is(err, app.ErrProjectNotFound) {
		t.Errorf("another tenant read findings: %v", err)
	}
}

// TestRecordedFindingsKeepTheirLayerSeverity: a caller may not assert
// `waived` — the run decides that from the project's waivers.
func TestRecordedFindingsKeepTheirLayerSeverity(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	f := finding(domain.LayerParity, "missing-argument", domain.Locus{Key: "a.b", Locale: "fr"}, domain.Waived)
	if _, err := h.svc.RecordCheckRun(h.developer(), app.RecordRun{
		Project: project, Ref: "main", Trigger: domain.TriggerCLI, Findings: []domain.Finding{f},
	}); !errors.Is(err, app.ErrPreGradedFinding) {
		t.Errorf("err = %v, want ErrPreGradedFinding", err)
	}
}

// TestPolicyStaysTheEvaluator: the run asks the policy what fails and
// never decides for itself.
func TestPolicyStaysTheEvaluator(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	warning := finding(domain.LayerSource, "ambiguous-short", domain.Locus{Key: "a.b"}, domain.Warning)
	run, err := h.svc.RecordCheckRun(h.developer(), app.RecordRun{
		Project: project, Ref: "main", Trigger: domain.TriggerPullRequest, PolicyVersion: 3,
		Policy: checkpolicy.Policy{FailOn: checkpolicy.Warning}, Findings: []domain.Finding{warning},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Conclusion != domain.ConclusionFailure || run.PolicyVersion != 3 {
		t.Errorf("run = %+v: fail_on warning must fail on a warning", run)
	}
}
