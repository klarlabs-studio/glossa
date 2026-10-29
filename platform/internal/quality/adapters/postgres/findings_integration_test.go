//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	qualitypg "go.klarlabs.de/glossa/platform/internal/quality/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The store's own questions: what row-level security does to a query
// that names another tenant's run, and whether a finding survives its
// columns whole. What the queries select and how they page is graded
// through the service in internal/quality/app, against the same
// database and through the writer that actually fills these rows.

var env *dbtest.Env

func TestMain(m *testing.M) {
	var err error
	env, err = dbtest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	env.Close()
	os.Exit(code)
}

func inTenant(t *testing.T, uow *db.UnitOfWork, tenant tenancy.ID, fn func(context.Context, app.Store) error) error {
	t.Helper()
	return qualitypg.NewTransactor(uow).InTenant(tenancy.ContextWithTenant(t.Context(), tenant), fn)
}

// seedRun writes one completed run and its findings through the store,
// so the insert's column mapping is under test alongside the read.
func seedRun(
	t *testing.T, uow *db.UnitOfWork, tenant tenancy.ID, project uuid.UUID,
	ref string, started time.Time, findings []domain.Finding,
) domain.CheckRun {
	t.Helper()
	counts, conclusion := domain.Conclude(failsOnError{}, findings)
	run := domain.CheckRun{
		ID: uuid.Must(uuid.NewV7()), Project: project, Ref: ref, Trigger: domain.TriggerCLI, PolicyVersion: 3,
		Layers: []domain.Layer{domain.LayerStructure, domain.LayerParity}, Counts: counts,
		Conclusion: conclusion, CreatedBy: "token:seed", StartedAt: started, CompletedAt: started,
	}
	err := inTenant(t, uow, tenant, func(ctx context.Context, st app.Store) error {
		if err := st.InsertCheckRun(ctx, run); err != nil {
			return err
		}
		return st.InsertFindings(ctx, run.ID, project, findings)
	})
	if err != nil {
		t.Fatalf("seed a run: %v", err)
	}
	return run
}

// failsOnError is the default policy's verdict, spelled out so the
// seed doesn't depend on a policy the store knows nothing about.
type failsOnError struct{}

func (failsOnError) Fails(s domain.Severity) bool { return s == domain.Error }

// TestFindingsAreTenantIsolated: findings are tenant-owned under forced
// row-level security, so another tenant's run is not there whatever id
// it asks for. The service can't answer this on its own — it refuses an
// unknown project before it ever reaches a quality table — so the guard
// on the tables themselves belongs here.
func TestFindingsAreTenantIsolated(t *testing.T) {
	ctx := t.Context()
	if err := env.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := env.SeedTenant(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := env.SeedTenant(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	now := time.Now().UTC().Truncate(time.Millisecond)
	projectA, projectB := uuid.New(), uuid.New()
	runA := seedRun(t, uow, first, projectA, "main", now, []domain.Finding{
		finding(domain.LayerParity, "argument_missing", "checkout.pay", "de", domain.Error),
	})
	seedRun(t, uow, second, projectB, "main", now, []domain.Finding{
		finding(domain.LayerStyle, "secret_code", "secret.key", "fr", domain.Warning),
	})

	// The second tenant's project is simply not there for the first …
	err = inTenant(t, uow, first, func(ctx context.Context, st app.Store) error {
		if _, err := st.LatestCheckRun(ctx, projectB, app.RunFilter{}); !errors.Is(err, app.ErrCheckRunNotFound) {
			t.Errorf("another tenant's project answered %v, want not found", err)
		}
		got, err := st.ListFindings(ctx, runA, app.FindingFilter{}, "", 10, now)
		if err != nil {
			return err
		}
		if len(got) != 1 || got[0].Locus.Key != "checkout.pay" {
			t.Errorf("findings = %+v, want the first tenant's own", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// … and naming the first tenant's run id directly returns nothing.
	err = inTenant(t, uow, second, func(ctx context.Context, st app.Store) error {
		if _, err := st.CheckRun(ctx, projectA, runA.ID); !errors.Is(err, app.ErrCheckRunNotFound) {
			t.Errorf("the second tenant read the first's run: %v", err)
		}
		got, err := st.ListFindings(ctx, runA, app.FindingFilter{}, "", 10, now)
		if err != nil {
			return err
		}
		if len(got) != 0 {
			t.Errorf("the second tenant read the first's findings: %+v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestFindingRoundTrip: a finding is stored across two dozen columns,
// and every one of them is something a surface renders — the file and
// line an annotation needs, the span the overlay underlines, the
// evidence and the fix. A conversion that drops one is the failure
// M4 exists to end (RFC 0005 §14 decision 1), so the whole locus goes
// down and comes back.
func TestFindingRoundTrip(t *testing.T) {
	ctx := t.Context()
	if err := env.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	tenant, err := env.SeedTenant(ctx, "roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	project := uuid.New()
	now := time.Now().UTC()
	revision := 41
	to := 30
	message, translation, capture := uuid.New(), uuid.New(), uuid.New()
	want := domain.New(domain.Finding{
		Layer: domain.LayerLength, Code: "too_long", Severity: domain.Error, Subject: "checkout.pay",
		Detail: "expansion", Message: "the German is 40% longer than the source",
		Locus: domain.Locus{
			Message: message.String(), Key: "checkout.pay", Locale: "de", Revision: translation.String(),
			Namespace: "checkout", File: "src/Checkout.vue", Line: 12, Column: 7,
			Route: "/checkout", Component: "PayButton", Capture: capture.String(), Region: "cta",
			Span: &domain.Span{Side: domain.SideTarget, Start: 3, End: 11},
		},
		Evidence:       map[string]any{"limit": float64(30), "measured": float64(42)},
		Fix:            &domain.Fix{Kind: domain.FixShorten, To: &to, Hint: "Bezahlen"},
		SourceRevision: &revision,
	})
	run := seedRun(t, uow, tenant, project, "main", now, []domain.Finding{want})

	err = inTenant(t, uow, tenant, func(ctx context.Context, st app.Store) error {
		got, err := st.ListFindings(ctx, run, app.FindingFilter{}, "", 10, now)
		if err != nil {
			return err
		}
		if len(got) != 1 {
			t.Fatalf("findings = %d, want 1", len(got))
		}
		f := got[0]
		if f.Fingerprint != want.Fingerprint || f.Schema != domain.Schema {
			t.Errorf("identity = %q/%q, want %q/%q", f.Schema, f.Fingerprint, domain.Schema, want.Fingerprint)
		}
		if f.Locus != want.Locus {
			// Span is a pointer, so compare what it points at too.
			if f.Locus.Span == nil || want.Locus.Span == nil || *f.Locus.Span != *want.Locus.Span {
				t.Errorf("span = %+v, want %+v", f.Locus.Span, want.Locus.Span)
			}
			f.Locus.Span, want.Locus.Span = nil, nil
			if f.Locus != want.Locus {
				t.Errorf("locus = %+v, want %+v", f.Locus, want.Locus)
			}
		}
		if f.Message != want.Message || f.Subject != want.Subject || f.Detail != want.Detail {
			t.Errorf("prose = %+v", f.Finding)
		}
		if f.Evidence["limit"] != float64(30) || f.Evidence["measured"] != float64(42) {
			t.Errorf("evidence = %+v, want what the layer measured", f.Evidence)
		}
		if f.Fix == nil || f.Fix.Kind != domain.FixShorten || f.Fix.To == nil || *f.Fix.To != to {
			t.Errorf("fix = %+v", f.Fix)
		}
		if f.SourceRevision == nil || *f.SourceRevision != revision {
			t.Errorf("source revision = %v, want %d: a waiver dies when it moves", f.SourceRevision, revision)
		}
		if f.Waived || f.SortKey == "" {
			t.Errorf("record = waived %v, sort key %q", f.Waived, f.SortKey)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func finding(layer domain.Layer, code, key, locale string, severity domain.Severity) domain.Finding {
	return domain.New(domain.Finding{
		Layer: layer, Code: code, Severity: severity, Subject: code,
		Locus:   domain.Locus{Key: key, Locale: locale, Namespace: "checkout", File: "src/Checkout.vue", Line: 12},
		Message: "something is wrong with " + key,
	})
}
