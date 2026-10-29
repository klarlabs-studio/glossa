//go:build integration

package snapshot_test

import (
	"context"
	"fmt"
	identitypg "go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres"
	localizationidentity "go.klarlabs.de/glossa/platform/internal/localization/adapters/identity"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/coverage"
	catalogpg "go.klarlabs.de/glossa/platform/internal/catalog/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/catalog/adapters/projection"
	catalogapp "go.klarlabs.de/glossa/platform/internal/catalog/app"
	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	catalogport "go.klarlabs.de/glossa/platform/internal/localization/adapters/catalog"
	localizationpg "go.klarlabs.de/glossa/platform/internal/localization/adapters/postgres"
	localizationapp "go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/quality/adapters/snapshot"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
	"go.klarlabs.de/glossa/platform/internal/quality/layers"
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

// world is Catalog and Localization wired as the composition root wires
// them, with the outbox driven by hand so the projection is current.
type world struct {
	catalog      *catalogapp.Service
	localization *localizationapp.Service
	dispatcher   *outbox.Dispatcher
	ctx          context.Context
}

func newWorld(t *testing.T) *world {
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
	cat.SetLocales(coverage.NewPolicyLocales(loc))
	cat.SetProjection(projection.New(loc))
	reg := outbox.NewRegistry()
	if err := loc.Subscribe(reg); err != nil {
		t.Fatal(err)
	}
	d, err := outbox.NewDispatcher(outbox.NewPostgresStore(uow), reg, outbox.DispatcherConfig{
		BatchSize: 100, MaxAttempts: 3, PollInterval: 10 * time.Millisecond, Lease: time.Minute,
		HandlerTimeout: 10 * time.Second, InlineAttempts: 1, InlineBackoff: time.Millisecond,
	}, outbox.DispatcherOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return &world{catalog: cat, localization: loc, dispatcher: d,
		ctx: authztest.Member(context.Background(), tenant, []string{"developer"})}
}

func (w *world) drain(t *testing.T) {
	t.Helper()
	for range 50 {
		n, err := w.dispatcher.ProcessBatch(context.Background())
		if err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("outbox did not drain")
}

// project is a German-source project with French and Japanese, keys
// pushed and translated in both, then obsolete where asked.
func (w *world) project(t *testing.T, keys []string, obsolete ...string) uuid.UUID {
	t.Helper()
	settings := catalogdomain.Settings{DefaultSyntax: mfcontent.MF1}
	p, _, err := w.catalog.CreateProject(w.ctx, catalogapp.NewProject{
		Slug: "brotwerk", Name: "Brotwerk", SourceLocale: "de", Settings: &settings,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{"fr", "ja"} {
		if _, _, err := w.localization.AddLocale(w.ctx, p.ID.UUID(), l); err != nil {
			t.Fatal(err)
		}
	}
	var items []catalogapp.UpsertItem
	for _, k := range keys {
		items = append(items, catalogapp.UpsertItem{Key: k, Text: "Text " + k})
	}
	if _, err := w.catalog.UpsertMessages(w.ctx, p.ID, items); err != nil {
		t.Fatal(err)
	}
	w.drain(t)
	for _, k := range keys {
		for _, l := range []string{"fr", "ja"} {
			if _, _, err := w.localization.PutTranslation(w.ctx, p.ID.UUID(), k, l,
				localizationapp.TranslationInput{Text: l + " " + k}, nil); err != nil {
				t.Fatalf("translate %s/%s: %v", k, l, err)
			}
		}
	}
	for _, k := range obsolete {
		if _, err := w.catalog.ObsoleteMessage(w.ctx, p.ID, k, nil); err != nil {
			t.Fatal(err)
		}
	}
	w.drain(t)
	return p.ID.UUID()
}

// The server's snapshot carries the translations of an obsolete message
// apart from the catalog's, and the completeness layer reports each by
// the obsolete message's ID — the fingerprint `glossa check` computes.
func TestSnapshotCarriesTheTranslationsOfObsoleteMessages(t *testing.T) {
	w := newWorld(t)
	p := w.project(t, []string{"checkout.pay", "help.legacy.title"}, "help.legacy.title")
	snap, err := snapshot.New(w.catalog, w.localization).Snapshot(w.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := w.catalog.GetMessage(w.ctx, catalogdomain.ProjectID(p), "help.legacy.title")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.UUID(legacy.ID).String()
	if got := snap.Project.Orphans; len(got) != 2 || got[0].MessageID != id || got[0].Key != "help.legacy.title" ||
		got[0].Locale != "fr" || got[1].Locale != "ja" || got[0].Revision == "" || snap.Project.MoreOrphans {
		t.Fatalf("orphans = %+v (more %v)", got, snap.Project.MoreOrphans)
	}
	for _, trs := range snap.Project.Translations {
		if _, ok := trs["help.legacy.title"]; ok {
			t.Error("an orphan landed among the catalog's translations")
		}
	}
	var found []domain.Finding
	for _, f := range (layers.Completeness{}).Check(snap.Project, checkpolicy.Policy{}) {
		if f.Code == checkpolicy.CodeUnknownKey {
			found = append(found, f)
		}
	}
	want := domain.Fingerprint(domain.LayerCompleteness, checkpolicy.CodeUnknownKey, domain.Locus{Message: id, Locale: "fr"}, "")
	if len(found) != 2 || found[0].Fingerprint != want || found[0].Locus.Message != id {
		t.Errorf("unknown-key findings = %+v, want two, the first %s", found, want)
	}
}

// A project that obsoleted more than layers.MaxOrphans translations is
// read one page per listing, and the snapshot says there were more.
func TestSnapshotReadsOnePageOfOrphans(t *testing.T) {
	w := newWorld(t)
	var keys []string
	for i := range layers.MaxOrphans/2 + 1 { // two locales each: one over the bound
		keys = append(keys, fmt.Sprintf("old.%03d", i))
	}
	p := w.project(t, append([]string{"checkout.pay"}, keys...), keys...)
	snap, err := snapshot.New(w.catalog, w.localization).Snapshot(w.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Project.Orphans) != layers.MaxOrphans || !snap.Project.MoreOrphans {
		t.Errorf("%d orphans read (more %v), want one page of %d and more", len(snap.Project.Orphans),
			snap.Project.MoreOrphans, layers.MaxOrphans)
	}
	if len(snap.Project.Translations["fr"]) != 1 {
		t.Errorf("French translations = %d, want checkout.pay's alone", len(snap.Project.Translations["fr"]))
	}
}
