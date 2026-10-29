//go:build integration

package app_test

import (
	"context"
	"fmt"
	identitypg "go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	localizationidentity "go.klarlabs.de/glossa/platform/internal/localization/adapters/identity"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/coverage"
	catalogpg "go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres"
	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	localizationcatalog "go.klarlabs.de/glossa/platform/internal/localization/adapters/catalog"
	localizationpg "go.klarlabs.de/glossa/platform/internal/localization/adapters/postgres"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	qualitycatalog "go.klarlabs.de/glossa/platform/internal/quality/adapters/catalog"
	qualitypg "go.klarlabs.de/glossa/platform/internal/quality/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
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

// clock is a settable time source.
type clock struct{ t time.Time }

func newClock() *clock                   { return &clock{t: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)} }
func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

// harness wires Catalog and Quality the way the composition root does.
type harness struct {
	catalog *catalogapp.Service
	locales *localizationapp.Service
	svc     *app.Service
	tenant  tenancy.ID
	clock   *clock
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if err := env.Reset(context.Background()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	return harnessFor(t, "acme")
}

// harnessFor adds a tenant to the current database state.
func harnessFor(t *testing.T, slug string) *harness {
	t.Helper()
	tenant, err := env.SeedTenant(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	clk := newClock()
	cat := catalogapp.New(catalogpg.NewTransactor(uow), catalogapp.WithClock(clk.now))
	loc := localizationapp.New(localizationpg.NewTransactor(uow), localizationcatalog.New(cat),
		localizationapp.WithReviewers(localizationidentity.NewReviewers(identitypg.NewTransactor(uow, nil))))
	// As the composition root wires it: a policy's required locales are
	// validated against Localization through the port that answers to
	// the write's own catalog.write, not to translations.read.
	cat.SetLocales(coverage.NewPolicyLocales(loc))
	svc := app.NewService(qualitypg.NewTransactor(uow), qualitycatalog.New(cat), app.WithClock(clk.now))
	return &harness{catalog: cat, locales: loc, svc: svc, tenant: tenant, clock: clk}
}

func (h *harness) developer() context.Context {
	return authztest.Member(context.Background(), h.tenant, []string{"developer"})
}

func (h *harness) translator() context.Context {
	return authztest.Member(context.Background(), h.tenant, []string{"translator"}, "de")
}

// ci acts as a repository's CI token: catalog.read and catalog.write,
// and nothing else — what `glossa policy import` runs as.
func (h *harness) ci() context.Context { return authztest.CIToken(context.Background(), h.tenant) }

// addLocale gives the project a target locale.
func (h *harness) addLocale(t *testing.T, project uuid.UUID, code string) {
	t.Helper()
	if _, _, err := h.locales.AddLocale(h.developer(), project, code); err != nil {
		t.Fatalf("add locale %s: %v", code, err)
	}
}

// project creates a project and returns its ID.
func (h *harness) project(t *testing.T, slug string) uuid.UUID {
	t.Helper()
	settings := catalogdomain.Settings{DefaultSyntax: mfcontent.MF1}
	p, _, err := h.catalog.CreateProject(h.developer(),
		catalogapp.NewProject{Slug: slug, Name: slug, SourceLocale: "en", Settings: &settings}, "")
	if err != nil {
		t.Fatal(err)
	}
	return p.ID.UUID()
}

// sha is a full commit ID from a short one.
func sha(short string) string { return short + strings.Repeat("0", 40-len(short)) }

// finding builds a sealed finding, so every test's fingerprints are the
// ones the layers would compute.
func finding(layer domain.Layer, code string, l domain.Locus, severity domain.Severity, opts ...func(*domain.Finding)) domain.Finding {
	f := domain.Finding{Layer: layer, Code: code, Locus: l, Severity: severity, Message: code + " in " + l.Key}
	for _, o := range opts {
		o(&f)
	}
	return domain.New(f)
}

func at(revision int) func(*domain.Finding) {
	return func(f *domain.Finding) { f.SourceRevision = &revision }
}

func subject(s string) func(*domain.Finding) {
	return func(f *domain.Finding) { f.Subject = s }
}
