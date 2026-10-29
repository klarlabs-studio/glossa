package app_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// ListFindings against a store that remembers what it was asked. The
// integration tests grade the same surface against Postgres; these ask
// the narrower question — what the service resolves, validates and
// hands down — and they ask it without a container, so the answer is
// there on every `go test ./...`.

// ── a store that remembers what it was asked ────────────────────────

type fakeStore struct {
	run  domain.CheckRun
	rows []app.FindingRecord
	// runErr is what the run lookup answers instead of run.
	runErr error
	// runs and versions are what the policy surface reads: the runs an
	// impact preview is measured against, and the saved policy versions.
	runs     []domain.CheckRun
	versions []app.PolicyVersion

	// records lets the store take a write; without it a write is a bug
	// on the path under test. recorded and inserted are what it took.
	records  bool
	recorded domain.CheckRun
	inserted []domain.Finding
	// sighted is what each capture's stored findings fingerprint: the
	// previous sighting of a scope.
	sighted      map[uuid.UUID][]string
	lastPrevious uuid.UUID

	// linguistic is the linguistic-QA job table (migration 0038); its
	// methods are in linguistic_test.go, beside the tests that use them.
	linguistic map[uuid.UUID]domain.LinguisticJob

	// The daily sweep's half (RFC 0005 §13 wave 7): how many waivers
	// and runs the store says it retired, and what it was asked.
	expiring    int
	oldRuns     int
	sweepErr    error
	sweptAt     time.Time
	sweptCutoff time.Time
	sweptLimit  int

	// trend is the findings-by-day rollup a test set, and rolledUp the
	// days a recorded run restated.
	trend    []domain.DailyFindings
	rolledUp []time.Time

	// published are the domain events the writes raised, in order.
	published []outbox.Event

	// What the last ListFindings and run lookup passed down.
	lastTrendFrom time.Time
	lastTrendTo   time.Time
	lastRunFilter app.FindingFilter
	lastRun       app.RunFilter
	lastTriggers  []domain.Trigger
	lastAfter     string
	lastLimit     int
	lastNow       time.Time
	lastCapture   uuid.UUID
	lastRegion    string
}

// recordingStore takes writes and has no waivers: the store a capture
// upload's findings are recorded into.
func recordingStore() *fakeStore { return &fakeStore{records: true} }

func (f *fakeStore) LatestCheckRun(_ context.Context, _ uuid.UUID, filter app.RunFilter) (domain.CheckRun, error) {
	f.lastRun = filter
	return f.run, f.runErr
}

func (f *fakeStore) CheckRun(_ context.Context, _, id uuid.UUID) (domain.CheckRun, error) {
	f.lastRun = app.RunFilter{}
	if f.runErr != nil {
		return domain.CheckRun{}, f.runErr
	}
	if id != f.run.ID {
		return domain.CheckRun{}, app.ErrCheckRunNotFound
	}
	return f.run, nil
}

// ListFindings keyset-pages rows the way the query does, so a cursor
// the service issues has to be one the store can continue from.
func (f *fakeStore) ListFindings(
	_ context.Context, _ domain.CheckRun, filter app.FindingFilter, after string, limit int, now time.Time,
) ([]app.FindingRecord, error) {
	f.lastRunFilter, f.lastAfter, f.lastLimit, f.lastNow = filter, after, limit, now
	var out []app.FindingRecord
	for _, r := range f.rows {
		if after != "" && r.SortKey <= after {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeStore) CountFindings(context.Context, domain.CheckRun, time.Time) (domain.Counts, error) {
	return f.run.Counts, nil
}

// CountFindingsByLayer groups whatever rows a test set by locale and
// layer, so the summary reads a breakdown that sums to the counts
// beside it.
func (f *fakeStore) CountFindingsByLayer(
	_ context.Context, _ domain.CheckRun, _ time.Time,
) ([]domain.LocaleLayerCount, error) {
	byLocale := map[string][]domain.Finding{}
	for _, r := range f.rows {
		byLocale[r.Locus.Locale] = append(byLocale[r.Locus.Locale], r.Finding)
	}
	locales := make([]string, 0, len(byLocale))
	for locale := range byLocale {
		locales = append(locales, locale)
	}
	slices.Sort(locales)
	var out []domain.LocaleLayerCount
	for _, locale := range locales {
		for _, c := range domain.ByLayer(byLocale[locale]) {
			out = append(out, domain.LocaleLayerCount{Locale: locale, LayerCount: c})
		}
	}
	return out, nil
}

// RollUpFindingsByDay records the day a run restated.
func (f *fakeStore) RollUpFindingsByDay(_ context.Context, _ uuid.UUID, day time.Time) error {
	if !f.records {
		panic("not on this path")
	}
	f.rolledUp = append(f.rolledUp, day.UTC())
	return nil
}

// FindingsByDay hands back the trend a test set.
func (f *fakeStore) FindingsByDay(
	_ context.Context, _ uuid.UUID, from, to time.Time,
) ([]domain.DailyFindings, error) {
	f.lastTrendFrom, f.lastTrendTo = from, to
	return f.trend, nil
}

// ListCaptureFindings hands back the rows a test set, and remembers
// what it was asked for: the query does the filtering, and what the
// service must get right is which capture and region it asks about.
func (f *fakeStore) ListCaptureFindings(
	_ context.Context, _, capture uuid.UUID, region, after string, limit int, now time.Time,
) ([]app.FindingRecord, error) {
	f.lastCapture, f.lastRegion, f.lastAfter, f.lastLimit, f.lastNow = capture, region, after, limit, now
	return f.rows, nil
}

// CaptureFingerprints is what the previous capture of a scope showed,
// as a test set it: the state the two-sighting rule counts against.
func (f *fakeStore) CaptureFingerprints(_ context.Context, _, capture uuid.UUID) ([]string, error) {
	f.lastPrevious = capture
	return f.sighted[capture], nil
}

// InsertCheckRun and InsertFindings record what a run stored, for the
// tests that are about what the service wrote rather than what it read.
// A store that never expects a write panics instead (recordingStore).
func (f *fakeStore) InsertCheckRun(_ context.Context, r domain.CheckRun) error {
	if !f.records {
		panic("not on this path")
	}
	f.recorded = r
	return nil
}

func (f *fakeStore) InsertFindings(_ context.Context, _, _ uuid.UUID, fs []domain.Finding) error {
	if !f.records {
		panic("not on this path")
	}
	f.inserted = append(f.inserted, fs...)
	return nil
}

// Publish keeps the events a write raised, so a test can ask what the
// rest of the platform was told.
func (f *fakeStore) Publish(_ context.Context, e outbox.Event) error {
	if !f.records {
		panic("not on this path")
	}
	if err := e.Validate(); err != nil { // what the outbox would refuse
		return err
	}
	f.published = append(f.published, e)
	return nil
}

// ListCheckRuns returns the runs the impact preview is measured
// against, newest first, and nothing when a test set none.
func (f *fakeStore) ListCheckRuns(
	_ context.Context, _ uuid.UUID, _ app.RunFilter, _ *app.RunCursor, limit int,
) ([]domain.CheckRun, error) {
	if len(f.runs) > limit {
		return f.runs[:limit], nil
	}
	return f.runs, nil
}

// HasCheckRunOf answers from the runs a test set, and remembers the
// triggers it was asked about.
func (f *fakeStore) HasCheckRunOf(_ context.Context, _ uuid.UUID, triggers []domain.Trigger) (bool, error) {
	f.lastTriggers = triggers
	for _, r := range f.runs {
		if slices.Contains(triggers, r.Trigger) {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) InsertPolicyVersion(_ context.Context, v app.PolicyVersion) (bool, error) {
	for _, had := range f.versions {
		if had.Version == v.Version {
			return false, nil
		}
	}
	f.versions = append(f.versions, v)
	return true, nil
}

func (f *fakeStore) PolicyVersion(_ context.Context, _ uuid.UUID, version int) (app.PolicyVersion, error) {
	for _, v := range f.versions {
		if v.Version == version {
			return v, nil
		}
	}
	return app.PolicyVersion{}, app.ErrPolicyVersionNotFound
}

// ListPolicyVersions pages newest first, the way the query does.
func (f *fakeStore) ListPolicyVersions(
	_ context.Context, _ uuid.UUID, after *int, limit int,
) ([]app.PolicyVersion, error) {
	ordered := slices.Clone(f.versions)
	slices.SortFunc(ordered, func(a, b app.PolicyVersion) int { return b.Version - a.Version })
	var out []app.PolicyVersion
	for _, v := range ordered {
		if after != nil && v.Version >= *after {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, v)
	}
	return out, nil
}

func (f *fakeStore) LatestFinding(context.Context, uuid.UUID, string) (app.FindingSummary, bool, error) {
	panic("not on this path")
}

func (f *fakeStore) UpsertWaiver(context.Context, domain.Waiver) (domain.Waiver, bool, error) {
	panic("not on this path")
}
func (f *fakeStore) Waiver(context.Context, uuid.UUID, uuid.UUID) (domain.Waiver, error) {
	panic("not on this path")
}

func (f *fakeStore) ListWaivers(
	context.Context, uuid.UUID, app.WaiverFilter, *app.WaiverCursor, int, time.Time,
) ([]app.WaiverRecord, error) {
	panic("not on this path")
}
func (f *fakeStore) RevokeWaiver(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	panic("not on this path")
}

func (f *fakeStore) LiveWaivers(context.Context, uuid.UUID, time.Time) ([]domain.Waiver, error) {
	if !f.records {
		panic("not on this path")
	}
	return nil, nil
}

type fakeTx struct{ store *fakeStore }

func (t fakeTx) InTenant(ctx context.Context, fn func(context.Context, app.Store) error) error {
	return fn(ctx, t.store)
}

// knownProjects is a Catalog that knows one project, so an unknown one
// is a 404 and not an empty list. It also holds that project's check
// policy, because Catalog is where the document that grades lives.
type knownProjects struct {
	id uuid.UUID
	// policy is the stored document and projectVersion the project row's
	// ETag, which a save has to still match.
	policy         checkpolicy.Policy
	projectVersion int
	// open maps open branch names to their pull requests.
	open map[string]int
	// messages is the catalog's key → message ID, which is what a
	// reported finding's fingerprint is computed over. askedKeys are the
	// keys the last resolution asked about.
	messages  map[string]uuid.UUID
	askedKeys []string
	// conflict makes the next save lose the race.
	conflict bool
	// saved and savedIfMatch record what the last save asked for.
	saved        *checkpolicy.Policy
	savedIfMatch int
}

func (c *knownProjects) Project(_ context.Context, project uuid.UUID) error {
	if project != c.id {
		return app.ErrProjectNotFound
	}
	return nil
}

// MessageIDs resolves only the keys the catalog was given: a key it
// does not know is absent, and the finding's identity then falls back
// to the key.
func (c *knownProjects) MessageIDs(
	_ context.Context, project uuid.UUID, keys []string,
) (map[string]uuid.UUID, error) {
	if project != c.id {
		return nil, app.ErrProjectNotFound
	}
	c.askedKeys = append(c.askedKeys, keys...)
	out := map[string]uuid.UUID{}
	for _, k := range keys {
		if id, ok := c.messages[k]; ok {
			out[k] = id
		}
	}
	return out, nil
}

func (c *knownProjects) CheckPolicy(_ context.Context, project uuid.UUID) (app.StoredPolicy, error) {
	if project != c.id {
		return app.StoredPolicy{}, app.ErrProjectNotFound
	}
	return app.StoredPolicy{Policy: c.policy, ProjectVersion: c.projectVersion}, nil
}

func (c *knownProjects) SaveCheckPolicy(
	_ context.Context, project uuid.UUID, ifMatch int, p checkpolicy.Policy,
) error {
	if project != c.id {
		return app.ErrProjectNotFound
	}
	if c.conflict {
		return app.ErrPolicyConflict
	}
	c.saved, c.savedIfMatch = &p, ifMatch
	c.policy, c.projectVersion = p, c.projectVersion+1
	return nil
}

func (c *knownProjects) OpenPullRequests(_ context.Context, project uuid.UUID) (map[string]int, error) {
	if project != c.id {
		return nil, app.ErrProjectNotFound
	}
	return c.open, nil
}

// storeWith is a completed, failing run of `main` with n findings in
// the order the query returns them: errors first, then by layer,
// locale, key and id, joined the way sort_key is (db/queries/quality/
// findings.sql).
func storeWith(n int) *fakeStore {
	s := &fakeStore{run: domain.CheckRun{
		ID: uuid.New(), Ref: "main", Trigger: domain.TriggerCLI, Conclusion: domain.ConclusionFailure,
		Layers: []domain.Layer{domain.LayerParity}, Counts: domain.Counts{Errors: n},
		StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(),
	}}
	for i := range n {
		id := uuid.New()
		key := fmt.Sprintf("checkout.pay.%02d", i)
		s.rows = append(s.rows, app.FindingRecord{
			Finding: domain.New(domain.Finding{
				Layer: domain.LayerParity, Code: "argument_missing", Severity: domain.Error,
				Locus: domain.Locus{Key: key, Locale: "de"}, Subject: key,
			}),
			SortKey: "0\x01parity\x01de\x01" + key + "\x01" + id.String(),
		})
	}
	return s
}

// serviceFor returns a service over store, and the project its Catalog
// knows.
func serviceFor(store *fakeStore) (*app.Service, uuid.UUID) {
	svc, _, project := serviceAndCatalog(store)
	return svc, project
}

// serviceAndCatalog also hands back the Catalog, for the tests that
// care what the service stored through it.
func serviceAndCatalog(store *fakeStore) (*app.Service, *knownProjects, uuid.UUID) {
	project := uuid.New()
	catalog := &knownProjects{id: project, projectVersion: 1}
	return app.NewService(fakeTx{store: store}, catalog), catalog, project
}

// readCtx carries a principal that may read the catalog.
func readCtx(t *testing.T) context.Context {
	t.Helper()
	return authztest.Token(t.Context(), tenancy.NewID(), "read")
}

func pageOf(size int) pagination.Page { return pagination.Page{Size: size} }

// TestListFindingsReadsTheNewestRunOfTheRef: a finding belongs to a
// run, so a list that names no run reads exactly one — the newest of
// the ref and commit asked for — and never merges two runs' copies of
// the same problem.
//
// Before M4's merge the store took a `completedOnly` flag and this test
// asserted a read surface never shows a run still in flight. The flag
// has no caller any more: RecordCheckRun stores a run together with its
// verdict, so the store holds no in-flight run to exclude. What is left
// to pin is that the ref and the commit reach the store unchanged and
// that nothing else is invented as a filter.
func TestListFindingsReadsTheNewestRunOfTheRef(t *testing.T) {
	store := storeWith(3)
	svc, project := serviceFor(store)

	got, err := svc.ListFindings(readCtx(t), project,
		app.FindingQuery{Ref: "main", Commit: "abc"}, pageOf(pagination.DefaultPageSize))
	if err != nil {
		t.Fatal(err)
	}
	if got.Run == nil || got.Run.ID != store.run.ID || got.Run.Ref != "main" {
		t.Fatalf("run = %+v, want the store's", got.Run)
	}
	if len(got.Items) != 3 || got.Next != nil {
		t.Fatalf("page = %d items, next %v, want all three on one page", len(got.Items), got.Next)
	}
	if got.Counts != (domain.Counts{Errors: 3}) {
		t.Errorf("counts = %+v, want the run's, recounted against today's waivers", got.Counts)
	}
	if want := (app.RunFilter{Ref: "main", Commit: "abc"}); store.lastRun != want {
		t.Errorf("run filter = %+v, want %+v", store.lastRun, want)
	}
	if store.lastLimit != pagination.DefaultPageSize+1 {
		t.Errorf("limit = %d, want the page size plus the lookahead row", store.lastLimit)
	}
}

// TestListFindingsPaginates: the cursor is the order's own key, and it
// round-trips through the page token the API hands out.
func TestListFindingsPaginates(t *testing.T) {
	store := storeWith(5)
	svc, project := serviceFor(store)

	first, err := svc.ListFindings(readCtx(t), project, app.FindingQuery{}, pageOf(2))
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 {
		t.Fatalf("findings = %d, want 2", len(first.Items))
	}
	if first.Next == nil {
		t.Fatal("next = nil, want a token: two of five were returned")
	}
	next, err := pagination.Parse(nil, first.Next)
	if err != nil {
		t.Fatalf("the token this list issued was refused: %v", err)
	}
	if want := first.Items[1].SortKey; next.After != want {
		t.Fatalf("cursor = %q, want the last row's sort key %q", next.After, want)
	}

	second, err := svc.ListFindings(readCtx(t), project, app.FindingQuery{}, pagination.Page{Size: 2, After: next.After})
	if err != nil {
		t.Fatal(err)
	}
	if store.lastAfter != next.After {
		t.Errorf("after = %q, want the cursor", store.lastAfter)
	}
	if len(second.Items) != 2 || second.Items[0].SortKey == first.Items[0].SortKey {
		t.Fatalf("second page = %+v, want the two rows after the cursor", second.Items)
	}
}

// TestListFindingsRefusesAnUnknownFilter: a filter outside the
// vocabulary would return nothing, which reads like "clean" and is not.
//
// The old limit cases — past the cap, negative — are gone from this
// layer: ListFindings takes a pagination.Page that has already been
// validated, and pagination's own tests refuse both.
func TestListFindingsRefusesAnUnknownFilter(t *testing.T) {
	svc, project := serviceFor(storeWith(1))
	for _, tc := range []struct {
		name   string
		filter app.FindingFilter
	}{
		{name: "a layer nobody stores", filter: app.FindingFilter{Layer: "spelling"}},
		{name: "a severity nobody stores", filter: app.FindingFilter{Severity: "info"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.ListFindings(readCtx(t), project,
				app.FindingQuery{Filter: tc.filter}, pageOf(pagination.DefaultPageSize))
			if !errors.Is(err, app.ErrInvalidQuery) {
				t.Fatalf("err = %v, want ErrInvalidQuery", err)
			}
		})
	}
}

// TestListFindingsPassesTheFilterDown: every member reaches the store,
// including the tri-state Waived, where nil means both and false means
// "only what is not waived" — not "don't filter".
func TestListFindingsPassesTheFilterDown(t *testing.T) {
	store := storeWith(1)
	svc, project := serviceFor(store)
	no := false
	want := app.FindingFilter{
		Layer: string(domain.LayerParity), Severity: string(domain.Error), Code: "argument_missing",
		Locale: "de", Namespace: "checkout", Key: "checkout.pay", Waived: &no,
	}
	if _, err := svc.ListFindings(readCtx(t), project,
		app.FindingQuery{Filter: want}, pageOf(pagination.DefaultPageSize)); err != nil {
		t.Fatal(err)
	}
	if store.lastRunFilter != want {
		t.Fatalf("filter = %+v, want %+v", store.lastRunFilter, want)
	}
	if store.lastRunFilter.Waived == nil || *store.lastRunFilter.Waived {
		t.Errorf("waived = %v, want a false that reaches the store as false", store.lastRunFilter.Waived)
	}
}

// TestListFindingsNeedsCatalogRead: a finding is about the catalog's
// messages, so reading one needs `catalog.read` (RFC 0005 §9).
func TestListFindingsNeedsCatalogRead(t *testing.T) {
	store := storeWith(1)
	svc, project := serviceFor(store)
	_, err := svc.ListFindings(context.Background(), project, app.FindingQuery{}, pageOf(pagination.DefaultPageSize))
	if !errors.Is(err, authz.ErrUnauthenticated) {
		t.Fatalf("err = %v, want unauthenticated", err)
	}
	if store.lastLimit != 0 {
		t.Error("the store was read before the permission was checked")
	}
}

// TestListFindingsPassesNotFoundOn: a run the caller named and a
// project nobody knows are both 404s. A project nobody has checked yet
// is not: it has no findings, which is an empty list.
func TestListFindingsPassesNotFoundOn(t *testing.T) {
	t.Run("a named run that isn't there", func(t *testing.T) {
		store := storeWith(0)
		store.runErr = app.ErrCheckRunNotFound
		svc, project := serviceFor(store)
		_, err := svc.ListFindings(readCtx(t), project,
			app.FindingQuery{Run: uuid.New()}, pageOf(pagination.DefaultPageSize))
		if !errors.Is(err, app.ErrCheckRunNotFound) {
			t.Fatalf("err = %v, want ErrCheckRunNotFound", err)
		}
	})

	t.Run("an unknown project", func(t *testing.T) {
		svc, _ := serviceFor(storeWith(1))
		_, err := svc.ListFindings(readCtx(t), uuid.New(), app.FindingQuery{}, pageOf(pagination.DefaultPageSize))
		if !errors.Is(err, app.ErrProjectNotFound) {
			t.Fatalf("err = %v, want ErrProjectNotFound", err)
		}
	})

	t.Run("nothing checked yet", func(t *testing.T) {
		store := storeWith(0)
		store.runErr = app.ErrCheckRunNotFound
		svc, project := serviceFor(store)
		got, err := svc.ListFindings(readCtx(t), project, app.FindingQuery{}, pageOf(pagination.DefaultPageSize))
		if err != nil {
			t.Fatalf("err = %v, want an empty page", err)
		}
		if got.Run != nil || len(got.Items) != 0 || got.Items == nil || got.Next != nil ||
			got.Counts != (domain.Counts{}) {
			t.Fatalf("page = %+v, want an empty one that renders as [] and not null", got)
		}
	})
}
