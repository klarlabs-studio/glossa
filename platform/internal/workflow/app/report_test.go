package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/workflow/app"
	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

// fakeFacts answers a unit's quality facts from a table, and records
// the completion time each was asked about.
type fakeFacts struct {
	units map[domain.Unit]app.UnitQuality
	asked map[domain.Unit]time.Time
}

func (f *fakeFacts) UnitQuality(_ context.Context, _, message uuid.UUID, locale string, completed time.Time) (app.UnitQuality, error) {
	u := domain.Unit{Message: message, Locale: locale}
	f.asked[u] = completed
	return f.units[u], nil
}

// reportFixture is a seeded history: two vendors' completed work in de,
// a late assignment, a reworked unit, an open assignment and one
// completed before the period.
type reportFixture struct {
	w                 *world
	facts             *fakeFacts
	lingua, wordsmith domain.Assignee
	t0                time.Time
	units             []domain.Unit
}

func newReportFixture(t *testing.T) *reportFixture {
	t.Helper()
	w := newWorld(t)
	f := &reportFixture{w: w, facts: &fakeFacts{units: map[domain.Unit]app.UnitQuality{}, asked: map[domain.Unit]time.Time{}},
		lingua: domain.VendorAssignee(w.vendor), wordsmith: domain.VendorAssignee(uuid.New()),
		t0: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)}
	for range 5 {
		f.units = append(f.units, domain.Unit{Message: uuid.New(), Locale: "de"})
	}
	clock := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	w.svc = app.NewWorkService(w.work, w.dir, w.authors,
		app.WithWorkClock(func() time.Time { return clock }), app.WithQualityFacts(f.facts))

	day := func(n int) time.Time { return f.t0.Add(time.Duration(n) * 24 * time.Hour) }
	// lingua: on time, two units; one approved untouched (from TM), one
	// approved after an edit of 4 characters, with a terminology finding.
	f.done(f.lingua, day(0), ptr(day(3)), day(2), f.units[0], f.units[1])
	// lingua: late, one unit, rejected and given out again afterwards.
	f.done(f.lingua, day(1), ptr(day(2)), day(4), f.units[2])
	f.put(f.wordsmith, day(5), nil, domain.AssignmentOpen, nil, f.units[2]) // the rework
	// wordsmith: no due date, one unit still in review.
	f.done(f.wordsmith, day(2), nil, day(3), f.units[3])
	// lingua, completed before the period the tests ask for.
	f.done(f.lingua, day(-30), nil, day(-29), f.units[4])

	f.facts.units[f.units[0]] = app.UnitQuality{Found: true, SourceWords: 3, FromTM: true, ReviewState: "approved"}
	f.facts.units[f.units[1]] = app.UnitQuality{Found: true, SourceWords: 5, ReviewState: "approved", Changed: true,
		EditDistance: 4, EditRatio: 0.2, Findings: []domain.FindingCount{{Layer: "terminology", Severity: "error", Count: 1}}}
	f.facts.units[f.units[2]] = app.UnitQuality{Found: true, SourceWords: 7, ReviewState: "rejected",
		Findings: []domain.FindingCount{{Layer: "terminology", Severity: "error", Count: 1}, {Layer: "length", Severity: "warning", Count: 2}}}
	f.facts.units[f.units[3]] = app.UnitQuality{Found: true, SourceWords: 2, ReviewState: "needs_review"}
	f.facts.units[f.units[4]] = app.UnitQuality{Found: true, SourceWords: 100, ReviewState: "approved"}
	return f
}

func ptr[T any](v T) *T { return &v }

func (f *reportFixture) done(to domain.Assignee, created time.Time, due *time.Time, closed time.Time, units ...domain.Unit) {
	f.put(to, created, due, domain.AssignmentDone, &closed, units...)
}

func (f *reportFixture) put(to domain.Assignee, created time.Time, due *time.Time, state domain.AssignmentState, closed *time.Time, units ...domain.Unit) {
	a := domain.Assignment{ID: uuid.New(), ProjectID: f.w.project, Units: units, Assignee: to,
		Permission: "translations.write", DueAt: due, State: state, Version: 1, CreatedBy: "test",
		CreatedAt: created, UpdatedAt: created, ClosedAt: closed}
	if closed != nil {
		a.ClosedBy = "person:" + uuid.NewString()
	}
	f.w.work.assignments[a.ID] = a
}

func (f *reportFixture) report(ctx context.Context, q app.ReportQuery) app.QualityReport {
	f.w.t.Helper()
	r, err := f.w.svc.QualityReport(ctx, q)
	if err != nil {
		f.w.t.Fatal(err)
	}
	return r
}

func TestQualityNumbersPerVendorAndLocale(t *testing.T) {
	f := newReportFixture(t)
	r := f.report(f.w.token("read"), app.ReportQuery{Since: f.t0})
	if r.Truncated || len(r.Rows) != 2 {
		t.Fatalf("report = %+v", r)
	}
	byAssignee := map[string]app.QualityRow{}
	for _, row := range r.Rows {
		byAssignee[row.Assignee] = row
	}
	l := byAssignee[f.lingua.String()]
	if l.Locale != "de" || l.Assignments != 2 || l.OnTime != 1 || l.Late != 1 || l.NoDue != 0 || l.OnTimeRate != 0.5 {
		t.Errorf("lingua's assignments: %+v", l)
	}
	if l.Units != 3 || l.SourceWords != 15 || l.TMWords["exact"] != 3 {
		t.Errorf("lingua's volume: units %d, words %d, tm %v", l.Units, l.SourceWords, l.TMWords)
	}
	if l.Approved != 2 || l.Rejected != 1 || l.InReview != 0 || l.Changed != 1 {
		t.Errorf("lingua's review outcomes: %+v", l)
	}
	// The edit averages over the three reviewed units: (0 + 4 + 0) / 3.
	if l.MeanEditDistance != 1.333 || l.MeanEditRatio != 0.067 {
		t.Errorf("lingua's edits: distance %v, ratio %v", l.MeanEditDistance, l.MeanEditRatio)
	}
	if l.Findings["terminology"] != 2 || l.Findings["length"] != 2 || l.FindingsPerUnit != 1.333 {
		t.Errorf("lingua's findings: %v, per unit %v", l.Findings, l.FindingsPerUnit)
	}
	if l.Reworked != 1 || l.ReworkRate != 0.333 {
		t.Errorf("lingua's rework: %d, rate %v", l.Reworked, l.ReworkRate)
	}
	w := byAssignee[f.wordsmith.String()]
	if w.Assignments != 1 || w.NoDue != 1 || w.OnTimeRate != 0 || w.Units != 1 || w.InReview != 1 || w.MeanEditDistance != 0 {
		t.Errorf("wordsmith: %+v (its open assignment is not delivered work)", w)
	}
	// The facts were read at each assignment's completion.
	if got := f.facts.asked[f.units[2]]; !got.Equal(f.t0.Add(4 * 24 * time.Hour)) {
		t.Errorf("unit 2 was read as of %v", got)
	}
}

func TestAReportNarrowsByVendorAndPeriod(t *testing.T) {
	f := newReportFixture(t)
	all := f.report(f.w.token("read"), app.ReportQuery{Assignee: f.lingua.String()})
	if len(all.Rows) != 1 || all.Rows[0].Units != 4 || all.Rows[0].Assignments != 3 {
		t.Fatalf("lingua since forever: %+v", all.Rows)
	}
	recent := f.report(f.w.token("read"), app.ReportQuery{Assignee: f.lingua.String(), Since: f.t0})
	if len(recent.Rows) != 1 || recent.Rows[0].Units != 3 || recent.Since == nil {
		t.Fatalf("lingua since t0: %+v", recent)
	}
	if _, err := f.w.svc.QualityReport(f.w.token("read"), app.ReportQuery{Assignee: "vendor:acme"}); !errors.Is(err, app.ErrInvalidQuery) {
		t.Errorf("a malformed assignee: %v", err)
	}
}

// The report is a list like every other: assignments.read in the
// caller's project scope, a project outside it not found, and an
// `assigned` member — a vendor's — refused.
func TestWhoReadsAReport(t *testing.T) {
	f := newReportFixture(t)
	elsewhere := uuid.New()
	scoped, err := identity.ParseProjectScope([]string{elsewhere.String()})
	if err != nil {
		t.Fatal(err)
	}
	token := f.w.token("read")
	p, _ := authz.From(token)
	p.Projects = scoped
	scopedToken := authz.WithPrincipal(tenancy.ContextWithTenant(context.Background(), f.w.tenant), p)
	if _, err := f.w.svc.QualityReport(scopedToken, app.ReportQuery{Project: f.w.project}); !errors.Is(err, authz.ErrNotVisible) {
		t.Errorf("a project outside the token's scope: %v, want not visible", err)
	}
	if r := f.report(scopedToken, app.ReportQuery{}); len(r.Rows) != 0 {
		t.Errorf("a token scoped to another project saw %d rows", len(r.Rows))
	}

	vendorMember := f.w.person([]string{"translator"}, []string{"de"}, nil, f.w.vendor)
	vp, _ := authz.From(vendorMember)
	vp.Visibility = identity.VisibilityAssigned
	vendorMember = authz.WithPrincipal(tenancy.ContextWithTenant(context.Background(), f.w.tenant), vp)
	if _, err := f.w.svc.QualityReport(vendorMember, app.ReportQuery{}); !errors.Is(err, authz.ErrForbidden) {
		t.Errorf("a vendor's member read the report: %v", err)
	}

	bare := app.NewWorkService(f.w.work, f.w.dir, f.w.authors)
	if _, err := bare.QualityReport(f.w.token("read"), app.ReportQuery{}); !errors.Is(err, app.ErrReportUnavailable) {
		t.Errorf("a server without facts: %v", err)
	}
}
