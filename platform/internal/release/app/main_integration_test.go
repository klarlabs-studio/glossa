//go:build integration

package app_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	identitypg "go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	localizationidentity "go.klarlabs.de/glossa/platform/internal/localization/adapters/identity"
	"maps"
	"os"
	"slices"
	"sync"
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
	"go.klarlabs.de/glossa/platform/internal/kernel/objectstore"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	catalogport "go.klarlabs.de/glossa/platform/internal/localization/adapters/catalog"
	localizationpg "go.klarlabs.de/glossa/platform/internal/localization/adapters/postgres"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/release/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/release/adapters/sources"
	"go.klarlabs.de/glossa/platform/internal/release/app"
	"go.klarlabs.de/glossa/platform/internal/release/domain"
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

// harness wires Catalog, Localization and Release the way the
// composition root does, on a memory object store, plus an outbox
// dispatcher the test drives by hand.
type harness struct {
	catalog      *catalogapp.Service
	localization *localizationapp.Service
	svc          *app.Service
	objects      *objectstore.Memory
	signingKey   ed25519.PublicKey
	dispatcher   *outbox.Dispatcher
	tenant       tenancy.ID
	// clock is the service's clock: real time, moved on by Advance.
	clock *offsetClock
	// scanner finds background work across tenants (system scope).
	scanner *postgres.Scanner
}

// offsetClock is real time plus an offset tests move forward.
type offsetClock struct {
	mu     sync.Mutex
	offset time.Duration
}

func (c *offsetClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().UTC().Add(c.offset)
}

func (c *offsetClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.offset += d
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
	cat.SetCoverage(coverage.New(loc))
	cat.SetProjection(projection.New(loc))
	key, err := domain.ParseSigningKey("test-2026", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	signer, err := domain.NewSigner([]domain.SigningKey{key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	objects := objectstore.NewMemory()
	clock := &offsetClock{}
	svc := app.New(postgres.NewTransactor(uow), sources.New(cat, loc), objects, signer, app.WithClock(clock.Now))
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
	return &harness{
		catalog: cat, localization: loc, svc: svc, objects: objects, dispatcher: d, tenant: tenant,
		signingKey: key.Key.Public().(ed25519.PublicKey), clock: clock, scanner: postgres.NewScanner(uow),
	}
}

// drain delivers every pending event.
func (h *harness) drain(t *testing.T) {
	t.Helper()
	for range 20 {
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

func (h *harness) as(roles ...string) context.Context {
	return authztest.Member(context.Background(), h.tenant, roles)
}

func (h *harness) owner() context.Context { return h.as("owner") }

// project creates a project (source en, review required) with locales
// and messages pushed in MF1, and delivers the resulting events.
func (h *harness) project(t *testing.T, locales []string, messages map[string]string) uuid.UUID {
	t.Helper()
	ctx := h.owner()
	settings := catalogdomain.Settings{DefaultSyntax: mfcontent.MF1, ReviewRequired: true}
	p, _, err := h.catalog.CreateProject(ctx, catalogapp.NewProject{
		Slug: "brotwerk", Name: "Brotwerk", SourceLocale: "en", Settings: &settings,
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
	for _, k := range slices.Sorted(maps.Keys(messages)) {
		items = append(items, catalogapp.UpsertItem{Key: k, Text: messages[k]})
	}
	res, err := h.catalog.UpsertMessages(h.owner(), catalogdomain.ProjectID(project), items)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Error != nil {
			t.Fatalf("push %s: %+v", r.Key, r.Error)
		}
	}
	h.drain(t)
}

// translate writes text in state (as an owner, who may review).
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

func count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := env.Super.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func ptr[T any](v T) *T { return &v }
