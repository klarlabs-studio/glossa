//go:build integration

package app_test

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	identitypg "go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	localizationidentity "go.klarlabs.de/glossa/platform/internal/localization/adapters/identity"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/coverage"
	catalogpg "go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/projection"
	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/cassette"
	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/metrics"
	intelligencepg "go.klarlabs.de/glossa/platform/internal/intelligence/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/sources"
	"go.klarlabs.de/glossa/platform/internal/intelligence/app"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/sealing"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	knowledgepg "go.klarlabs.de/glossa/platform/internal/knowledge/adapters/postgres"
	knowledgesources "go.klarlabs.de/glossa/platform/internal/knowledge/adapters/sources"
	knowledgeapp "go.klarlabs.de/glossa/platform/internal/knowledge/app"
	catalogport "go.klarlabs.de/glossa/platform/internal/localization/adapters/catalog"
	localizationpg "go.klarlabs.de/glossa/platform/internal/localization/adapters/postgres"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
)

// The wiring's integration tests run the real contexts on Postgres (the
// app role, without BYPASSRLS) with the job queue driven by the test.
// Providers are cassettes (testdata/wiring/<test>.json) recorded from
// the scripted answers each test declares — never a real provider.
// Re-record after a prompt change:
//
//	go test -tags=integration ./internal/intelligence/app -run TestWiring -record-wiring

var (
	wenv         *dbtest.Env
	recordWiring = flag.Bool("record-wiring", false, "re-record testdata/wiring cassettes from the tests' scripted answers")
)

func TestMain(m *testing.M) {
	flag.Parse()
	var err error
	wenv, err = dbtest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	wenv.Close()
	os.Exit(code)
}

// answer is one scripted provider answer: text, or a provider failure.
type answer struct {
	text string
	err  *domain.ProviderError
}

// scripted answers each task from a queue (recording only).
type scripted struct {
	mu      sync.Mutex
	answers map[domain.Task][]answer
}

func (s *scripted) Name() string { return "anthropic" }

func (s *scripted) Complete(_ context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.answers[req.Task]
	if len(q) == 0 {
		return domain.Completion{}, errors.New("scripted: no answer left for " + string(req.Task))
	}
	s.answers[req.Task] = q[1:]
	if q[0].err != nil {
		return domain.Completion{}, q[0].err
	}
	return domain.Completion{Provider: "anthropic", Model: req.Model, Text: q[0].text, Stop: domain.StopEnd,
		Usage: domain.Usage{InputTokens: 900, OutputTokens: 60}}, nil
}

// counting counts the calls that reach the provider.
type counting struct {
	next  domain.Provider
	calls *atomic.Int32
}

func (c counting) Name() string { return c.next.Name() }

func (c counting) Complete(ctx context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	c.calls.Add(1)
	return c.next.Complete(ctx, req)
}

// refusing fails every call: tests that must not reach a provider.
type refusing struct{}

func (refusing) Name() string { return "anthropic" }

func (refusing) Complete(context.Context, domain.CompletionRequest) (domain.Completion, error) {
	return domain.Completion{}, errors.New("this test must not call a provider")
}

// factory hands every configured provider the test's cassette.
type factory struct {
	provider domain.Provider
	calls    atomic.Int32
	keys     []string
	mu       sync.Mutex
}

func (f *factory) Provider(cfg domain.ProviderConfig, key string) (domain.Provider, error) {
	f.mu.Lock()
	f.keys = append(f.keys, key)
	f.mu.Unlock()
	return counting{next: f.provider, calls: &f.calls}, nil
}

// environments is Release's eligibility policies, faked per test.
type environments map[string]bool

func (e environments) ShipsApproved(_ context.Context, _ uuid.UUID, env string) (bool, error) {
	return e[env], nil
}

type wiring struct {
	catalog      *catalogapp.Service
	localization *localizationapp.Service
	knowledge    *knowledgeapp.Service
	svc          *app.Service
	worker       *app.Worker
	dispatcher   *outbox.Dispatcher
	tenant       tenancy.ID
	factory      *factory
	registry     *prometheus.Registry
	envs         environments
}

// newWiring resets the database and wires every context; answers (nil:
// no provider call allowed) script the cassette.
func newWiring(t *testing.T, answers map[domain.Task][]answer) *wiring {
	t.Helper()
	ctx := context.Background()
	if err := wenv.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	tenant, err := wenv.SeedTenant(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(wenv.App)
	cat := catalogapp.New(catalogpg.NewTransactor(uow))
	loc := localizationapp.New(localizationpg.NewTransactor(uow), catalogport.New(cat),
		localizationapp.WithReviewers(localizationidentity.NewReviewers(identitypg.NewTransactor(uow, nil))))
	cat.SetCoverage(coverage.New(loc))
	cat.SetProjection(projection.New(loc))
	know := knowledgeapp.New(knowledgepg.NewTransactor(uow), knowledgesources.NewTranslations(loc, cat), knowledgesources.NewProjects(cat))
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	sealer, err := sealing.New(key)
	if err != nil {
		t.Fatal(err)
	}
	f := &factory{provider: cassetteFor(t, answers)}
	reg := prometheus.NewRegistry()
	envs := environments{"production": true, "staging": true}
	svc, err := app.NewService(app.Deps{
		Tx: intelligencepg.NewTransactor(uow), Catalog: sources.NewCatalog(cat), Localization: sources.NewLocalization(loc, cat),
		Environments: envs, Knowledge: sources.NewKnowledge(know), Providers: f, Sealer: sealer,
		Metrics: metrics.New(reg),
	})
	if err != nil {
		t.Fatal(err)
	}
	events := outbox.NewRegistry()
	for _, sub := range []interface{ Subscribe(*outbox.Registry) error }{loc, know, svc} {
		if err := sub.Subscribe(events); err != nil {
			t.Fatal(err)
		}
	}
	d, err := outbox.NewDispatcher(outbox.NewPostgresStore(uow), events, outbox.DispatcherConfig{
		BatchSize: 100, MaxAttempts: 3, PollInterval: 10 * time.Millisecond, Lease: time.Minute,
		HandlerTimeout: 30 * time.Second, InlineAttempts: 1, InlineBackoff: time.Millisecond,
	}, outbox.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	w := app.NewWorker(svc, intelligencepg.NewClaimer(uow), app.WorkerConfig{Workers: 1, JobTimeout: time.Minute})
	return &wiring{catalog: cat, localization: loc, knowledge: know, svc: svc, worker: w, dispatcher: d, tenant: tenant,
		factory: f, registry: reg, envs: envs}
}

// cassetteFor replays testdata/wiring/<test>.json, or records it from
// answers with -record-wiring. Replays must use every recording.
func cassetteFor(t *testing.T, answers map[domain.Task][]answer) domain.Provider {
	t.Helper()
	if answers == nil {
		return refusing{}
	}
	path := filepath.Join("testdata", "wiring", strings.ReplaceAll(t.Name(), "/", "_")+".json")
	if *recordWiring {
		rec := cassette.NewRecorder("scripted")
		t.Cleanup(func() {
			if err := rec.Cassette().Save(path); err != nil {
				t.Error(err)
			}
		})
		return rec.Wrap(&scripted{answers: answers})
	}
	cas, err := cassette.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if unused := cas.Unused(); len(unused) != 0 && !t.Failed() {
			t.Errorf("%d recorded interactions were not replayed; re-record with -record-wiring", len(unused))
		}
	})
	return cas.Provider("anthropic")
}

func (w *wiring) as(roles []string, locales ...string) context.Context {
	return authztest.Member(context.Background(), w.tenant, roles, locales...)
}

func (w *wiring) admin() context.Context     { return w.as([]string{"admin"}) }
func (w *wiring) developer() context.Context { return w.as([]string{"developer"}) }
func (w *wiring) reviewer() context.Context  { return w.as([]string{"reviewer"}) }

// configure sets up an Anthropic provider (the default routing's), the
// consent and a monthly budget.
func (w *wiring) configure(t *testing.T, consent bool, budget domain.MicroUSD) {
	t.Helper()
	ctx := w.admin()
	if _, _, err := w.svc.CreateProvider(ctx, app.ProviderInput{Name: "anthropic", Kind: "anthropic", APIKey: "sk-test-key"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.PutSettings(ctx, app.SettingsInput{ProviderConsent: &consent, MonthlyBudget: &budget}, nil); err != nil {
		t.Fatal(err)
	}
}

// project creates a project (source en, MF1, no required review) with
// locales and messages and delivers the events.
func (w *wiring) project(t *testing.T, locales []string, messages map[string]string) uuid.UUID {
	t.Helper()
	ctx := w.developer()
	settings := catalogdomain.Settings{DefaultSyntax: mfcontent.MF1}
	p, _, err := w.catalog.CreateProject(ctx, catalogapp.NewProject{Slug: "shop", Name: "Shop", SourceLocale: "en", Settings: &settings}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range locales {
		if _, _, err := w.localization.AddLocale(ctx, p.ID.UUID(), l); err != nil {
			t.Fatal(err)
		}
	}
	w.drain(t)
	w.push(t, p.ID.UUID(), messages)
	return p.ID.UUID()
}

func (w *wiring) push(t *testing.T, project uuid.UUID, messages map[string]string) {
	t.Helper()
	var items []catalogapp.UpsertItem
	for k, text := range messages {
		items = append(items, catalogapp.UpsertItem{Key: k, Text: text})
	}
	if len(items) == 0 {
		return
	}
	res, err := w.catalog.UpsertMessages(w.developer(), catalogdomain.ProjectID(project), items)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Error != nil {
			t.Fatalf("push %s: %+v", r.Key, r.Error)
		}
	}
	w.drain(t)
}

// drain delivers every pending outbox event.
func (w *wiring) drain(t *testing.T) {
	t.Helper()
	for range 50 {
		n, err := w.dispatcher.ProcessBatch(context.Background())
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if n == 0 {
			if dead := wcount(t, "SELECT count(*) FROM outbox_events WHERE status = 'dead'"); dead > 0 {
				var last string
				_ = wenv.Super.QueryRow(context.Background(), "SELECT coalesce(last_error, '') FROM outbox_events WHERE status = 'dead' LIMIT 1").Scan(&last)
				t.Fatalf("%d events dead-lettered: %s", dead, last)
			}
			return
		}
	}
	t.Fatal("outbox did not drain")
}

// work runs due jobs until none is due, then delivers their events.
func (w *wiring) work(t *testing.T) int {
	t.Helper()
	ran := 0
	for range 100 {
		worked, err := w.worker.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("worker: %v", err)
		}
		if !worked {
			w.drain(t)
			return ran
		}
		ran++
	}
	t.Fatal("the queue did not empty")
	return ran
}

func (w *wiring) jobs(t *testing.T) []domain.Job {
	t.Helper()
	jobs, _, err := w.svc.ListJobs(w.developer(), app.JobFilter{}, firstPageW())
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

func wcount(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := wenv.Super.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}
