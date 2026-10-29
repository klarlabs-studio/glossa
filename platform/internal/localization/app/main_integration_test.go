//go:build integration

package app_test

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/coverage"
	catalogpg "go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/projection"
	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	catalogport "go.klarlabs.de/glossa/platform/internal/localization/adapters/catalog"
	"go.klarlabs.de/glossa/platform/internal/localization/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
)

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

// harness wires Catalog and Localization the way the composition root
// does, plus an outbox dispatcher the test drives by hand.
type harness struct {
	catalog    *catalogapp.Service
	svc        *app.Service
	dispatcher *outbox.Dispatcher
	tenant     tenancy.ID
}

func newHarness(t *testing.T) *harness { return newHarnessProjecting(t, true) }

// newHarnessProjecting is newHarness, but without Catalog's synchronous
// projection when project is false: only the outbox subscriber then
// brings Localization's message projection up to date.
func newHarnessProjecting(t *testing.T, project bool) *harness {
	t.Helper()
	ctx := context.Background()
	if err := env.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	tenant, err := env.SeedTenant(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	cat := catalogapp.New(catalogpg.NewTransactor(uow))
	svc := app.New(postgres.NewTransactor(uow), catalogport.New(cat))
	cat.SetCoverage(coverage.New(svc))
	cat.SetLocales(coverage.NewPolicyLocales(svc))
	if project {
		cat.SetProjection(projection.New(svc))
	}
	reg := outbox.NewRegistry()
	if err := svc.Subscribe(reg); err != nil {
		t.Fatal(err)
	}
	d, err := outbox.NewDispatcher(outbox.NewPostgresStore(uow), reg, outbox.DispatcherConfig{
		BatchSize: 100, MaxAttempts: 3, PollInterval: 10 * time.Millisecond, Lease: time.Minute,
		HandlerTimeout: 10 * time.Second, InlineAttempts: 1, InlineBackoff: time.Millisecond,
	}, outbox.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{catalog: cat, svc: svc, dispatcher: d, tenant: tenant}
}

// drain delivers every pending event.
func (h *harness) drain(t *testing.T) {
	t.Helper()
	for i := 0; i < 20; i++ {
		n, err := h.dispatcher.ProcessBatch(context.Background())
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if n == 0 {
			if dead := count(t, "SELECT count(*) FROM outbox_events WHERE status = 'dead'"); dead > 0 {
				t.Fatalf("%d events dead-lettered", dead)
			}
			return
		}
	}
	t.Fatal("outbox did not drain")
}

func (h *harness) as(roles []string, locales ...string) context.Context {
	return authztest.Member(context.Background(), h.tenant, roles, locales...)
}

func (h *harness) developer() context.Context { return h.as([]string{"developer"}) }

// ci acts as a repository's CI token: catalog.read and catalog.write,
// and nothing else.
func (h *harness) ci() context.Context { return authztest.CIToken(context.Background(), h.tenant) }

// setup creates a project (source en) with locales and messages pushed
// in MF1, and delivers the resulting events.
func (h *harness) setup(t *testing.T, reviewRequired bool, locales []string, messages map[string]string) uuid.UUID {
	t.Helper()
	ctx := h.developer()
	settings := catalogdomain.Settings{DefaultSyntax: mfcontent.MF1, ReviewRequired: reviewRequired}
	p, _, err := h.catalog.CreateProject(ctx, catalogapp.NewProject{
		Slug: "brotwerk", Name: "Brotwerk", SourceLocale: "en", Settings: &settings,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range locales {
		if _, _, err := h.svc.AddLocale(ctx, p.ID.UUID(), l); err != nil {
			t.Fatalf("add locale %s: %v", l, err)
		}
	}
	var items []catalogapp.UpsertItem
	for _, k := range slices.Sorted(maps.Keys(messages)) {
		items = append(items, catalogapp.UpsertItem{Key: k, Text: messages[k]})
	}
	if len(items) > 0 {
		res, err := h.catalog.UpsertMessages(ctx, p.ID, items)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res {
			if r.Error != nil {
				t.Fatalf("push %s: %+v", r.Key, r.Error)
			}
		}
	}
	h.drain(t)
	return p.ID.UUID()
}

func count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := env.Super.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func firstPage() pagination.Page { return pagination.Page{Size: pagination.DefaultPageSize} }

func ptr[T any](v T) *T { return &v }
