//go:build integration

package app_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
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

type harness struct {
	svc    *app.Service
	tenant tenancy.ID
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	if err := env.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	tenant, err := env.SeedTenant(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	return &harness{svc: app.New(postgres.NewTransactor(db.NewUnitOfWork(env.App))), tenant: tenant}
}

func (h *harness) developer() context.Context {
	return authztest.Member(context.Background(), h.tenant, []string{"developer"})
}

func (h *harness) project(t *testing.T, ctx context.Context) domain.Project {
	t.Helper()
	p, _, err := h.svc.CreateProject(ctx, app.NewProject{Slug: "brotwerk", Name: "Brotwerk", SourceLocale: "en"}, "")
	if err != nil {
		t.Fatal(err)
	}
	return p
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
