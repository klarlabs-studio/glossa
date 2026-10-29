package app_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// RFC 0005 §11 on logs: they carry check run ids, policy versions,
// fingerprints and token ids, and **never message text, never
// translation text, never image bytes**.
//
// Findings are the one place in this context where product copy is
// routinely in hand — an explanation quotes it, a subject *is* a term,
// a fix hint is a proposed translation — and they pass through every
// write path here. So the assertion is blunt and cheap: drive the
// paths that hold copy, with the copy spelled as something no format
// string could produce by accident, and read the whole log back.

// sentinels are strings that only ever come from a finding's product
// copy. If one reaches the log, the log is quoting somebody's catalog.
var sentinels = []string{
	"Zahlen Sie jetzt vierzig Euro", // an explanation quoting the source
	"Payer maintenant",              // a fix hint, which is a translation
	"Anmeldung",                     // a subject, which is a term
}

func TestNoLogCarriesMessageOrTranslationText(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	store := recordingStore()
	svc, catalog, project := serviceAndCatalog(store)
	svc = app.NewService(fakeTx{store: store}, catalog, app.WithLogger(logger))

	rev := 7
	if _, err := svc.ReportCheckRun(writeCtx(t), app.ReportCheckRun{
		Project: project, Ref: "feat/checkout", Commit: "9f2c1e7a4b3d5c6e8f0a1b2c3d4e5f6a7b8c9d0e",
		Trigger: domain.TriggerCLI, Layers: []domain.Layer{domain.LayerTerminology},
		Findings: []app.ReportedFinding{{
			Layer: domain.LayerTerminology, Code: "term_forbidden", Severity: domain.Error,
			Locus:          app.ReportedLocus{Key: "nav.login", Locale: "de", Namespace: "nav"},
			Explanation:    "the German says " + sentinels[0],
			Subject:        sentinels[2],
			Fix:            &domain.Fix{Kind: domain.FixReplace, Hint: sentinels[1]},
			Evidence:       map[string]any{"text": sentinels[0]},
			SourceRevision: &rev,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	// A policy save is the one path here that logs on purpose.
	if _, err := svc.SavePolicy(writeCtx(t), project, app.SavePolicy{
		Policy: checkpolicy.Policy{RequireComplete: []string{"de"}},
	}); err != nil {
		t.Fatal(err)
	}

	// And the sweep, which visits a tenant and says so.
	if _, err := svc.SweepTenant(writeCtx(t)); err != nil {
		t.Fatal(err)
	}

	logged := buf.String()
	for _, s := range sentinels {
		if strings.Contains(logged, s) {
			t.Errorf("the log quotes product copy (%q):\n%s", s, logged)
		}
	}
	// The same thing said the other way round: the paths that do log say
	// what §11 allows them to. A policy save names the project and the
	// version it wrote.
	if !strings.Contains(logged, `"version":1`) || !strings.Contains(logged, project.String()) {
		t.Errorf("the policy save logged neither its project nor its version:\n%s", logged)
	}
}

// A sweep that fails logs the tenant and the error, and nothing else:
// it never reads a reason, a fingerprint's finding or a message.
var errSweep = errors.New("storage is down")

func TestAFailingSweepLogsOnlyTheTenantAndTheError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	store := &fakeStore{sweepErr: errSweep}
	tenant := tenancy.NewID()
	scan := &oneTenant{id: tenant}
	svc := app.NewService(fakeTx{store: store}, &knownProjects{},
		app.WithScanner(scan), app.WithLogger(logger))

	if _, err := svc.Sweep(t.Context()); err == nil {
		t.Fatal("the sweep hid a failing tenant")
	}
	logged := buf.String()
	if !strings.Contains(logged, tenant.String()) {
		t.Errorf("the sweep did not say which tenant failed:\n%s", logged)
	}
	for _, s := range sentinels {
		if strings.Contains(logged, s) {
			t.Errorf("the sweep log quotes product copy (%q)", s)
		}
	}
}
