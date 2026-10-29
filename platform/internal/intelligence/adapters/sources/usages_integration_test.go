//go:build integration

package sources_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	catalogpg "go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres"
	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	contextcatalog "go.klarlabs.de/glossa/platform/internal/context/adapters/catalog"
	contextpg "go.klarlabs.de/glossa/platform/internal/context/adapters/postgres"
	contextapp "go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/sources"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
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

// The Usages port reads the Context context's service as the AI job
// worker's principal would (catalog.read).
func TestUsagesPortReadsCurrentUsagesAndCoLocatedMessages(t *testing.T) {
	ctx := context.Background()
	if err := env.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	tenant, err := env.SeedTenant(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	cat := catalogapp.New(catalogpg.NewTransactor(uow))
	svc := contextapp.New(contextpg.NewTransactor(uow), contextcatalog.New(cat))
	dev := authztest.Member(ctx, tenant, []string{"developer"})
	p, _, err := cat.CreateProject(dev, catalogapp.NewProject{Slug: "shop", Name: "Shop", SourceLocale: "en"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cat.CreateApplication(dev, p.ID, catalogapp.NewApplication{Slug: "web", Name: "Web", Platform: "web"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.UpsertMessages(dev, p.ID, []catalogapp.UpsertItem{
		{Key: "checkout.pay", Text: "Pay"}, {Key: "checkout.total", Text: "Total"}, {Key: "nav.home", Text: "Home"},
	}); err != nil {
		t.Fatal(err)
	}
	ids, err := cat.MessagesByKeys(dev, p.ID, []string{"checkout.pay", "checkout.total"})
	if err != nil {
		t.Fatal(err)
	}
	doc := `{"schema":"glossa.usages/v1","application":"web","commit":"` + strings.Repeat("a", 40) + `","branch":"main",
	  "tool":{"name":"@klarlabs-studio/glossa","version":"0.1.0"},"usages":[
	  {"key":"checkout.pay","file":"src/Pay.vue","line":3,"column":5,"component":"Pay","route":"/checkout","kind":"t"},
	  {"key":"checkout.total","file":"src/Pay.vue","line":9,"column":5,"component":"Pay","route":"/checkout","kind":"t"},
	  {"key":"nav.home","file":"index.html","line":1,"column":5,"kind":"element"}]}`
	if _, err := svc.IngestUsages(authztest.Token(ctx, tenant, "write"), contextapp.IngestUsages{
		Project: p.ID.UUID(), Source: "plugin", Document: []byte(doc),
	}); err != nil {
		t.Fatal(err)
	}

	worker, err := authz.Background(tenancy.ContextWithTenant(ctx, tenant), "intelligence.test", authz.CatalogRead)
	if err != nil {
		t.Fatal(err)
	}
	port := sources.NewUsages(svc)
	pay, total := ids["checkout.pay"].ID.UUID(), ids["checkout.total"].ID.UUID()
	got, err := port.Usages(worker, p.ID.UUID(), pay, 10)
	if err != nil || len(got) != 1 || got[0] != (app.Usage{File: "src/Pay.vue", Line: 3, Component: "Pay", Route: "/checkout"}) {
		t.Errorf("usages = %+v, err %v", got, err)
	}
	co, err := port.CoLocated(worker, p.ID.UUID(), pay, 5)
	if err != nil || !slices.Equal(co, []uuid.UUID{total}) {
		t.Errorf("co-located = %v, err %v", co, err)
	}
	if _, err := port.Usages(worker, uuid.New(), pay, 10); !errors.Is(err, app.ErrProjectNotFound) {
		t.Errorf("unknown project: err = %v", err)
	}
}
