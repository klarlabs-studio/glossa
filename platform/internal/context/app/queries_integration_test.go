//go:build integration

package app_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	catalogdomain "go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/context/app"
	"go.klarlabs.de/glossa/platform/internal/context/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// ingestDoc uploads a document as CI and moves the clock on.
func (h *harness) ingestDoc(t *testing.T, f fixture, source string, doc []byte) app.Ingested {
	t.Helper()
	out, err := h.svc.IngestUsages(h.ci(), app.IngestUsages{Project: f.project, Source: source, Document: doc})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	h.clock.advance(time.Minute)
	return out
}

func TestIngestTakesTheDefaultBranchFromTheProject(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	trunk := catalogdomain.Settings{DefaultSyntax: mfcontent.MF1, DefaultBranch: "trunk"}
	if _, err := h.catalog.UpdateProject(h.developer(), catalogdomain.ProjectID(f.project), 1,
		catalogdomain.ProjectChange{Settings: &trunk}); err != nil {
		t.Fatal(err)
	}
	onMain := h.ingest(t, f, "plugin", "web", "aaaaaaa", "main", use{"checkout.pay", "a.vue", 1})
	onTrunk := h.ingest(t, f, "plugin", "web", "bbbbbbb", "trunk", use{"checkout.pay", "b.vue", 1})
	if onMain.Build.OnDefaultBranch || !onTrunk.Build.OnDefaultBranch {
		t.Errorf("on default branch: main %t, trunk %t", onMain.Build.OnDefaultBranch, onTrunk.Build.OnDefaultBranch)
	}
}

func TestListBuildsPagesNewestFirst(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web", "api"}, "checkout.pay")
	first := h.ingest(t, f, "plugin", "web", "aaaaaaa", "main", use{"checkout.pay", "a.vue", 1}, use{"gone", "a.vue", 2})
	second := h.ingest(t, f, "extract", "api", "bbbbbbb", "main", use{"checkout.pay", "a.go", 1})
	third := h.ingest(t, f, "plugin", "web", "ccccccc", "feat/x", use{"x", "a.vue", 1}, use{"y", "a.vue", 2})
	ctx := h.developer()

	page, next, err := h.svc.ListBuilds(ctx, f.project, app.BuildQuery{}, pagination.Page{Size: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID != third.Build.ID || page[1].ID != second.Build.ID || next == nil {
		t.Fatalf("first page = %v, next %v", ids(page), next)
	}
	if page[0].UnknownKeys != 2 || page[1].UnknownKeys != 0 || page[0].Branch != "feat/x" || page[0].OnDefaultBranch {
		t.Errorf("first page = %+v", page)
	}
	p2, err := pagination.Parse(new(2), next)
	if err != nil {
		t.Fatal(err)
	}
	rest, next, err := h.svc.ListBuilds(ctx, f.project, app.BuildQuery{}, p2)
	if err != nil || len(rest) != 1 || rest[0].ID != first.Build.ID || rest[0].UnknownKeys != 1 || next != nil {
		t.Errorf("second page = %v, next %v, err %v", ids(rest), next, err)
	}

	web, _, err := h.svc.ListBuilds(ctx, f.project, app.BuildQuery{Application: "web"}, pagination.Page{Size: 10})
	if err != nil || !slices.Equal(ids(web), []uuid.UUID{third.Build.ID, first.Build.ID}) {
		t.Errorf("web builds = %v, err %v", ids(web), err)
	}
	if _, _, err := h.svc.ListBuilds(ctx, f.project, app.BuildQuery{Application: "ios"}, pagination.Page{Size: 10}); !errors.Is(err, app.ErrApplicationNotFound) {
		t.Errorf("unknown application: err = %v", err)
	}
	if _, _, err := h.svc.ListBuilds(ctx, uuid.New(), app.BuildQuery{}, pagination.Page{Size: 10}); !errors.Is(err, app.ErrProjectNotFound) {
		t.Errorf("unknown project: err = %v", err)
	}
	if _, _, err := h.svc.ListBuilds(context.Background(), f.project, app.BuildQuery{}, pagination.Page{Size: 10}); !errors.Is(err, authz.ErrUnauthenticated) {
		t.Errorf("no principal: err = %v", err)
	}
}

func ids(bs []app.BuildRecord) []uuid.UUID {
	out := make([]uuid.UUID, len(bs))
	for i, b := range bs {
		out[i] = b.ID
	}
	return out
}

func keysOf(us []app.UsageView) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.Key
	}
	return out
}

func TestListUsagesByRouteComponentAndFile(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "cart.title", "checkout.help", "checkout.pay", "nav.home")
	h.ingestDoc(t, f, "plugin", documentOf("web", "aaaaaaa", "main",
		placedUse{use{"cart.title", "src/Cart.vue", 3}, "Cart", "/cart"},
		placedUse{use{"checkout.help", "src/Payment.vue", 4}, "Payment", "/checkout"},
		placedUse{use{"checkout.pay", "src/Payment.vue", 9}, "Payment", "/checkout"},
		placedUse{use{"nav.home", "index.html", 2}, "", ""},
	))
	// A branch moves checkout.pay to the cart.
	h.ingestDoc(t, f, "plugin", documentOf("web", "bbbbbbb", "feat/x",
		placedUse{use{"checkout.pay", "src/Cart.vue", 5}, "Cart", "/cart"},
	))
	ctx := h.developer()
	list := func(branch string, filter app.UsageFilter) []string {
		t.Helper()
		got, err := h.svc.ListUsages(ctx, f.project, branch, filter, pagination.Page{Size: 50})
		if err != nil {
			t.Fatal(err)
		}
		return keysOf(got.Usages)
	}
	if got := list("", app.UsageFilter{Route: "/checkout"}); !slices.Equal(got, []string{"checkout.help", "checkout.pay"}) {
		t.Errorf("on /checkout = %v", got)
	}
	if got := list("", app.UsageFilter{Component: "Cart"}); !slices.Equal(got, []string{"cart.title"}) {
		t.Errorf("in Cart = %v", got)
	}
	if got := list("", app.UsageFilter{File: "index.html"}); !slices.Equal(got, []string{"nav.home"}) {
		t.Errorf("in index.html = %v", got)
	}
	if got := list("", app.UsageFilter{Route: "/checkout", Component: "Cart"}); len(got) != 0 {
		t.Errorf("filters combine: %v", got)
	}
	if got := list("feat/x", app.UsageFilter{Route: "/cart"}); !slices.Equal(got, []string{"checkout.pay"}) {
		t.Errorf("branch view of /cart = %v", got)
	}
	if got := list("", app.UsageFilter{}); len(got) != 4 {
		t.Errorf("every current usage = %v", got)
	}

	// Pages continue where the last one ended.
	var paged []string
	page := pagination.Page{Size: 1}
	for range 10 {
		got, err := h.svc.ListUsages(ctx, f.project, "", app.UsageFilter{}, page)
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, keysOf(got.Usages)...)
		if got.Next == nil {
			break
		}
		if page, err = pagination.Parse(new(1), got.Next); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(paged, []string{"cart.title", "checkout.help", "checkout.pay", "nav.home"}) {
		t.Errorf("paged = %v", paged)
	}
	if _, err := h.svc.ListUsages(ctx, f.project, "", app.UsageFilter{}, pagination.Page{Size: 1, After: "nonsense"}); err == nil {
		t.Error("a malformed cursor was accepted")
	}
}

func TestCoLocatedMessagesShareARouteOrACapture(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay", "checkout.help", "checkout.total", "cart.title", "nav.home")
	h.ingestDoc(t, f, "plugin", documentOf("web", "aaaaaaa", "main",
		placedUse{use{"cart.title", "src/Cart.vue", 3}, "Cart", "/cart"},
		placedUse{use{"checkout.pay", "src/Payment.vue", 9}, "Payment", "/checkout"},
		placedUse{use{"checkout.total", "src/Payment.vue", 12}, "Payment", "/checkout"},
		placedUse{use{"nav.home", "index.html", 2}, "", ""},
	))
	// The capture shows checkout.pay with checkout.help (and a key the
	// catalog doesn't know).
	capture := h.ingest(t, f, "capture", "web", "aaaaaaa", "main").Build
	if _, err := h.svc.IngestCapture(h.ci(), app.IngestCapture{Project: f.project, Build: capture.ID, Capture: shot("/checkout", "png")}); err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.CoLocated(h.developer(), f.project, f.ids["checkout.pay"], 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []uuid.UUID{f.ids["checkout.help"], f.ids["checkout.total"]}
	slices.SortFunc(want, func(a, b uuid.UUID) int { return compareUUID(a, b) })
	if !slices.Equal(got, want) {
		t.Errorf("co-located = %v, want checkout.help and checkout.total %v", got, want)
	}
	if got, err := h.svc.CoLocated(h.developer(), f.project, f.ids["nav.home"], 5); err != nil || len(got) != 0 {
		t.Errorf("a message on no route = %v, err %v", got, err)
	}
	if _, err := h.svc.CoLocated(h.developer(), f.project, f.ids["checkout.pay"], 0); !errors.Is(err, app.ErrInvalidQuery) {
		t.Errorf("limit 0: err = %v", err)
	}
}

func compareUUID(a, b uuid.UUID) int {
	return slices.Compare(a[:], b[:])
}

// limiter allows n uploads per key.
type limiter struct {
	mu   sync.Mutex
	n    int
	seen map[string]int
}

func (l *limiter) Allow(_ context.Context, key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen[key]++
	return l.seen[key] <= l.n
}

func TestUploadsAreRateLimitedPerTenant(t *testing.T) {
	l := &limiter{n: 2, seen: map[string]int{}}
	h := newHarness(t, app.WithLimiter(l))
	f := h.project(t, "shop", []string{"web"}, "checkout.pay")
	doc := document("web", "aaaaaaa", "main", use{"checkout.pay", "a.vue", 1})
	for range 2 {
		if _, err := h.svc.IngestUsages(h.ci(), app.IngestUsages{Project: f.project, Source: "plugin", Document: doc}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.svc.IngestUsages(h.ci(), app.IngestUsages{Project: f.project, Source: "plugin", Document: doc}); !errors.Is(err, app.ErrRateLimited) {
		t.Errorf("third upload: err = %v, want ErrRateLimited", err)
	}
	if l.seen["tenant:"+h.tenant.String()] != 3 {
		t.Errorf("limiter keys = %v", l.seen)
	}
}

// metrics records what Context reports.
type metrics struct {
	mu       sync.Mutex
	ingested []string
	coverage map[uuid.UUID][2]int
	captured map[uuid.UUID][2]int
	captures []string
	purges   []string
	// used is the last capture storage reported per tenant, with the
	// quota it was measured against.
	used map[tenancy.ID][2]int64
}

func (m *metrics) StorageUsed(tenant tenancy.ID, used, quota int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.used == nil {
		m.used = map[tenancy.ID][2]int64{}
	}
	m.used[tenant] = [2]int64{used, quota}
}

// storage is the tenant's last reported capture storage and its quota.
func (m *metrics) storage(tenant tenancy.ID) [2]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.used[tenant]
}

func (m *metrics) Purged(p app.Purged) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purges = append(m.purges, fmt.Sprintf("%d builds, %d captures, %d images",
		len(p.Builds), p.Captures, p.ImagesDeleted))
}

func (m *metrics) CapturesIngested(_ tenancy.ID, captures, regions, stored, deduplicated int, bytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.captures = append(m.captures, fmt.Sprintf("%d captures, %d regions, %d stored, %d deduplicated, bytes %t",
		captures, regions, stored, deduplicated, bytes > 0))
}

func (m *metrics) CaptureCoverage(_ tenancy.ID, project uuid.UUID, active, captured int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.captured == nil {
		m.captured = map[uuid.UUID][2]int{}
	}
	m.captured[project] = [2]int{active, captured}
}

func (m *metrics) BuildIngested(source domain.Source, usages, unknown int, replayed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	kind := "new"
	if replayed {
		kind = "replay"
	}
	m.ingested = append(m.ingested, string(source)+":"+kind+":"+itoa(usages)+"/"+itoa(unknown))
}

func (m *metrics) Coverage(_ tenancy.ID, project uuid.UUID, active, used int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.coverage[project] = [2]int{active, used}
}

func itoa(n int) string { return string(rune('0' + n)) }

func TestIngestRecordsMetricsAndCoverage(t *testing.T) {
	m := &metrics{coverage: map[uuid.UUID][2]int{}}
	h := newHarness(t, app.WithMetrics(m))
	f := h.project(t, "shop", []string{"web"}, "a.one", "b.two", "c.three")
	doc := document("web", "aaaaaaa", "main", use{"a.one", "a.vue", 1}, use{"zz.unknown", "a.vue", 2})
	h.ingestDoc(t, f, "plugin", doc)
	h.ingestDoc(t, f, "plugin", doc)
	h.ingest(t, f, "plugin", "web", "bbbbbbb", "feat/x", use{"b.two", "b.vue", 1})
	h.drain(t)
	if want := []string{"plugin:new:2/1", "plugin:replay:2/1", "plugin:new:1/0"}; !slices.Equal(m.ingested, want) {
		t.Errorf("ingested = %v, want %v", m.ingested, want)
	}
	// Only the default branch's build measures coverage: 1 of 3 used.
	if got := m.coverage[f.project]; got != [2]int{3, 1} {
		t.Errorf("coverage = %v, want 3 active, 1 used", got)
	}
}

func TestUsagesOfKeySaysWhenTheLimitCutsThemShort(t *testing.T) {
	h := newHarness(t)
	f := h.project(t, "shop", []string{"web"}, "checkout.pay", "nav.home")
	h.ingest(t, f, "plugin", "web", "aaaaaaa", "main", use{"checkout.pay", "a.vue", 1}, use{"checkout.pay", "b.vue", 2})
	ctx := h.developer()
	got, err := h.svc.UsagesOfKey(ctx, f.project, "checkout.pay", app.UsageQuery{Limit: 1})
	if err != nil || got.MessageID != f.ids["checkout.pay"] || len(got.Usages) != 1 || !got.Truncated {
		t.Errorf("limit 1 = %+v, err %v", got, err)
	}
	all, err := h.svc.UsagesOfKey(ctx, f.project, "checkout.pay", app.UsageQuery{})
	if err != nil || len(all.Usages) != 2 || all.Truncated {
		t.Errorf("default limit = %+v, err %v", all, err)
	}
	none, err := h.svc.UsagesOfKey(ctx, f.project, "nav.home", app.UsageQuery{})
	if err != nil || none.Usages == nil || len(none.Usages) != 0 {
		t.Errorf("unused message = %+v, err %v", none, err)
	}
}
