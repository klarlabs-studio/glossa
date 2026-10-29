//go:build integration

package app_test

import (
	"context"
	"fmt"
	identitypg "go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	localizationidentity "go.klarlabs.de/glossa/platform/internal/localization/adapters/identity"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	mf "go.klarlabs.de/glossa/messageformat"

	catalogpg "go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres"
	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	knowledgepg "go.klarlabs.de/glossa/platform/internal/knowledge/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/knowledge/adapters/sources"
	"go.klarlabs.de/glossa/platform/internal/knowledge/app"
	catalogport "go.klarlabs.de/glossa/platform/internal/localization/adapters/catalog"
	localizationpg "go.klarlabs.de/glossa/platform/internal/localization/adapters/postgres"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
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

// wrapTranslations lets a test observe the reads Knowledge makes of
// Localization; it is the identity unless a test sets it.
var wrapTranslations = func(t app.Translations) app.Translations { return t }

// wrapTransactor lets a test observe or fail the writes Knowledge makes;
// it is the identity unless a test sets it.
var wrapTransactor = func(t app.Transactor) app.Transactor { return t }

// harness wires Catalog, Localization and Knowledge the way the
// composition root does, plus an outbox dispatcher the test drives.
type harness struct {
	catalog      *catalogapp.Service
	localization *localizationapp.Service
	svc          *app.Service
	dispatcher   *outbox.Dispatcher
	tenant       tenancy.ID
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	if err := env.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	return harnessFor(t, "acme")
}

// harnessFor adds a tenant to the current database state.
func harnessFor(t *testing.T, slug string) *harness {
	t.Helper()
	ctx := context.Background()
	tenant, err := env.SeedTenant(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	cat := catalogapp.New(catalogpg.NewTransactor(uow))
	loc := localizationapp.New(localizationpg.NewTransactor(uow), catalogport.New(cat),
		localizationapp.WithReviewers(localizationidentity.NewReviewers(identitypg.NewTransactor(uow, nil))))
	svc := app.New(wrapTransactor(knowledgepg.NewTransactor(uow)), wrapTranslations(sources.NewTranslations(loc, cat)), sources.NewProjects(cat), app.WithMessages(sources.NewMessages(cat)))
	reg := outbox.NewRegistry()
	if err := loc.Subscribe(reg); err != nil {
		t.Fatal(err)
	}
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
	return &harness{catalog: cat, localization: loc, svc: svc, dispatcher: d, tenant: tenant}
}

// drain delivers every pending event.
func (h *harness) drain(t *testing.T) {
	t.Helper()
	for range 50 {
		n, err := h.dispatcher.ProcessBatch(context.Background())
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if n == 0 {
			if dead := count(t, "SELECT count(*) FROM outbox_events WHERE status = 'dead'"); dead > 0 {
				var last string
				_ = env.Super.QueryRow(context.Background(),
					"SELECT coalesce(last_error, '') FROM outbox_events WHERE status = 'dead' LIMIT 1").Scan(&last)
				t.Fatalf("%d events dead-lettered: %s", dead, last)
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
func (h *harness) reviewer() context.Context  { return h.as([]string{"reviewer"}) }
func (h *harness) translator() context.Context {
	return h.as([]string{"translator"})
}

// project creates a project (source en, MF1) with locales and messages
// and delivers the resulting events.
func (h *harness) project(t *testing.T, slug string, reviewRequired bool, locales []string, messages map[string]string) uuid.UUID {
	t.Helper()
	ctx := h.developer()
	settings := catalogdomain.Settings{DefaultSyntax: mfcontent.MF1, ReviewRequired: reviewRequired}
	p, _, err := h.catalog.CreateProject(ctx, catalogapp.NewProject{
		Slug: slug, Name: slug, SourceLocale: "en", Settings: &settings,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range locales {
		if _, _, err := h.localization.AddLocale(ctx, p.ID.UUID(), l); err != nil {
			t.Fatalf("add locale %s: %v", l, err)
		}
	}
	h.push(t, p.ID.UUID(), messages)
	return p.ID.UUID()
}

func (h *harness) push(t *testing.T, project uuid.UUID, messages map[string]string) {
	t.Helper()
	var items []catalogapp.UpsertItem
	for k, text := range messages {
		items = append(items, catalogapp.UpsertItem{Key: k, Text: text})
	}
	if len(items) > 0 {
		res, err := h.catalog.UpsertMessages(h.developer(), catalogdomain.ProjectID(project), items)
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
}

// translate writes a translation as a reviewer (state nil: the
// project's policy), creating or revising it.
func (h *harness) translate(t *testing.T, project uuid.UUID, key, locale, text string, state *string) localizationapp.TranslationView {
	t.Helper()
	ctx := h.reviewer()
	var ifMatch *int
	if cur, err := h.localization.GetTranslation(ctx, project, key, locale); err == nil {
		ifMatch = &cur.Revision
	}
	v, _, err := h.localization.PutTranslation(ctx, project, key, locale,
		localizationapp.TranslationInput{Text: text, State: state}, ifMatch)
	if err != nil {
		t.Fatalf("translate %s/%s: %v", key, locale, err)
	}
	return v
}

// translateMF2 writes an approved MF2 translation as a reviewer.
func (h *harness) translateMF2(t *testing.T, project uuid.UUID, key, locale, text string) {
	t.Helper()
	approved := "approved"
	if _, _, err := h.localization.PutTranslation(h.reviewer(), project, key, locale,
		localizationapp.TranslationInput{Text: text, Syntax: "mf2", State: &approved}, nil); err != nil {
		t.Fatalf("translate %s/%s: %v", key, locale, err)
	}
}

func (h *harness) review(t *testing.T, project uuid.UUID, key, locale, state string) {
	t.Helper()
	ctx := h.reviewer()
	cur, err := h.localization.GetTranslation(ctx, project, key, locale)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.localization.ReviewTranslation(ctx, project, key, locale, state, cur.Revision); err != nil {
		t.Fatalf("review %s/%s → %s: %v", key, locale, state, err)
	}
}

func count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := env.Super.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func firstPage() pagination.Page { return pagination.Page{Size: pagination.MaxPageSize} }

func ptr[T any](v T) *T { return &v }

func tag(s string) bcp47.Tag { return bcp47.MustParse(s) }

func mf2(t *testing.T, src string) mf.Message {
	t.Helper()
	m, err := mf.ParseMF2(src)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mf1(t *testing.T, src string) mf.Message {
	t.Helper()
	m, err := mf.ParseMF1(src, "en")
	if err != nil {
		t.Fatal(err)
	}
	return m
}
