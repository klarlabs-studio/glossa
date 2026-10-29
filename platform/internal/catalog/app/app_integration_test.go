//go:build integration

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/catalog/app"
	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

func TestCreateProjectIsIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := h.developer()
	in := app.NewProject{Slug: "brotwerk", Name: "Brotwerk", SourceLocale: "DE_de"}
	p, replayed, err := h.svc.CreateProject(ctx, in, "key-1")
	if err != nil || replayed {
		t.Fatalf("create: %v replayed=%v", err, replayed)
	}
	if p.SourceLocale.String() != "de-DE" || p.Settings.DefaultSyntax != mfcontent.MF1 {
		t.Errorf("project = %+v", p)
	}
	again, replayed, err := h.svc.CreateProject(ctx, in, "key-1")
	if err != nil || !replayed || again.ID != p.ID {
		t.Errorf("replay: %v replayed=%v id=%s", err, replayed, again.ID)
	}
	if _, _, err := h.svc.CreateProject(ctx, app.NewProject{Slug: "other", Name: "X", SourceLocale: "de"}, "key-1"); !errors.Is(err, app.ErrIdempotencyReuse) {
		t.Errorf("reused key: %v", err)
	}
	if _, _, err := h.svc.CreateProject(ctx, in, ""); !errors.Is(err, app.ErrSlugTaken) {
		t.Errorf("slug clash: %v", err)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'catalog.project.created'"); n != 1 {
		t.Errorf("project.created events = %d", n)
	}
}

func TestProjectUpdateNeedsCurrentVersion(t *testing.T) {
	h := newHarness(t)
	ctx := h.developer()
	p := h.project(t, ctx)
	name := "Brotwerk Web"
	if _, err := h.svc.UpdateProject(ctx, p.ID, p.Version+1, domain.ProjectChange{Name: &name}); !errors.Is(err, app.ErrPreconditionFailed) {
		t.Errorf("stale If-Match: %v", err)
	}
	up, err := h.svc.UpdateProject(ctx, p.ID, p.Version, domain.ProjectChange{Name: &name})
	if err != nil || up.Version != 2 || up.Name != name {
		t.Errorf("update: %v %+v", err, up)
	}
	// Deleting a project destroys history: developers can't.
	if err := h.svc.DeleteProject(ctx, p.ID, nil); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("developer delete: %v", err)
	}
	admin := authztest.Member(context.Background(), h.tenant, []string{"admin"})
	if err := h.svc.DeleteProject(admin, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.GetProject(ctx, p.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("get deleted: %v", err)
	}
}

// fakeLocales is Localization's answer, without Localization.
type fakeLocales []string

func (f fakeLocales) LocaleCodes(context.Context, domain.ProjectID) ([]string, error) { return f, nil }

// TestCheckPolicyRoundTripsThroughStorage: the setting has to survive
// the JSONB column with the one distinction that matters — no locales
// required is not the same as every locale required.
func TestCheckPolicyRoundTripsThroughStorage(t *testing.T) {
	h := newHarness(t)
	h.svc.SetLocales(fakeLocales{"en", "de"})
	ctx := h.developer()
	p := h.project(t, ctx)
	if p.Settings.CheckPolicy != nil {
		t.Fatalf("a new project stores a policy: %+v", p.Settings.CheckPolicy)
	}

	tests := []struct {
		name   string
		policy checkpolicy.Policy
	}{
		{name: "a named list", policy: checkpolicy.Policy{
			RequireComplete: []string{"de"}, FailOn: checkpolicy.Warning, MissingTranslations: checkpolicy.Warning}},
		{name: "no locale", policy: checkpolicy.Policy{
			RequireComplete: []string{}, FailOn: checkpolicy.Error, MissingTranslations: checkpolicy.Error}},
		{name: "every locale, nothing fails", policy: checkpolicy.Policy{
			FailOn: checkpolicy.Never, MissingTranslations: checkpolicy.Error}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cur, err := h.svc.GetProject(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			settings := cur.Settings
			policy := tc.policy
			settings.CheckPolicy = &policy
			if _, err := h.svc.UpdateProject(ctx, p.ID, cur.Version, domain.ProjectChange{Settings: &settings}); err != nil {
				t.Fatal(err)
			}
			back, err := h.svc.GetProject(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			got := back.Settings.Policy()
			if !got.Equal(tc.policy) || (got.RequireComplete == nil) != (tc.policy.RequireComplete == nil) {
				t.Fatalf("stored policy = %+v, want %+v", got, tc.policy)
			}
		})
	}

	// A locale the project does not have is refused, by the project's
	// own locales rather than by anything Catalog keeps.
	cur, err := h.svc.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings := cur.Settings
	settings.CheckPolicy = &checkpolicy.Policy{RequireComplete: []string{"ja"}}
	if _, err := h.svc.UpdateProject(ctx, p.ID, cur.Version, domain.ProjectChange{Settings: &settings}); !errors.Is(err, checkpolicy.ErrUnknownLocale) {
		t.Errorf("unknown locale: %v, want ErrUnknownLocale", err)
	}
}

// TestCheckPolicyDocumentRoundTripsThroughStorage: the document of
// RFC 0005 §4.1 — rules, environments, a version and a grace — has to
// survive the JSONB column and migration 0029's constraint whole,
// because the policy is what two readers grade a commit by.
func TestCheckPolicyDocumentRoundTripsThroughStorage(t *testing.T) {
	h := newHarness(t)
	h.svc.SetLocales(fakeLocales{"en", "de", "fr"})
	ctx := h.developer()
	p := h.project(t, ctx)

	saved := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	document := checkpolicy.Policy{
		Schema: checkpolicy.Schema, RequireComplete: []string{"de"}, FailOn: checkpolicy.Error,
		MissingTranslations: checkpolicy.Warning,
		Environments: map[string]checkpolicy.Environment{
			"production": {
				RequireComplete: checkpolicy.RequiredLocales("de", "fr"),
				RequireReview:   checkpolicy.ReviewApproved,
			},
			"staging": {RequireComplete: checkpolicy.AllLocales()},
		},
		Rules: []checkpolicy.Rule{
			{Selector: checkpolicy.Selector{Layer: "source"}, Severity: checkpolicy.Off},
			{
				Selector: checkpolicy.Selector{Layer: "terminology", Namespace: "legal"},
				Severity: checkpolicy.Error,
			},
			{
				Selector: checkpolicy.Selector{Layer: "visual"},
				Severity: checkpolicy.Warning, Mode: checkpolicy.ModeWarn,
			},
		},
	}.Supersede(checkpolicy.Policy{Version: 6}, saved, checkpolicy.DefaultGrace)

	cur, err := h.svc.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings := cur.Settings
	settings.CheckPolicy = &document
	if _, err := h.svc.UpdateProject(ctx, p.ID, cur.Version,
		domain.ProjectChange{Settings: &settings}); err != nil {
		t.Fatal(err)
	}
	back, err := h.svc.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// What comes back is the document as validation normalized it — a
	// rule that named no mode enforces — and nothing else moved.
	want, err := document.Validate([]string{"en", "de", "fr"})
	if err != nil {
		t.Fatal(err)
	}
	got := back.Settings.Policy()
	if !got.Equal(want) {
		t.Fatalf("stored document = %+v, want %+v", got, want)
	}
	// And it still decides: the rules, the environments and the grace
	// came back, not just the bytes.
	if !got.FailsDecision(got.Decide(checkpolicy.Target{
		Layer: "terminology", Namespace: "legal", Locale: "de", Severity: checkpolicy.Warning,
	})) {
		t.Error("the stored rule stopped deciding")
	}
	if got.Computes("source", "") {
		t.Error("the source layer computes, want the stored rule to switch it off")
	}
	if !got.RequiresIn("production", "fr") || got.RequiresIn("", "fr") {
		t.Error("the stored environment's require_complete did not survive")
	}
	if !got.Pins(saved.Add(-time.Hour), saved.Add(time.Hour)) || got.Previous.Version != 6 {
		t.Errorf("the grace or the previous version did not survive: %+v", got.Previous)
	}

	// A rule raising the advisory layer to error is refused by the
	// domain before storage ever sees it (RFC 0005 §14 decision 10).
	cur, err = h.svc.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings = cur.Settings
	settings.CheckPolicy = &checkpolicy.Policy{Rules: []checkpolicy.Rule{
		{Selector: checkpolicy.Selector{Layer: "linguistic"}, Severity: checkpolicy.Error},
	}}
	if _, err := h.svc.UpdateProject(ctx, p.ID, cur.Version,
		domain.ProjectChange{Settings: &settings}); !errors.Is(err, checkpolicy.ErrAdvisoryLayer) {
		t.Errorf("raising the linguistic layer: %v, want ErrAdvisoryLayer", err)
	}
}

func TestApplications(t *testing.T) {
	h := newHarness(t)
	ctx := h.developer()
	p := h.project(t, ctx)
	a, _, err := h.svc.CreateApplication(ctx, p.ID, app.NewApplication{Slug: "web", Name: "Shop", Platform: "web"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.svc.CreateApplication(ctx, p.ID, app.NewApplication{Slug: "web", Name: "Again", Platform: "api"}, ""); !errors.Is(err, app.ErrSlugTaken) {
		t.Errorf("slug clash: %v", err)
	}
	if _, _, err := h.svc.CreateApplication(ctx, domain.NewProjectID(), app.NewApplication{Slug: "x", Name: "X", Platform: "web"}, ""); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unknown project: %v", err)
	}
	platform := domain.PlatformAPI
	if a, err = h.svc.UpdateApplication(ctx, p.ID, a.ID, 1, domain.ApplicationChange{Platform: &platform}); err != nil || a.Version != 2 {
		t.Fatalf("update: %v", err)
	}
	items, _, err := h.svc.ListApplications(ctx, p.ID, firstPage())
	if err != nil || len(items) != 1 || items[0].Platform != domain.PlatformAPI {
		t.Errorf("list: %v %+v", err, items)
	}
	if err := h.svc.DeleteApplication(ctx, p.ID, a.ID, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMessageLifecycleKeepsIdentityAndHistory(t *testing.T) {
	h := newHarness(t)
	ctx := h.developer()
	p := h.project(t, ctx)

	m, _, err := h.svc.CreateMessage(ctx, p.ID, app.NewMessage{
		Key: "cart.items", Text: "{count, plural, one {# item} other {# items}}", Description: "Cart badge",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Source.Syntax != mfcontent.MF1 || len(m.Source.Arguments) != 1 || m.Revision != 1 {
		t.Errorf("created %+v", m)
	}

	// A no-op revision in the other syntax.
	same, err := h.svc.ReviseSource(ctx, p.ID, "cart.items", m.Version,
		".input {$count :number}\n.match $count\none {{{$count} item}}\n* {{{$count} items}}", "mf2")
	if err != nil {
		t.Fatal(err)
	}
	if same.Revision != 1 || same.Version != m.Version {
		t.Errorf("the same message in MF2 made revision %d", same.Revision)
	}

	revised, err := h.svc.ReviseSource(ctx, p.ID, "cart.items", same.Version, "{count, plural, one {# product} other {# products}}", "")
	if err != nil {
		t.Fatal(err)
	}
	if revised.ID != m.ID || revised.Revision != same.Revision+1 {
		t.Errorf("revise changed identity or skipped: %+v", revised)
	}
	if _, err := h.svc.ReviseSource(ctx, p.ID, "cart.items", m.Version, "x", ""); !errors.Is(err, app.ErrPreconditionFailed) {
		t.Errorf("stale revise: %v", err)
	}
	var invalid *mfcontent.InvalidError
	if _, err := h.svc.ReviseSource(ctx, p.ID, "cart.items", revised.Version, "{count, plural,", ""); !errors.As(err, &invalid) {
		t.Errorf("invalid source: %v", err)
	}

	renamed, err := h.svc.RenameMessage(ctx, p.ID, "cart.items", "cart.badge", nil)
	if err != nil || renamed.ID != m.ID || renamed.Key != "cart.badge" {
		t.Fatalf("rename: %v %+v", err, renamed)
	}
	if _, err := h.svc.GetMessage(ctx, p.ID, "cart.items"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("old key still resolves: %v", err)
	}
	revs, _, err := h.svc.SourceRevisions(ctx, p.ID, "cart.badge", firstPage())
	if err != nil || len(revs) != revised.Revision || revs[0].Number != revised.Revision || revs[len(revs)-1].Number != 1 {
		t.Errorf("history after rename: %v %d revisions", err, len(revs))
	}

	obsolete, err := h.svc.ObsoleteMessage(ctx, p.ID, "cart.badge", nil)
	if err != nil || obsolete.State != domain.MessageObsolete {
		t.Fatalf("obsolete: %v", err)
	}
	for _, typ := range []string{"created", "source_revised", "renamed", "obsoleted"} {
		if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = $1 AND aggregate_id = $2",
			"catalog.message."+typ, m.ID.String()); n < 1 {
			t.Errorf("no catalog.message.%s event", typ)
		}
	}
	// The source log is append-only for the application role.
	if _, err := env.App.Exec(context.Background(), "UPDATE catalog_source_revisions SET text = 'x'"); err == nil {
		t.Error("glossa_app may update the source log")
	}
}

func TestRenameToTakenKeyFails(t *testing.T) {
	h := newHarness(t)
	ctx := h.developer()
	p := h.project(t, ctx)
	for _, k := range []string{"a", "b"} {
		if _, _, err := h.svc.CreateMessage(ctx, p.ID, app.NewMessage{Key: k, Text: k}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.svc.RenameMessage(ctx, p.ID, "a", "b", nil); !errors.Is(err, app.ErrKeyTaken) {
		t.Errorf("rename onto b: %v", err)
	}
	if _, _, err := h.svc.CreateMessage(ctx, p.ID, app.NewMessage{Key: "a", Text: "again"}, ""); !errors.Is(err, app.ErrKeyTaken) {
		t.Errorf("duplicate key: %v", err)
	}
}

func TestBulkUpsertIsIdempotentAndRevisionAware(t *testing.T) {
	h := newHarness(t)
	ctx := authztest.Token(context.Background(), h.tenant, "write")
	p := h.project(t, h.developer())
	two := 2
	items := []app.UpsertItem{
		{Key: "checkout.pay", Text: "Pay {amount, number, ::currency/EUR}"},
		{Key: "cart.items", Text: "{count, plural, one {# item} other {# items}}"},
		{Key: "home.title", Text: "Welcome", Namespace: ptr("landing")},
	}
	first, err := h.svc.UpsertMessages(ctx, p.ID, items)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range first {
		if r.Status != app.UpsertCreated {
			t.Errorf("%s: %s %+v", r.Key, r.Status, r.Error)
		}
	}
	events := count(t, "SELECT count(*) FROM outbox_events")
	again, err := h.svc.UpsertMessages(ctx, p.ID, items)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range again {
		if r.Status != app.UpsertUnchanged {
			t.Errorf("second push %s: %s", r.Key, r.Status)
		}
	}
	if n := count(t, "SELECT count(*) FROM outbox_events"); n != events {
		t.Errorf("a no-op push published %d events", n-events)
	}
	if n := count(t, "SELECT count(*) FROM catalog_source_revisions"); n != 3 {
		t.Errorf("source revisions = %d, want 3", n)
	}

	results, err := h.svc.UpsertMessages(ctx, p.ID, []app.UpsertItem{
		{Key: "checkout.pay", Text: "Pay now", BaseRevision: ptr(1)},           // revised
		{Key: "cart.items", Text: "Changed", BaseRevision: &two},               // stale base
		{Key: "home.title", Text: "Welcome", Description: ptr("Landing hero")}, // details only
		{Key: "new.one", Text: "{broken"},                                      // invalid MF1
		{Key: "Bad Key", Text: "x"},                                            // invalid key
		{Key: "new.two", Text: "Two"},                                          // created
		{Key: "new.two", Text: "Dup"},                                          // duplicate in batch
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		status app.UpsertStatus
		code   string
	}{
		{app.UpsertRevised, ""}, {app.UpsertFailed, "source_revision_conflict"}, {app.UpsertUpdated, ""},
		{app.UpsertFailed, "invalid_message"}, {app.UpsertFailed, "invalid_message_key"},
		{app.UpsertCreated, ""}, {app.UpsertFailed, "duplicate_key"},
	}
	for i, w := range want {
		r := results[i]
		code := ""
		if r.Error != nil {
			code = r.Error.Code
		}
		if r.Status != w.status || code != w.code {
			t.Errorf("item %d (%s): %s %q, want %s %q", i, r.Key, r.Status, code, w.status, w.code)
		}
	}
	if results[0].Message.Revision != 2 {
		t.Errorf("revised to %d", results[0].Message.Revision)
	}

	// Pushing an obsolete message again brings it back.
	if _, err := h.svc.ObsoleteMessage(h.developer(), p.ID, "home.title", nil); err != nil {
		t.Fatal(err)
	}
	back, err := h.svc.UpsertMessages(ctx, p.ID, []app.UpsertItem{{Key: "home.title", Text: "Welcome"}})
	if err != nil || back[0].Status != app.UpsertUpdated || back[0].Message.State != domain.MessageActive {
		t.Errorf("reactivate: %v %+v", err, back)
	}
}

func TestListMessagesFilters(t *testing.T) {
	h := newHarness(t)
	ctx := h.developer()
	p := h.project(t, ctx)
	var items []app.UpsertItem
	for _, k := range []string{"checkout.pay", "checkout.total", "checkout_x.y", "cart.items", "home.title"} {
		items = append(items, app.UpsertItem{Key: k, Text: k})
	}
	items[4].Namespace = ptr("landing")
	if _, err := h.svc.UpsertMessages(ctx, p.ID, items); err != nil {
		t.Fatal(err)
	}
	keys := func(q app.MessageQuery, page pagination.Page) ([]string, *string) {
		t.Helper()
		ms, next, err := h.svc.ListMessages(ctx, p.ID, q, page)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, m := range ms {
			out = append(out, string(m.Key))
		}
		return out, next
	}
	// "_" is a literal in keys, not a LIKE wildcard.
	if got, _ := keys(app.MessageQuery{KeyPrefix: "checkout."}, firstPage()); len(got) != 2 {
		t.Errorf("prefix checkout. = %v", got)
	}
	if got, _ := keys(app.MessageQuery{KeyPrefix: "checkout_"}, firstPage()); len(got) != 1 {
		t.Errorf("prefix checkout_ = %v", got)
	}
	if got, _ := keys(app.MessageQuery{Namespace: "landing"}, firstPage()); len(got) != 1 || got[0] != "home.title" {
		t.Errorf("namespace = %v", got)
	}
	page1, next := keys(app.MessageQuery{}, pagination.Page{Size: 2})
	if len(page1) != 2 || next == nil || page1[0] != "cart.items" {
		t.Fatalf("page 1 = %v", page1)
	}
	page, err := pagination.Parse(ptr(2), next)
	if err != nil {
		t.Fatal(err)
	}
	if page2, _ := keys(app.MessageQuery{}, page); len(page2) != 2 || page2[0] != "checkout.total" {
		t.Errorf("page 2 = %v", page2)
	}
	if _, _, err := h.svc.ListMessages(ctx, p.ID, app.MessageQuery{MissingIn: "de"}, firstPage()); !errors.Is(err, app.ErrNoCoverage) {
		t.Errorf("coverage without Localization: %v", err)
	}
}

func TestListNamespaces(t *testing.T) {
	h := newHarness(t)
	ctx := h.developer()
	p := h.project(t, ctx)
	items := []app.UpsertItem{
		{Key: "a.one", Text: "1"}, {Key: "a.two", Text: "2"},
		{Key: "b.one", Text: "1", Namespace: ptr("checkout")}, {Key: "b.two", Text: "2", Namespace: ptr("checkout")},
		{Key: "c.one", Text: "1", Namespace: ptr("landing")},
	}
	if _, err := h.svc.UpsertMessages(ctx, p.ID, items); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ObsoleteMessage(ctx, p.ID, "b.two", nil); err != nil {
		t.Fatal(err)
	}
	page1, next, err := h.svc.ListNamespaces(ctx, p.ID, pagination.Page{Size: 2})
	if err != nil || next == nil {
		t.Fatalf("page 1: %v %v", err, next)
	}
	want := []app.NamespaceSummary{{Name: "checkout", Active: 1, Obsolete: 1}, {Name: "default", Active: 2}}
	if len(page1) != 2 || page1[0] != want[0] || page1[1] != want[1] {
		t.Errorf("page 1 = %+v, want %+v", page1, want)
	}
	page, err := pagination.Parse(ptr(2), next)
	if err != nil {
		t.Fatal(err)
	}
	page2, next, err := h.svc.ListNamespaces(ctx, p.ID, page)
	if err != nil || next != nil || len(page2) != 1 || page2[0] != (app.NamespaceSummary{Name: "landing", Active: 1}) {
		t.Errorf("page 2 = %+v %v %v", page2, next, err)
	}
	if _, _, err := h.svc.ListNamespaces(ctx, domain.NewProjectID(), firstPage()); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("unknown project: %v", err)
	}
	if _, _, err := h.svc.ListNamespaces(context.Background(), p.ID, firstPage()); err == nil {
		t.Error("listed without a principal")
	}
}

func TestCatalogIsTenantIsolated(t *testing.T) {
	h := newHarness(t)
	ctx := h.developer()
	p := h.project(t, ctx)
	a, _, err := h.svc.CreateApplication(ctx, p.ID, app.NewApplication{Slug: "web", Name: "Web", Platform: "web"}, "")
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := h.svc.CreateMessage(ctx, p.ID, app.NewMessage{Key: "a.b", Text: "Hello"}, "")
	if err != nil {
		t.Fatal(err)
	}

	other, err := env.SeedTenant(context.Background(), "bolt")
	if err != nil {
		t.Fatal(err)
	}
	intruder := authztest.Member(context.Background(), other, []string{"owner"})
	if _, err := h.svc.GetProject(intruder, p.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("project: %v", err)
	}
	if list, _, err := h.svc.ListProjects(intruder, firstPage()); err != nil || len(list) != 0 {
		t.Errorf("projects: %v %d", err, len(list))
	}
	if _, err := h.svc.GetApplication(intruder, p.ID, a.ID); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("application: %v", err)
	}
	if _, err := h.svc.GetMessage(intruder, p.ID, "a.b"); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("message: %v", err)
	}
	if _, err := h.svc.SourceRevisionOf(intruder, p.ID, m.ID, 1); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("source revision: %v", err)
	}
	if _, err := h.svc.ReviseSource(intruder, p.ID, "a.b", 1, "pwned", ""); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("revise: %v", err)
	}
	if _, err := h.svc.UpsertMessages(intruder, p.ID, []app.UpsertItem{{Key: "a.b", Text: "x"}}); !errors.Is(err, app.ErrNotFound) {
		t.Errorf("upsert: %v", err)
	}
	// Writing another tenant's id directly is refused by RLS.
	if _, err := env.App.Exec(context.Background(), `INSERT INTO catalog_projects (id, tenant_id, slug, name, source_locale, version, created_by, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, 'x', 'X', 'en', 1, 'x', now(), now())`, h.tenant.UUID()); err == nil {
		t.Error("an unscoped connection inserted a project")
	}
}

func TestCatalogAuthorization(t *testing.T) {
	h := newHarness(t)
	p := h.project(t, h.developer())
	translator := authztest.Member(context.Background(), h.tenant, []string{"translator"}, "de")
	if _, _, err := h.svc.CreateMessage(translator, p.ID, app.NewMessage{Key: "x", Text: "x"}, ""); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("translator writes catalog: %v", err)
	}
	if _, err := h.svc.UpsertMessages(authztest.Token(context.Background(), h.tenant, "read"), p.ID, []app.UpsertItem{{Key: "x", Text: "x"}}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("read token pushes: %v", err)
	}
	if _, err := h.svc.GetProject(translator, p.ID); err != nil {
		t.Errorf("translator reads catalog: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
