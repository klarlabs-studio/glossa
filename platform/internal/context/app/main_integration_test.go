//go:build integration

package app_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	catalogpg "go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres"
	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	catalogport "go.klarlabs.de/glossa/platform/internal/context/adapters/catalog"
	contextpg "go.klarlabs.de/glossa/platform/internal/context/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
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

// clock is a settable time source.
type clock struct{ t time.Time }

func newClock() *clock                   { return &clock{t: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)} }
func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func (c *clock) set(t time.Time)         { c.t = t }

// harness wires Catalog and Context the way the composition root does,
// plus an outbox dispatcher the test drives.
type harness struct {
	catalog    *catalogapp.Service
	svc        *app.Service
	dispatcher *outbox.Dispatcher
	tenant     tenancy.ID
	clock      *clock
}

func newHarness(t *testing.T, opts ...app.Option) *harness {
	t.Helper()
	if err := env.Reset(context.Background()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	return harnessFor(t, "acme", opts...)
}

// harnessFor adds a tenant to the current database state.
func harnessFor(t *testing.T, slug string, opts ...app.Option) *harness {
	t.Helper()
	tenant, err := env.SeedTenant(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	clk := newClock()
	// Catalog shares the clock, so a test can close a branch at a chosen
	// moment and watch retention's grace period run out.
	cat := catalogapp.New(catalogpg.NewTransactor(uow), catalogapp.WithClock(clk.now))
	opts = append([]app.Option{app.WithClock(clk.now), app.WithSweeper(contextpg.NewSweeper(uow))}, opts...)
	svc := app.New(contextpg.NewTransactor(uow), catalogport.New(cat), opts...)
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
	return &harness{catalog: cat, svc: svc, dispatcher: d, tenant: tenant, clock: clk}
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

func (h *harness) developer() context.Context {
	return authztest.Member(context.Background(), h.tenant, []string{"developer"})
}

func (h *harness) translator() context.Context {
	return authztest.Member(context.Background(), h.tenant, []string{"translator"}, "de")
}

// ci is a CI token with the write scope.
func (h *harness) ci() context.Context {
	return authztest.Token(context.Background(), h.tenant, "write")
}

// fixture is a project with applications and messages.
type fixture struct {
	project uuid.UUID
	apps    map[string]uuid.UUID
	ids     map[string]uuid.UUID
}

// project creates a project with applications (by slug) and messages.
func (h *harness) project(t *testing.T, slug string, apps []string, keys ...string) fixture {
	t.Helper()
	ctx := h.developer()
	settings := catalogdomain.Settings{DefaultSyntax: mfcontent.MF1}
	p, _, err := h.catalog.CreateProject(ctx, catalogapp.NewProject{Slug: slug, Name: slug, SourceLocale: "en", Settings: &settings}, "")
	if err != nil {
		t.Fatal(err)
	}
	f := fixture{project: p.ID.UUID(), apps: map[string]uuid.UUID{}, ids: map[string]uuid.UUID{}}
	for _, a := range apps {
		created, _, err := h.catalog.CreateApplication(ctx, p.ID, catalogapp.NewApplication{Slug: a, Name: a, Platform: "web"}, "")
		if err != nil {
			t.Fatal(err)
		}
		f.apps[a] = created.ID.UUID()
	}
	var items []catalogapp.UpsertItem
	for _, k := range keys {
		items = append(items, catalogapp.UpsertItem{Key: k, Text: "Text of " + k})
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
		found, err := h.catalog.MessagesByKeys(ctx, p.ID, keys)
		if err != nil {
			t.Fatal(err)
		}
		for k, m := range found {
			f.ids[k] = m.ID.UUID()
		}
	}
	h.drain(t)
	return f
}

// use is one usage line of a document: key@file:line.
type use struct {
	key, file string
	line      int
}

// sha is a full commit ID from a short one: documents carry full IDs.
func sha(short string) string { return short + strings.Repeat("0", 40-len(short)) }

// document writes a glossa.usages/v1 document; commit is abbreviated.
// Every usage is in PaymentFooter on /checkout.
func document(application, commit, branch string, uses ...use) []byte {
	placed := make([]placedUse, len(uses))
	for i, u := range uses {
		placed[i] = placedUse{use: u, component: "PaymentFooter", route: "/checkout"}
	}
	return documentOf(application, commit, branch, placed...)
}

// placedUse is a usage with its component and route ("" for none).
type placedUse struct {
	use
	component, route string
}

func documentOf(application, commit, branch string, uses ...placedUse) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, `{"schema":"glossa.usages/v1","application":%q,"commit":%q,"branch":%q,`+
		`"tool":{"name":"@klarlabs-studio/glossa","version":"0.1.0"},"usages":[`, application, sha(commit), branch)
	for i, u := range uses {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"key":%q,"file":%q,"line":%d,"column":3,"kind":"t"`, u.key, u.file, u.line)
		if u.component != "" {
			fmt.Fprintf(&b, `,"component":%q`, u.component)
		}
		if u.route != "" {
			fmt.Fprintf(&b, `,"route":%q`, u.route)
		}
		b.WriteByte('}')
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

// ingest uploads a plugin build as CI (default branch main) and moves
// the clock on, so builds are ordered.
func (h *harness) ingest(t *testing.T, f fixture, source, application, commit, branch string, uses ...use) app.Ingested {
	t.Helper()
	out, err := h.svc.IngestUsages(h.ci(), app.IngestUsages{
		Project: f.project, Source: source, Document: document(application, commit, branch, uses...),
	})
	if err != nil {
		t.Fatalf("ingest %s@%s: %v", application, commit, err)
	}
	h.clock.advance(time.Minute)
	return out
}

func count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := env.Super.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func digest(s string) domain.Digest { return domain.DigestOf([]byte(s)) }
