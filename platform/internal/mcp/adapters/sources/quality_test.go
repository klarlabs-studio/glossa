package sources_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz/authztest"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/mcp/adapters/sources"
	mcpapp "go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
	qualityapp "go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The seam between MCP's `findings_list` and Quality's service: the
// cursor it hands an agent has to be one it takes back, `waived` has to
// mean the tri-state filter's "only waived", and a project nobody has
// checked has to be an empty page rather than a not-found an agent
// would read as "no such project".

// store is Quality's port with only the findings path filled in;
// anything else on it panics on a nil interface, which is the bug.
type store struct {
	qualityapp.Store
	run   domain.CheckRun
	rows  []qualityapp.FindingRecord
	noRun bool

	lastFilter qualityapp.FindingFilter
	lastAfter  string
	lastLimit  int
}

func (s *store) LatestCheckRun(context.Context, uuid.UUID, qualityapp.RunFilter) (domain.CheckRun, error) {
	if s.noRun {
		return domain.CheckRun{}, qualityapp.ErrCheckRunNotFound
	}
	return s.run, nil
}

func (s *store) ListFindings(
	_ context.Context, _ domain.CheckRun, f qualityapp.FindingFilter, after string, limit int, _ time.Time,
) ([]qualityapp.FindingRecord, error) {
	s.lastFilter, s.lastAfter, s.lastLimit = f, after, limit
	var out []qualityapp.FindingRecord
	for _, r := range s.rows {
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

func (s *store) CountFindings(context.Context, domain.CheckRun, time.Time) (domain.Counts, error) {
	// What the findings are worth today, which is not the run's own
	// stored verdict: one of them has since been waived.
	return domain.Counts{Errors: 2, Waived: 1}, nil
}

type transactor struct{ store *store }

func (t transactor) InTenant(ctx context.Context, fn func(context.Context, qualityapp.Store) error) error {
	return fn(ctx, t.store)
}

// catalog knows one project. The policy side of the port is not on
// MCP's read path — an agent reads findings, never the policy document
// — so it is embedded rather than answered.
type catalog struct {
	qualityapp.Catalog
	id uuid.UUID
}

func (c catalog) Project(_ context.Context, project uuid.UUID) error {
	if project != c.id {
		return qualityapp.ErrProjectNotFound
	}
	return nil
}

func newStore(n int) *store {
	s := &store{run: domain.CheckRun{
		ID: uuid.New(), Ref: "main", Trigger: domain.TriggerCLI, Conclusion: domain.ConclusionFailure,
		PolicyVersion: 3, Layers: []domain.Layer{domain.LayerParity},
		Counts: domain.Counts{Errors: 3}, StartedAt: time.Now().UTC(), CompletedAt: time.Now().UTC(),
	}}
	for i := range n {
		key := string(rune('a' + i))
		s.rows = append(s.rows, qualityapp.FindingRecord{
			Finding: domain.New(domain.Finding{
				Layer: domain.LayerParity, Code: "argument_missing", Severity: domain.Error,
				Locus: domain.Locus{Key: key, Locale: "de"}, Subject: key,
			}),
			SortKey: "0\x01parity\x01de\x01" + key,
		})
	}
	return s
}

func newQuality(s *store) (*sources.Quality, uuid.UUID, context.Context) {
	project := uuid.New()
	svc := qualityapp.NewService(transactor{store: s}, catalog{id: project})
	return sources.NewQuality(svc), project, authztest.Token(context.Background(), tenancy.NewID(), "read")
}

// TestFindingsCursorRoundTrips: the `next_cursor` an agent is handed is
// an API page token, and the next call has to decode it back into the
// sort key the query pages on. Handing the token straight through as a
// cursor would compare "v1.…" against a sort key and quietly return the
// wrong rows — an agent has no way to notice.
func TestFindingsCursorRoundTrips(t *testing.T) {
	s := newStore(5)
	q, project, ctx := newQuality(s)

	_, first, cursor, err := q.Findings(ctx, project, tools.FindingsQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || cursor == "" {
		t.Fatalf("first page = %d findings, cursor %q", len(first), cursor)
	}
	if s.lastLimit != 3 {
		t.Errorf("limit = %d, want the page size plus the lookahead row", s.lastLimit)
	}

	_, second, _, err := q.Findings(ctx, project, tools.FindingsQuery{Limit: 2, After: cursor})
	if err != nil {
		t.Fatal(err)
	}
	if s.lastAfter != s.rows[1].SortKey {
		t.Fatalf("after = %q, want the second row's sort key %q", s.lastAfter, s.rows[1].SortKey)
	}
	if len(second) != 2 || second[0].Key == first[0].Key {
		t.Fatalf("second page = %+v, want the two rows after the cursor", second)
	}
}

// TestFindingsRefusesACursorItNeverIssued: restarting at the first page
// would make an agent loop forever.
func TestFindingsRefusesACursorItNeverIssued(t *testing.T) {
	q, project, ctx := newQuality(newStore(3))
	_, _, _, err := q.Findings(ctx, project, tools.FindingsQuery{Limit: 2, After: "not-a-cursor"})
	var invalid *mcpapp.InvalidArgumentError
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want an invalid argument", err)
	}
}

// TestWaivedMapsOntoTheTriState: `waived: true` narrows to what a
// waiver accepts; leaving it out leaves both in, and never means "only
// what is not waived".
func TestWaivedMapsOntoTheTriState(t *testing.T) {
	s := newStore(1)
	q, project, ctx := newQuality(s)

	if _, _, _, err := q.Findings(ctx, project, tools.FindingsQuery{Limit: 10, WaivedOnly: true}); err != nil {
		t.Fatal(err)
	}
	if s.lastFilter.Waived == nil || !*s.lastFilter.Waived {
		t.Errorf("waived = %v, want a true that reaches the filter", s.lastFilter.Waived)
	}
	if _, _, _, err := q.Findings(ctx, project, tools.FindingsQuery{Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if s.lastFilter.Waived != nil {
		t.Errorf("waived = %v, want nil: both, not only what is unwaived", *s.lastFilter.Waived)
	}
}

// TestRunCountsAreTodaysNotTheRows: the items carry today's waivers, so
// the numbers beside them must too, or a waiver an agent just made
// reads as having changed nothing.
func TestRunCountsAreTodaysNotTheRows(t *testing.T) {
	q, project, ctx := newQuality(newStore(2))
	run, _, _, err := q.Findings(ctx, project, tools.FindingsQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if run.Errors != 2 || run.Waived != 1 {
		t.Errorf("counts = %d error(s), %d waived, want today's 2 and 1", run.Errors, run.Waived)
	}
	if run.Conclusion != string(domain.ConclusionFailure) || run.PolicyVersion != 3 {
		t.Errorf("run = %+v: what it concluded when it ran stays what it was", run)
	}
}

// TestNothingCheckedYetIsAnEmptyPage, not a not-found: an agent reading
// "not found" would go looking for the project.
func TestNothingCheckedYetIsAnEmptyPage(t *testing.T) {
	s := newStore(0)
	s.noRun = true
	q, project, ctx := newQuality(s)

	run, found, cursor, err := q.Findings(ctx, project, tools.FindingsQuery{Limit: 10})
	if err != nil {
		t.Fatalf("err = %v, want an empty page", err)
	}
	if run.ID != "" || run.Ref != "" || run.Layers != nil || found == nil || len(found) != 0 || cursor != "" {
		t.Fatalf("run = %+v, findings = %v, cursor = %q", run, found, cursor)
	}
}

// TestAnUnknownProjectIsNotFound: MCP says not-found for anything this
// tenant cannot see, and never forbidden-with-detail.
func TestAnUnknownProjectIsNotFound(t *testing.T) {
	q, _, ctx := newQuality(newStore(1))
	if _, _, _, err := q.Findings(ctx, uuid.New(), tools.FindingsQuery{Limit: 10}); !errors.Is(err, tools.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
