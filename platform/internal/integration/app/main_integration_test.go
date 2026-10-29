//go:build integration

package app_test

import (
	"bytes"
	"context"
	"fmt"
	identitypg "go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	localizationidentity "go.klarlabs.de/glossa/platform/internal/localization/adapters/identity"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	catalogpg "go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres"
	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	clicatalog "go.klarlabs.de/glossa/platform/internal/cli/catalog"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	integrationpg "go.klarlabs.de/glossa/platform/internal/integration/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/integration/adapters/sources"
	"go.klarlabs.de/glossa/platform/internal/integration/app"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/s3store"
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore/s3store/s3test"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	knowledgepg "go.klarlabs.de/glossa/platform/internal/knowledge/adapters/postgres"
	knowledgesources "go.klarlabs.de/glossa/platform/internal/knowledge/adapters/sources"
	knowledgeapp "go.klarlabs.de/glossa/platform/internal/knowledge/app"
	knowledgedomain "go.klarlabs.de/glossa/platform/internal/knowledge/domain"
	catalogport "go.klarlabs.de/glossa/platform/internal/localization/adapters/catalog"
	localizationpg "go.klarlabs.de/glossa/platform/internal/localization/adapters/postgres"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
)

var (
	env     *dbtest.Env
	objects *s3store.Store
)

// TestMain boots Postgres (the app role, without BYPASSRLS) and MinIO
// once: uploads and exports stream through a real S3 API.
func TestMain(m *testing.M) {
	ctx := context.Background()
	var err error
	if env, err = dbtest.Start(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	minio, err := s3test.Start(ctx)
	if err != nil {
		env.Close()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if objects, err = minio.Store(ctx, "glossa-integration"); err != nil {
		minio.Close()
		env.Close()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	minio.Close()
	env.Close()
	os.Exit(code)
}

// harness wires Catalog, Localization, Knowledge and Integration the way
// the composition root does, with a worker and an outbox dispatcher the
// test drives by hand.
type harness struct {
	catalog      *catalogapp.Service
	localization *localizationapp.Service
	knowledge    *knowledgeapp.Service
	svc          *app.Service
	worker       *app.Worker
	dispatcher   *outbox.Dispatcher
	tenant       tenancy.ID
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
	uow := db.NewUnitOfWork(env.App)
	cat := catalogapp.New(catalogpg.NewTransactor(uow))
	loc := localizationapp.New(localizationpg.NewTransactor(uow), catalogport.New(cat),
		localizationapp.WithReviewers(localizationidentity.NewReviewers(identitypg.NewTransactor(uow, nil))))
	know := knowledgeapp.New(knowledgepg.NewTransactor(uow), knowledgesources.NewTranslations(loc, cat), knowledgesources.NewProjects(cat))
	svc := app.New(app.Deps{
		Tx: integrationpg.NewTransactor(uow), Catalog: sources.NewCatalog(cat), Localization: sources.NewLocalization(loc, cat),
		Knowledge: sources.NewKnowledge(know), Objects: objects, Config: app.Config{MaxUploadBytes: 1 << 20},
	})
	reg := outbox.NewRegistry()
	for _, sub := range []interface{ Subscribe(*outbox.Registry) error }{loc, know, svc} {
		if err := sub.Subscribe(reg); err != nil {
			t.Fatal(err)
		}
	}
	d, err := outbox.NewDispatcher(outbox.NewPostgresStore(uow), reg, outbox.DispatcherConfig{
		BatchSize: 100, MaxAttempts: 3, PollInterval: 10 * time.Millisecond, Lease: time.Minute,
		HandlerTimeout: 10 * time.Second, InlineAttempts: 1, InlineBackoff: time.Millisecond,
	}, outbox.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{
		catalog: cat, localization: loc, knowledge: know, svc: svc, dispatcher: d, tenant: tenant,
		worker: app.NewWorker(svc, integrationpg.NewClaimer(uow), app.WorkerConfig{JobTimeout: time.Minute}),
	}
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
				t.Fatalf("%d events dead-lettered", dead)
			}
			return
		}
	}
	t.Fatal("outbox did not drain")
}

// work runs every due job, then delivers the events they caused.
func (h *harness) work(t *testing.T) {
	t.Helper()
	for range 100 {
		worked, err := h.worker.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("worker: %v", err)
		}
		if !worked {
			h.drain(t)
			return
		}
	}
	t.Fatal("the queue did not empty")
}

func (h *harness) as(roles []string, locales ...string) context.Context {
	return authztest.Member(context.Background(), h.tenant, roles, locales...)
}

func (h *harness) owner() context.Context     { return h.as([]string{"owner"}) }
func (h *harness) developer() context.Context { return h.as([]string{"developer"}) }

// project creates a project (source en) with locales and MF1 messages.
func (h *harness) project(t *testing.T, slug string, reviewRequired bool, locales []string, messages map[string]string) uuid.UUID {
	t.Helper()
	ctx := h.developer()
	settings := catalogdomain.Settings{DefaultSyntax: mfcontent.MF1, ReviewRequired: reviewRequired}
	p, _, err := h.catalog.CreateProject(ctx, catalogapp.NewProject{Slug: slug, Name: slug, SourceLocale: "en", Settings: &settings}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range locales {
		if _, _, err := h.localization.AddLocale(ctx, p.ID.UUID(), l); err != nil {
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

// translate writes text in state as an owner.
func (h *harness) translate(t *testing.T, project uuid.UUID, key, locale, text, state string) {
	t.Helper()
	ctx := h.owner()
	var ifMatch *int
	if cur, err := h.localization.GetTranslation(ctx, project, key, locale); err == nil {
		ifMatch = &cur.Revision
	}
	if _, _, err := h.localization.PutTranslation(ctx, project, key, locale,
		localizationapp.TranslationInput{Text: text, State: &state}, ifMatch); err != nil {
		t.Fatalf("translate %s %s: %v", key, locale, err)
	}
}

// importFile creates an import, uploads body and runs the worker.
func (h *harness) importFile(t *testing.T, ctx context.Context, req app.ImportRequest, body string) domain.Job {
	t.Helper()
	j, _, err := h.svc.CreateImport(ctx, req, "")
	if err != nil {
		t.Fatalf("create import: %v", err)
	}
	if j, err = h.svc.UploadImport(ctx, j.ID, strings.NewReader(body)); err != nil {
		t.Fatalf("upload: %v", err)
	}
	h.work(t)
	if j, err = h.svc.GetImport(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	return j
}

// results lists every result of an import.
func (h *harness) results(t *testing.T, ctx context.Context, job uuid.UUID) []domain.Item {
	t.Helper()
	items, _, err := h.svc.ImportResults(ctx, job, app.ItemFilter{}, pagination.Page{Size: pagination.MaxPageSize})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// export runs an export and downloads its file.
func (h *harness) export(t *testing.T, ctx context.Context, req app.ExportRequest) (domain.Job, []byte) {
	t.Helper()
	j, _, err := h.svc.CreateExport(ctx, req, "")
	if err != nil {
		t.Fatalf("create export: %v", err)
	}
	h.work(t)
	rc, j, err := h.svc.OpenExport(ctx, j.ID)
	if err != nil {
		t.Fatalf("download export (%s %s %s): %v", j.State, j.FailureCode, j.FailureMessage, err)
	}
	defer rc.Close()
	var b bytes.Buffer
	if _, err := io.Copy(&b, rc); err != nil {
		t.Fatal(err)
	}
	return j, b.Bytes()
}

func count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := env.Super.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func ptr[T any](v T) *T { return &v }

// cliPull is what `glossa pull` writes for entries (flat style).
func cliPull(entries map[string]string) ([]byte, error) {
	return clicatalog.Encode(entries, clicatalog.Flat)
}

// knowledgeTerm is a concept with a preferred en and de term.
func knowledgeTerm(en, de string) knowledgedomain.ConceptInput {
	return knowledgedomain.ConceptInput{Definition: "A bill", Terms: []knowledgedomain.TermInput{
		{Locale: "en", Text: en, Status: "preferred"}, {Locale: "de", Text: de, Status: "preferred"},
	}}
}

func authzMember(tenant tenancy.ID, roles ...string) context.Context {
	return authztest.Member(context.Background(), tenant, roles)
}

// byKey indexes results by kind, key and locale.
func byKey(items []domain.Item) map[string]domain.Item {
	out := map[string]domain.Item{}
	for _, it := range items {
		out[string(it.Kind)+" "+it.Key+" "+it.Locale] = it
	}
	return out
}
