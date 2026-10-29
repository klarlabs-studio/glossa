//go:build integration

package app_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// ReportCheckRun against a real catalog and a real database: the part
// the fakes cannot answer is whether the key a reporter sent resolves
// to the message ID the rest of the platform fingerprints over.

// message puts one source message in the catalog and returns its ID.
func message(t *testing.T, h *harness, project uuid.UUID, key, text string) uuid.UUID {
	t.Helper()
	res, err := h.catalog.UpsertMessages(h.developer(), catalogdomain.ProjectID(project),
		[]catalogapp.UpsertItem{{Key: key, Text: text}})
	if err != nil {
		t.Fatalf("upsert %s: %v", key, err)
	}
	if len(res) != 1 || res[0].Message == nil {
		t.Fatalf("upsert %s: %+v", key, res)
	}
	return res[0].Message.ID.UUID()
}

// TestReportedRunFingerprintsOverTheCatalogMessage: a reporter hands in
// a key, and the stored finding carries the print every other surface
// computes — over the catalog message ID. It is the property the whole
// operation turns on: a waiver made against this fingerprint is the
// waiver that accepts the same finding when the pull-request check, the
// findings list or `glossa findings` sees it again.
func TestReportedRunFingerprintsOverTheCatalogMessage(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	h.addLocale(t, project, "de")
	id := message(t, h, project, "checkout.pay", "Pay {amount}")

	run, err := h.svc.ReportCheckRun(h.ci(), app.ReportCheckRun{
		Project: project, Ref: "feat/checkout", Commit: sha("abc"), Trigger: domain.TriggerCLI,
		Layers: []domain.Layer{domain.LayerParity},
		Findings: []app.ReportedFinding{{
			Layer: domain.LayerParity, Code: "missing-argument", Severity: domain.Error,
			Locus:       app.ReportedLocus{Key: "checkout.pay", Locale: "de"},
			Explanation: "the German drops {amount}", Subject: "amount",
		}},
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if run.Trigger != domain.TriggerCLI || run.Ref != "feat/checkout" || run.Commit != sha("abc") {
		t.Errorf("run = %+v, want the reported ref, commit and trigger", run)
	}
	if run.Counts != (domain.Counts{Errors: 1}) || run.Conclusion != domain.ConclusionFailure {
		t.Errorf("counts = %+v, conclusion = %q, want the policy's verdict on one error", run.Counts, run.Conclusion)
	}

	got := list(t, h, project, app.FindingQuery{Run: run.ID})
	if len(got.Items) != 1 {
		t.Fatalf("stored %d findings, want the one reported", len(got.Items))
	}
	want := domain.Fingerprint(domain.LayerParity, "missing-argument",
		domain.Locus{Message: id.String(), Key: "checkout.pay", Locale: "de"}, "amount")
	if got.Items[0].Fingerprint != want {
		t.Errorf("fingerprint = %s, want the print over the catalog message %s", got.Items[0].Fingerprint, want)
	}
	if got.Items[0].Locus.Message != id.String() {
		t.Errorf("locus.message = %q, want the resolved message ID", got.Items[0].Locus.Message)
	}

	// And the waiver made against that print accepts the same finding
	// the next time it is reported, which is the reason the server owns
	// the fingerprint at all.
	if _, _, err := h.svc.CreateWaiver(h.developer(), project, app.CreateWaiver{
		Fingerprint: want, Reason: "the German is intentionally shorter here",
	}); err != nil {
		t.Fatalf("waive: %v", err)
	}
	again, err := h.svc.ReportCheckRun(h.ci(), app.ReportCheckRun{
		Project: project, Ref: "feat/checkout", Commit: sha("def"), Trigger: domain.TriggerCLI,
		Layers: []domain.Layer{domain.LayerParity},
		Findings: []app.ReportedFinding{{
			Layer: domain.LayerParity, Code: "missing-argument", Severity: domain.Error,
			Locus:       app.ReportedLocus{Key: "checkout.pay", Locale: "de"},
			Explanation: "the German drops {amount}", Subject: "amount",
		}},
	})
	if err != nil {
		t.Fatalf("report again: %v", err)
	}
	if again.Counts != (domain.Counts{Waived: 1}) || again.Conclusion != domain.ConclusionSuccess {
		t.Errorf("counts = %+v, conclusion = %q; the waiver did not reach the reported finding",
			again.Counts, again.Conclusion)
	}
}

// TestReportedRunIsGradedByTheStoredPolicy: the project's document
// decides, not the reporter — which is what stops a client declaring
// its own build green, and what makes an unrecorded local override
// harmless.
func TestReportedRunIsGradedByTheStoredPolicy(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	h.addLocale(t, project, "de")
	message(t, h, project, "checkout.pay", "Pay {amount}")

	if _, err := h.svc.SavePolicy(h.developer(), project, app.SavePolicy{
		Policy: checkpolicy.Policy{
			FailOn: checkpolicy.Error, MissingTranslations: checkpolicy.Error,
			Rules: []checkpolicy.Rule{
				{Selector: checkpolicy.Selector{Layer: "parity"}, Severity: checkpolicy.Warning},
			},
		},
	}); err != nil {
		t.Fatalf("save policy: %v", err)
	}

	run, err := h.svc.ReportCheckRun(h.ci(), app.ReportCheckRun{
		Project: project, Ref: "main", Trigger: domain.TriggerCLI,
		Layers: []domain.Layer{domain.LayerParity},
		Findings: []app.ReportedFinding{{
			Layer: domain.LayerParity, Code: "missing-argument", Severity: domain.Error,
			Locus:       app.ReportedLocus{Key: "checkout.pay", Locale: "de"},
			Explanation: "the German drops {amount}", Subject: "amount",
		}},
	})
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if run.Counts != (domain.Counts{Warnings: 1}) || run.Conclusion != domain.ConclusionSuccess {
		t.Errorf("counts = %+v, conclusion = %q; the reporter's error survived the policy",
			run.Counts, run.Conclusion)
	}
	if run.PolicyVersion == 0 {
		t.Error("the run did not record which policy version graded it")
	}
}

// TestReportingIsWhatACITokenAlreadyHolds: `glossa check` runs under a
// CI token — catalog.read and catalog.write, the ceiling of RFC 0004
// §6.3 — and recording needs no more than that. A translator, who can
// write a locale but not the catalog, cannot file a verdict.
func TestReportingIsWhatACITokenAlreadyHolds(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	in := app.ReportCheckRun{
		Project: project, Ref: "main", Trigger: domain.TriggerCLI,
		Layers: []domain.Layer{domain.LayerStructure},
	}
	if _, err := h.svc.ReportCheckRun(h.ci(), in); err != nil {
		t.Errorf("a CI token could not record its own run: %v", err)
	}
	if _, err := h.svc.ReportCheckRun(h.translator(), in); err == nil {
		t.Error("a translator recorded a check run")
	}
}

// TestReportedRunRefusesPastTheCapAgainstTheDatabase: RFC 0005 §10's
// 10 000, refused whole — nothing is stored, and nothing is truncated
// into looking clean.
func TestReportedRunRefusesPastTheCapAgainstTheDatabase(t *testing.T) {
	h := newHarness(t)
	project := h.project(t, "demo")
	fs := make([]app.ReportedFinding, app.MaxRunFindings+1)
	for i := range fs {
		fs[i] = app.ReportedFinding{
			Layer: domain.LayerStructure, Code: "unparseable", Severity: domain.Error,
			Locus: app.ReportedLocus{Key: "k", Locale: "de"}, Explanation: "does not parse",
		}
	}
	_, err := h.svc.ReportCheckRun(h.ci(), app.ReportCheckRun{
		Project: project, Ref: "main", Layers: []domain.Layer{domain.LayerStructure}, Findings: fs,
	})
	if !errors.Is(err, app.ErrTooManyFindings) {
		t.Fatalf("err = %v, want ErrTooManyFindings", err)
	}
	runs, _, err := h.svc.ListCheckRuns(h.developer(), project, app.RunFilter{}, firstPage())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Errorf("a refused run left %d runs behind", len(runs))
	}
}
