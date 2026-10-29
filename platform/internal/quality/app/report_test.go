package app_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// ReportCheckRun is the create half of RFC 0005 §9's "check runs
// (create, read, list)": a check that ran in a product's CI, recorded
// so that Studio, the findings list and the summary see it.
//
// What these tests pin is everything a reporter may not decide — the
// fingerprint, the severity, the verdict, the waivers — and the two
// limits that keep a recorded run honest.

// reported is one finding as `glossa check` would hand it over.
func reported(key, code string, sev domain.Severity) app.ReportedFinding {
	return app.ReportedFinding{
		Layer: domain.LayerParity, Code: code, Severity: sev,
		Locus:       app.ReportedLocus{Key: key, Locale: "de"},
		Explanation: "the translation drops an argument the source has",
		Subject:     "amount",
	}
}

// reporter is a service that takes writes, the Catalog behind it, and a
// context holding a repository's CI token and nothing more —
// catalog.read and catalog.write, exactly what `glossa check` runs
// under today.
type reporter struct {
	ctx     context.Context
	svc     *app.Service
	store   *fakeStore
	catalog *knownProjects
	project uuid.UUID
}

func newReporter(t *testing.T) reporter {
	t.Helper()
	store := recordingStore()
	svc, catalog, project := serviceAndCatalog(store)
	return reporter{
		ctx: authztest.CIToken(t.Context(), tenancy.NewID()),
		svc: svc, store: store, catalog: catalog, project: project,
	}
}

// TestReportedFingerprintIsTheServersOverTheCatalogMessage: a reporter
// has the key; the server has the catalog. The stored print is the one
// every other surface computes for the same finding — over the catalog
// message ID — and is *not* the print over the key, which is what a
// client could have minted.
func TestReportedFingerprintIsTheServersOverTheCatalogMessage(t *testing.T) {
	r := newReporter(t)
	message := uuid.New()
	r.catalog.messages = map[string]uuid.UUID{"checkout.pay": message}

	if _, err := r.svc.ReportCheckRun(r.ctx, app.ReportCheckRun{
		Project: r.project, Ref: "main", Trigger: domain.TriggerCLI,
		Layers:   []domain.Layer{domain.LayerParity},
		Findings: []app.ReportedFinding{reported("checkout.pay", "argument_missing", domain.Error)},
	}); err != nil {
		t.Fatal(err)
	}
	if len(r.store.inserted) != 1 {
		t.Fatalf("stored %d findings, want the one reported", len(r.store.inserted))
	}
	got := r.store.inserted[0]
	want := domain.Fingerprint(domain.LayerParity, "argument_missing",
		domain.Locus{Message: message.String(), Key: "checkout.pay", Locale: "de"}, "amount")
	if got.Fingerprint != want {
		t.Errorf("fingerprint = %s, want the server's over the catalog message (%s)", got.Fingerprint, want)
	}
	overTheKey := domain.Fingerprint(domain.LayerParity, "argument_missing",
		domain.Locus{Key: "checkout.pay", Locale: "de"}, "amount")
	if got.Fingerprint == overTheKey {
		t.Error("fingerprint is the one a client could mint over the key; every waiver against it would drift")
	}
	if got.Locus.Message != message.String() {
		t.Errorf("locus.message = %q, want the resolved catalog message", got.Locus.Message)
	}
	if got.Schema != domain.Schema {
		t.Errorf("schema = %q, want %q", got.Schema, domain.Schema)
	}
}

// TestReportedFindingFallsBackToTheKeyTheCatalogDoesNotKnow: a key
// nothing in the catalog answers to keeps its identity over the key,
// exactly as an offline check's does. Nothing is dropped and nothing
// is invented.
func TestReportedFindingFallsBackToTheKeyTheCatalogDoesNotKnow(t *testing.T) {
	r := newReporter(t)

	if _, err := r.svc.ReportCheckRun(r.ctx, app.ReportCheckRun{
		Project: r.project, Ref: "main", Trigger: domain.TriggerCLI,
		Layers:   []domain.Layer{domain.LayerParity},
		Findings: []app.ReportedFinding{reported("checkout.gone", "argument_missing", domain.Error)},
	}); err != nil {
		t.Fatal(err)
	}
	got := r.store.inserted[0]
	want := domain.Fingerprint(domain.LayerParity, "argument_missing",
		domain.Locus{Key: "checkout.gone", Locale: "de"}, "amount")
	if got.Fingerprint != want || got.Locus.Message != "" {
		t.Errorf("fingerprint = %s, message = %q; want the print over the key and no invented ID",
			got.Fingerprint, got.Locus.Message)
	}
}

// TestReportedKeysAreResolvedOnceEach: a run of many findings over few
// messages asks the catalog about the distinct keys, not about every
// row.
func TestReportedKeysAreResolvedOnceEach(t *testing.T) {
	r := newReporter(t)
	var fs []app.ReportedFinding
	for range 5 {
		fs = append(fs, reported("checkout.pay", "argument_missing", domain.Error))
		fs = append(fs, reported("", "missing_translation", domain.Warning))
	}
	if _, err := r.svc.ReportCheckRun(r.ctx, app.ReportCheckRun{
		Project: r.project, Ref: "main", Layers: []domain.Layer{domain.LayerParity}, Findings: fs,
	}); err != nil {
		t.Fatal(err)
	}
	if len(r.catalog.askedKeys) != 1 || r.catalog.askedKeys[0] != "checkout.pay" {
		t.Errorf("asked the catalog for %v, want the one distinct key and no empty one", r.catalog.askedKeys)
	}
}

// TestReportedSeverityIsThePolicysNotTheReporters: the reporter says
// what its layer emitted; the project's stored policy decides what that
// is worth here, and a rule that switches a layer off means the finding
// is not stored at all — `off` is "the project does not compute this".
func TestReportedSeverityIsThePolicysNotTheReporters(t *testing.T) {
	r := newReporter(t)
	r.catalog.policy = checkpolicy.Policy{
		Version: 7, FailOn: checkpolicy.Error,
		Rules: []checkpolicy.Rule{
			{Selector: checkpolicy.Selector{Layer: "parity", Code: "argument_missing"}, Severity: checkpolicy.Warning},
			{Selector: checkpolicy.Selector{Layer: "parity", Code: "markup_missing"}, Severity: checkpolicy.Off},
		},
	}

	run, err := r.svc.ReportCheckRun(r.ctx, app.ReportCheckRun{
		Project: r.project, Ref: "main", Trigger: domain.TriggerCLI,
		Layers: []domain.Layer{domain.LayerParity},
		Findings: []app.ReportedFinding{
			reported("checkout.pay", "argument_missing", domain.Error),
			reported("checkout.tos", "markup_missing", domain.Error),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.store.inserted) != 1 {
		t.Fatalf("stored %d findings, want only the one the policy still computes", len(r.store.inserted))
	}
	if got := r.store.inserted[0]; got.Severity != domain.Warning {
		t.Errorf("severity = %q, want the policy's warning over the reporter's error", got.Severity)
	}
	if run.Conclusion != domain.ConclusionSuccess {
		t.Errorf("conclusion = %q; a reporter's `error` the policy lowered may not fail the run", run.Conclusion)
	}
	if run.Counts != (domain.Counts{Warnings: 1}) {
		t.Errorf("counts = %+v, want the one warning the policy left", run.Counts)
	}
	if run.PolicyVersion != 7 {
		t.Errorf("policy_version = %d, want the stored document's", run.PolicyVersion)
	}
}

// TestReportedAdvisoryLayerCannotFailARun: a model's opinion is
// clamped back to warning however it arrives, because the only path
// from a reported finding to a stored one runs through the policy
// (RFC 0005 §14 decision 10).
func TestReportedAdvisoryLayerCannotFailARun(t *testing.T) {
	r := newReporter(t)
	r.catalog.policy = checkpolicy.Policy{
		FailOn: checkpolicy.Error,
		Rules:  []checkpolicy.Rule{{Selector: checkpolicy.Selector{}, Severity: checkpolicy.Error}},
	}
	f := reported("checkout.pay", "tone-mismatch", domain.Warning)
	f.Layer = domain.LayerLinguistic

	run, err := r.svc.ReportCheckRun(r.ctx, app.ReportCheckRun{
		Project: r.project, Ref: "main", Layers: []domain.Layer{domain.LayerLinguistic},
		Findings: []app.ReportedFinding{f},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.store.inserted[0].Severity; got != domain.Warning {
		t.Errorf("severity = %q, want warning: a build never fails on a model's opinion", got)
	}
	if run.Conclusion != domain.ConclusionSuccess {
		t.Errorf("conclusion = %q, want success", run.Conclusion)
	}
}

// TestReportedRunAppliesTheProjectsWaivers: the run is graded against
// the waivers that stand, here as on every other path that stores
// findings — a reporter neither applies one nor is believed about one.
func TestReportedRunRefusesAPreGradedFinding(t *testing.T) {
	r := newReporter(t)

	_, err := r.svc.ReportCheckRun(r.ctx, app.ReportCheckRun{
		Project: r.project, Ref: "main", Layers: []domain.Layer{domain.LayerParity},
		Findings: []app.ReportedFinding{reported("checkout.pay", "argument_missing", domain.Waived)},
	})
	if !errors.Is(err, app.ErrPreGradedFinding) {
		t.Fatalf("err = %v, want ErrPreGradedFinding: a reporter may not waive its own findings", err)
	}
	if len(r.store.inserted) != 0 {
		t.Error("a refused run stored findings")
	}
}

// TestReportedRunRefusesMoreThanTheCap: RFC 0005 §10's 10 000, refused
// rather than truncated — a silently shortened run is a report that
// lies about what was checked.
func TestReportedRunRefusesMoreThanTheCap(t *testing.T) {
	r := newReporter(t)
	fs := make([]app.ReportedFinding, app.MaxRunFindings+1)
	for i := range fs {
		fs[i] = reported(fmt.Sprintf("checkout.%05d", i), "argument_missing", domain.Warning)
	}

	_, err := r.svc.ReportCheckRun(r.ctx, app.ReportCheckRun{
		Project: r.project, Ref: "main", Layers: []domain.Layer{domain.LayerParity}, Findings: fs,
	})
	if !errors.Is(err, app.ErrTooManyFindings) {
		t.Fatalf("err = %v, want ErrTooManyFindings", err)
	}
	if len(r.store.inserted) != 0 || len(r.catalog.askedKeys) != 0 {
		t.Error("a run past the cap resolved keys or stored findings before being refused")
	}
}

// TestReportedRunRefusesAServerJobsTrigger: `capture` and `write` are
// the capture ingest's and the write-time job's; a caller that could
// claim one could put words in a job's mouth.
func TestReportedRunRefusesAServerJobsTrigger(t *testing.T) {
	r := newReporter(t)
	for _, trigger := range []domain.Trigger{domain.TriggerCapture, domain.TriggerWrite} {
		_, err := r.svc.ReportCheckRun(r.ctx, app.ReportCheckRun{
			Project: r.project, Ref: "main", Trigger: trigger, Layers: []domain.Layer{domain.LayerParity},
		})
		if !errors.Is(err, app.ErrUnclaimableTrigger) {
			t.Errorf("trigger %q: err = %v, want ErrUnclaimableTrigger", trigger, err)
		}
	}
}

// TestReportedRunDefaultsToTheAPITrigger: a caller that says nothing is
// an API caller, not a CLI pretending to be one.
func TestReportedRunDefaultsToTheAPITrigger(t *testing.T) {
	r := newReporter(t)

	run, err := r.svc.ReportCheckRun(r.ctx, app.ReportCheckRun{
		Project: r.project, Ref: "main", Layers: []domain.Layer{domain.LayerStructure},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Trigger != domain.TriggerAPI {
		t.Errorf("trigger = %q, want %q", run.Trigger, domain.TriggerAPI)
	}
}

// TestReportedCleanRunIsStillRecorded: "this ref was checked and found
// nothing" is the answer a dashboard needs most. A capture upload and a
// linguistic job drop a run with no findings; this one may not, or a
// green project would look unchecked.
func TestReportedCleanRunIsStillRecorded(t *testing.T) {
	r := newReporter(t)

	run, err := r.svc.ReportCheckRun(r.ctx, app.ReportCheckRun{
		Project: r.project, Ref: "main", Commit: "0123456789abcdef0123456789abcdef01234567",
		Trigger: domain.TriggerCLI, Layers: domain.Layers,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.ID == uuid.Nil || r.store.recorded.ID != run.ID {
		t.Fatalf("run %v was not stored", run.ID)
	}
	if run.Conclusion != domain.ConclusionSuccess || run.Counts.Total() != 0 {
		t.Errorf("run = %+v, want a stored, clean, successful run", run)
	}
	if len(r.store.recorded.Layers) != len(domain.Layers) {
		t.Errorf("layers = %v, want every layer the run computed: clean is not the same answer as not looked at",
			r.store.recorded.Layers)
	}
	if r.store.recorded.Commit != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("commit = %q, want the one graded", r.store.recorded.Commit)
	}
	if len(r.store.rolledUp) != 1 {
		t.Errorf("rolled up %d days, want the run's own", len(r.store.rolledUp))
	}
}

// TestReportedRunRefusesAMalformedFinding: a finding with no layer, no
// code or no explanation has no identity and nothing to say.
func TestReportedRunRefusesAMalformedFinding(t *testing.T) {
	r := newReporter(t)
	cases := map[string]func(app.ReportedFinding) app.ReportedFinding{
		"no layer":       func(f app.ReportedFinding) app.ReportedFinding { f.Layer = "guesswork"; return f },
		"no code":        func(f app.ReportedFinding) app.ReportedFinding { f.Code = ""; return f },
		"no explanation": func(f app.ReportedFinding) app.ReportedFinding { f.Explanation = ""; return f },
		"no severity":    func(f app.ReportedFinding) app.ReportedFinding { f.Severity = ""; return f },
	}
	for name, mangle := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := r.svc.ReportCheckRun(r.ctx, app.ReportCheckRun{
				Project: r.project, Ref: "main", Layers: []domain.Layer{domain.LayerParity},
				Findings: []app.ReportedFinding{mangle(reported("checkout.pay", "argument_missing", domain.Error))},
			})
			if !errors.Is(err, app.ErrInvalidFinding) {
				t.Fatalf("err = %v, want ErrInvalidFinding", err)
			}
		})
	}
}

// TestReportedRunRefusesAnInvalidRun: the domain's refusals are
// reachable from the edge now that a caller writes a run's own fields.
func TestReportedRunRefusesAnInvalidRun(t *testing.T) {
	r := newReporter(t)
	for name, in := range map[string]app.ReportCheckRun{
		"no ref":        {Project: r.project, Layers: []domain.Layer{domain.LayerParity}},
		"not a commit":  {Project: r.project, Ref: "main", Commit: "HEAD", Layers: []domain.Layer{domain.LayerParity}},
		"unknown layer": {Project: r.project, Ref: "main", Layers: []domain.Layer{"vibes"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := r.svc.ReportCheckRun(r.ctx, in); err == nil {
				t.Fatal("a malformed run was recorded")
			}
		})
	}
}

// TestReportingNeedsTheWritePermissionCIAlreadyHolds: recording is
// `catalog.write` — the ceiling a CI token already has beside
// `catalog.read` (RFC 0004 §6.3), so `glossa check` needs no wider
// token than it runs under today. A reader may ask what is wrong; only
// a writer may put a verdict on the record.
func TestReportingNeedsTheWritePermissionCIAlreadyHolds(t *testing.T) {
	svc, _, project := serviceAndCatalog(recordingStore())
	tenant := tenancy.NewID()

	if _, err := svc.ReportCheckRun(authztest.Token(t.Context(), tenant, "read"), app.ReportCheckRun{
		Project: project, Ref: "main", Layers: []domain.Layer{domain.LayerParity},
	}); err == nil {
		t.Error("a read-only token recorded a check run")
	}
	if _, err := svc.ReportCheckRun(authztest.CIToken(t.Context(), tenant), app.ReportCheckRun{
		Project: project, Ref: "main", Layers: []domain.Layer{domain.LayerParity},
	}); err != nil {
		t.Errorf("a CI token could not record its own run: %v", err)
	}
}
