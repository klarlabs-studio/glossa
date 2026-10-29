//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"

	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	contextpg "go.klarlabs.de/glossa/platform/internal/context/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
)

func TestIngestUsagesResolvesKeysAndPublishesTheBuild(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay", "checkout.help")

	got := h.ingest(t, f, "plugin", "web", "9f2c1e7ab4", "main",
		use{"checkout.pay", "src/checkout/PaymentFooter.vue", 42},
		use{"checkout.gone", "src/checkout/Old.vue", 7},
		use{"checkout.pay", "src/checkout/Summary.vue", 12})

	b := got.Build
	if got.Replayed || got.UnknownKeys != 1 || b.UsageCount != 3 || !b.OnDefaultBranch || b.ApplicationID != f.apps["web"] ||
		b.Commit.String() != sha("9f2c1e7ab4") || b.Source != domain.SourcePlugin || b.Tool.Name != "@klarlabs-studio/glossa" {
		t.Errorf("ingested = %+v", got)
	}
	if n := count(t, "SELECT count(*) FROM context_usages WHERE build_id = $1 AND message_id = $2", b.ID, f.ids["checkout.pay"]); n != 2 {
		t.Errorf("resolved usages of checkout.pay = %d, want 2", n)
	}
	if n := count(t, "SELECT count(*) FROM context_usages WHERE build_id = $1 AND message_id IS NULL AND message_key = 'checkout.gone'", b.ID); n != 1 {
		t.Errorf("unknown-key usages = %d, want 1", n)
	}

	var payload domain.BuildIngested
	var raw []byte
	if err := env.Super.QueryRow(context.Background(),
		"SELECT payload FROM outbox_events WHERE event_type = 'context.build.ingested' AND aggregate_id = $1", b.ID.String(),
	).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ProjectID != f.project.String() || payload.ApplicationID != f.apps["web"].String() || payload.Usages != 3 ||
		payload.UnknownKeys != 1 || !payload.OnDefaultBranch || payload.Branch != "main" || payload.Source != "plugin" ||
		payload.By == "" {
		t.Errorf("payload = %+v", payload)
	}
	h.drain(t)
}

func TestIngestUsagesIsIdempotentPerUpload(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	first := h.ingest(t, f, "plugin", "web", "abcdef1", "main", use{"checkout.pay", "a.vue", 1}, use{"nope", "a.vue", 2})
	again := h.ingest(t, f, "plugin", "web", "abcdef1", "main", use{"checkout.pay", "a.vue", 1}, use{"nope", "a.vue", 2})

	if !again.Replayed || again.Build.ID != first.Build.ID || again.UnknownKeys != 1 {
		t.Errorf("second upload = %+v, want a replay of %s", again, first.Build.ID)
	}
	if n := count(t, "SELECT count(*) FROM context_builds"); n != 1 {
		t.Errorf("builds = %d, want 1", n)
	}
	if n := count(t, "SELECT count(*) FROM outbox_events WHERE event_type = 'context.build.ingested'"); n != 1 {
		t.Errorf("events = %d, want 1", n)
	}
	// The same commit from another collector, or another document, is a
	// new build.
	h.ingest(t, f, "extract", "web", "abcdef1", "main", use{"checkout.pay", "a.go", 1})
	h.ingest(t, f, "plugin", "web", "abcdef1", "main", use{"checkout.pay", "a.vue", 9})
	if n := count(t, "SELECT count(*) FROM context_builds"); n != 3 {
		t.Errorf("builds = %d, want 3", n)
	}
}

func TestIngestUsagesStoresLargeBuildsInBatches(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	uses := make([]use, 12_345)
	for i := range uses {
		uses[i] = use{"checkout.pay", fmt.Sprintf("src/f%05d.vue", i), i + 1}
	}
	got := h.ingest(t, f, "plugin", "web", "abcdef1", "main", uses...)
	if n := count(t, "SELECT count(*) FROM context_usages WHERE build_id = $1", got.Build.ID); n != len(uses) {
		t.Errorf("usages stored = %d, want %d", n, len(uses))
	}
	if n := count(t, "SELECT max(position) FROM context_usages WHERE build_id = $1", got.Build.ID); n != len(uses)-1 {
		t.Errorf("last position = %d", n)
	}
}

func TestIngestUsagesRefusals(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	doc := document("web", "abcdef1", "main", use{"checkout.pay", "a.vue", 1})
	cases := map[string]struct {
		ctx  context.Context
		in   app.IngestUsages
		want error
	}{
		"translator": {h.translator(), app.IngestUsages{Project: f.project, Source: "plugin", Document: doc}, authz.ErrForbidden},
		"unknown application": {h.ci(), app.IngestUsages{Project: f.project, Source: "plugin",
			Document: document("ios", "abcdef1", "main")}, app.ErrApplicationNotFound},
		"unknown project":  {h.ci(), app.IngestUsages{Project: uuid.New(), Source: "plugin", Document: doc}, app.ErrProjectNotFound},
		"unknown source":   {h.ci(), app.IngestUsages{Project: f.project, Source: "ci", Document: doc}, domain.ErrInvalidSource},
		"invalid document": {h.ci(), app.IngestUsages{Project: f.project, Source: "plugin", Document: []byte(`{}`)}, domain.ErrInvalidUpload},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := h.svc.IngestUsages(tc.ctx, tc.in); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if n := count(t, "SELECT count(*) FROM context_builds"); n != 0 {
		t.Errorf("builds = %d, want none", n)
	}
}

func files(us []app.UsageView) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = fmt.Sprintf("%s:%d@%s", u.File, u.Line, u.Branch)
	}
	return out
}

func TestCurrentUsagesFollowTheLatestBuildsWithTheBranchFallback(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web", "api"}, "checkout.pay")
	ctx := h.developer()

	h.ingest(t, f, "plugin", "web", "aaaaaaa", "main", use{"checkout.pay", "old.vue", 1})
	h.ingest(t, f, "plugin", "web", "bbbbbbb", "main", use{"checkout.pay", "web.vue", 2})
	h.ingest(t, f, "extract", "web", "bbbbbbb", "main", use{"checkout.pay", "web.go", 3})
	h.ingest(t, f, "extract", "api", "ccccccc", "main", use{"checkout.pay", "api.go", 4})
	// The branch rebuilt the web plugin only.
	h.ingest(t, f, "plugin", "web", "ddddddd", "feat/x", use{"checkout.pay", "branch.vue", 5})

	got, err := h.svc.KeyUsages(ctx, f.project, "checkout.pay", app.UsageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"web.go:3@main", "web.vue:2@main", "api.go:4@main"}
	if f.apps["api"].String() < f.apps["web"].String() {
		want = []string{"api.go:4@main", "web.go:3@main", "web.vue:2@main"}
	}
	if !slices.Equal(files(got), want) {
		t.Errorf("default view = %v, want %v", files(got), want)
	}

	branch, err := h.svc.MessageUsages(ctx, f.project, f.ids["checkout.pay"], app.UsageQuery{Branch: "feat/x"})
	if err != nil {
		t.Fatal(err)
	}
	gotBranch := files(branch)
	slices.Sort(gotBranch)
	if wantBranch := []string{"api.go:4@main", "branch.vue:5@feat/x", "web.go:3@main"}; !slices.Equal(gotBranch, wantBranch) {
		t.Errorf("branch view = %v, want %v", gotBranch, wantBranch)
	}
	// The default branch comes first.
	if branch[len(branch)-1].Branch != "feat/x" {
		t.Errorf("branch view order = %v", files(branch))
	}

	builds, err := h.svc.CurrentBuilds(ctx, f.project, "feat/x")
	if err != nil {
		t.Fatal(err)
	}
	if len(builds) != 3 {
		t.Errorf("current builds of feat/x = %d, want 3", len(builds))
	}

	limited, err := h.svc.KeyUsages(ctx, f.project, "checkout.pay", app.UsageQuery{Limit: 1})
	if err != nil || len(limited) != 1 {
		t.Errorf("limit 1: %d usages, err %v", len(limited), err)
	}
}

func TestUsagesSurviveARename(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	h.ingest(t, f, "plugin", "web", "abcdef1", "main", use{"checkout.pay", "a.vue", 1})
	if _, err := h.catalog.RenameMessage(h.developer(), catalogdomain.ProjectID(f.project), "checkout.pay", "checkout.submit", nil); err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.KeyUsages(h.developer(), f.project, "checkout.submit", app.UsageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Key != "checkout.pay" || *got[0].MessageID != f.ids["checkout.pay"] {
		t.Errorf("usages after rename = %+v", got)
	}
	if _, err := h.svc.KeyUsages(h.developer(), f.project, "checkout.pay", app.UsageQuery{}); !errors.Is(err, app.ErrMessageNotFound) {
		t.Errorf("old key: err = %v, want ErrMessageNotFound", err)
	}
}

func refKeys(ms []app.MessageRef) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Key
	}
	return out
}

func TestUnusedMessages(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "a.used", "b.branch", "c.never", "d.obsolete")
	ctx := h.developer()
	if _, err := h.catalog.ObsoleteMessage(ctx, catalogdomain.ProjectID(f.project), "d.obsolete", nil); err != nil {
		t.Fatal(err)
	}

	none, err := h.svc.UnusedMessages(ctx, f.project, "")
	if err != nil {
		t.Fatal(err)
	}
	if none.CurrentBuilds != 0 || !slices.Equal(refKeys(none.Messages), []string{"a.used", "b.branch", "c.never"}) {
		t.Errorf("before any build = %+v", none)
	}

	h.ingest(t, f, "plugin", "web", "aaaaaaa", "main", use{"a.used", "a.vue", 1}, use{"unknown.key", "a.vue", 2})
	h.ingest(t, f, "plugin", "web", "bbbbbbb", "feat/x", use{"b.branch", "b.vue", 1})

	main, err := h.svc.UnusedMessages(ctx, f.project, "")
	if err != nil {
		t.Fatal(err)
	}
	if main.CurrentBuilds != 1 || !slices.Equal(refKeys(main.Messages), []string{"b.branch", "c.never"}) {
		t.Errorf("default view = %+v", main)
	}
	branch, err := h.svc.UnusedMessages(ctx, f.project, "feat/x")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(refKeys(branch.Messages), []string{"a.used", "c.never"}) {
		t.Errorf("branch view = %v", refKeys(branch.Messages))
	}
}

func TestReadsNeedCatalogReadAndAKnownProject(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	if _, err := h.svc.MessageUsages(context.Background(), f.project, f.ids["checkout.pay"], app.UsageQuery{}); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("no principal: err = %v", err)
	}
	if _, err := h.svc.MessageUsages(h.developer(), uuid.New(), uuid.New(), app.UsageQuery{}); !errors.Is(err, app.ErrProjectNotFound) {
		t.Errorf("unknown project: err = %v", err)
	}
	if _, err := h.svc.UnusedMessages(h.developer(), uuid.New(), ""); !errors.Is(err, app.ErrProjectNotFound) {
		t.Errorf("unused of unknown project: err = %v", err)
	}
	if _, err := h.svc.CurrentBuilds(h.developer(), f.project, "bad branch"); !errors.Is(err, domain.ErrInvalidBranch) {
		t.Errorf("bad branch: err = %v", err)
	}
}

func TestTenantsDontSeeEachOthersUsages(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	h.ingest(t, f, "plugin", "web", "abcdef1", "main", use{"checkout.pay", "a.vue", 1})

	other := harnessFor(t, "bolt")
	if _, err := other.svc.MessageUsages(other.developer(), f.project, f.ids["checkout.pay"], app.UsageQuery{}); !errors.Is(err, app.ErrProjectNotFound) {
		t.Errorf("other tenant: err = %v, want ErrProjectNotFound", err)
	}
	// Even around Catalog, the other tenant's transaction sees no rows.
	var builds []domain.BuildSummary
	err := contextpg.NewTransactor(db.NewUnitOfWork(env.App)).InTenant(other.developer(),
		func(ctx context.Context, st app.Store) error {
			var err error
			builds, err = st.BuildSummaries(ctx, f.project)
			return err
		})
	if err != nil || len(builds) != 0 {
		t.Errorf("other tenant sees %d builds (err %v)", len(builds), err)
	}
	// Nor does its retention reach them.
	if p, err := other.svc.PurgeProject(other.developer(), f.project); err != nil || len(p.Builds) != 0 {
		t.Errorf("other tenant purged %v (err %v)", p.Builds, err)
	}
}
