package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The daily sweep (RFC 0005 §2.2, §2.3, §13 wave 7). Two things
// accumulate — waivers whose date has passed, and check runs past their
// 90 days — and this is the narrow question: what the service asks the
// store for, what it records, and what it refuses to do without a
// Scanner. The Postgres half (which runs are spared, that findings go
// with their run) is graded in the integration tests.

// ── the store's sweep half ──────────────────────────────────────────

func (f *fakeStore) ExpireWaivers(_ context.Context, now time.Time) (int, error) {
	f.sweptAt = now
	return f.expiring, f.sweepErr
}

func (f *fakeStore) DeleteExpiredCheckRuns(_ context.Context, cutoff time.Time, limit int) (int, error) {
	f.sweptCutoff, f.sweptLimit = cutoff, limit
	return f.oldRuns, f.sweepErr
}

// countingMetrics records the outcomes it was handed, so a test can ask
// what the sweep said about a waiver rather than what it did to a row.
type countingMetrics struct {
	app.NoMetrics
	waivers []string
}

func (m *countingMetrics) WaiverDecided(outcome string) { m.waivers = append(m.waivers, outcome) }

// oneTenant is a Scanner that reports the same tenant every time.
type oneTenant struct {
	id tenancy.ID
	// asked is what the scan was told the cutoff was.
	now, cutoff time.Time
	limit       int
	err         error
}

func (s *oneTenant) TenantsWithSweepWork(
	_ context.Context, now, cutoff time.Time, limit int,
) ([]tenancy.ID, error) {
	s.now, s.cutoff, s.limit = now, cutoff, limit
	if s.err != nil {
		return nil, s.err
	}
	return []tenancy.ID{s.id}, nil
}

// TestTheSweepRecordsExpiriesAndRetiresOldRuns: one pass over a tenant
// asks the store for both halves, with the retention cutoff 90 days
// behind the clock, and counts every expiry it recorded on the waiver
// series — because an expiry is something that became of a waiver, like
// a revocation, and the number a dashboard shows has to be true.
func TestTheSweepRecordsExpiriesAndRetiresOldRuns(t *testing.T) {
	store := &fakeStore{expiring: 3, oldRuns: 12}
	metrics := &countingMetrics{}
	now := time.Date(2026, 9, 30, 3, 0, 0, 0, time.UTC)
	svc := app.NewService(fakeTx{store: store}, &knownProjects{},
		app.WithMetrics(metrics), app.WithClock(func() time.Time { return now }))

	got, err := svc.SweepTenant(writeCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	if got.WaiversExpired != 3 || got.RunsDeleted != 12 || got.Tenants != 1 {
		t.Errorf("swept %+v", got)
	}
	if !store.sweptAt.Equal(now) {
		t.Errorf("expired waivers as of %s, want %s", store.sweptAt, now)
	}
	if want := now.Add(-domain.RunRetention); !store.sweptCutoff.Equal(want) {
		t.Errorf("retention cutoff %s, want %s (90 days)", store.sweptCutoff, want)
	}
	if store.sweptLimit != app.MaxRunsSweptPerTenant {
		t.Errorf("deleted up to %d runs, want the bound %d", store.sweptLimit, app.MaxRunsSweptPerTenant)
	}
	if len(metrics.waivers) != 3 {
		t.Fatalf("counted %v, want three expiries", metrics.waivers)
	}
	for _, o := range metrics.waivers {
		if o != app.WaiverExpired {
			t.Errorf("counted %q, want %q — an expiry is not a revocation", o, app.WaiverExpired)
		}
	}
}

// A sweep that fails records nothing: a half-counted expiry is worse
// than an uncounted one, because the number stops being true.
func TestAFailedSweepCountsNothing(t *testing.T) {
	boom := errors.New("storage is down")
	store := &fakeStore{expiring: 3, sweepErr: boom}
	metrics := &countingMetrics{}
	svc := app.NewService(fakeTx{store: store}, &knownProjects{}, app.WithMetrics(metrics))

	if _, err := svc.SweepTenant(writeCtx(t)); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if len(metrics.waivers) != 0 {
		t.Errorf("counted %v after a failure", metrics.waivers)
	}
}

// Sweeping is a write: a reader may not retire anybody's waivers.
func TestSweepingNeedsCatalogWrite(t *testing.T) {
	svc := app.NewService(fakeTx{store: &fakeStore{}}, &knownProjects{})
	if _, err := svc.SweepTenant(readCtx(t)); err == nil {
		t.Error("a read-only principal swept the tenant")
	}
}

// TestSweepVisitsEveryTenantWithWork: the cross-tenant pass hands the
// scan the same clock and the same cutoff the tenant half uses, and
// sums what each tenant's sweep did.
func TestSweepVisitsEveryTenantWithWork(t *testing.T) {
	store := &fakeStore{expiring: 1, oldRuns: 2}
	scan := &oneTenant{id: tenancy.NewID()}
	now := time.Date(2026, 9, 30, 3, 0, 0, 0, time.UTC)
	svc := app.NewService(fakeTx{store: store}, &knownProjects{},
		app.WithScanner(scan), app.WithClock(func() time.Time { return now }))

	got, err := svc.Sweep(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Tenants != 1 || got.WaiversExpired != 1 || got.RunsDeleted != 2 {
		t.Errorf("swept %+v", got)
	}
	if !scan.now.Equal(now) {
		t.Errorf("scanned as of %s, want %s", scan.now, now)
	}
	if want := now.Add(-domain.RunRetention); !scan.cutoff.Equal(want) {
		t.Errorf("scanned with cutoff %s, want %s", scan.cutoff, want)
	}
	if scan.limit <= 0 {
		t.Error("the scan was given no bound, so one run could be unbounded")
	}
}

// Without a Scanner the job says so. Answering "swept nothing" would
// read as "there was nothing to sweep", which is how a retention job
// goes years without being noticed.
func TestSweepWithoutAScannerIsAnError(t *testing.T) {
	svc := app.NewService(fakeTx{store: &fakeStore{}}, &knownProjects{})
	if _, err := svc.Sweep(t.Context()); err == nil {
		t.Error("Sweep answered without a Scanner")
	}
}

// ── what an expiry means to a waiver ────────────────────────────────

// A waiver stops accepting findings on its date, whether or not the
// sweep has run; the sweep only writes that down. Both halves have to
// hold, or a late job leaves a dead waiver accepting things.
func TestAnExpiredWaiverStopsCoveringWhetherOrNotItWasSwept(t *testing.T) {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	before, after := at.Add(-time.Hour), at.Add(time.Hour)
	dated := domain.Waiver{ExpiresAt: &at}
	swept := domain.Waiver{ExpiresAt: &at, ExpiredAt: &at}

	if !dated.Live(before) {
		t.Error("a waiver was dead before its date")
	}
	if dated.Live(after) {
		t.Error("an unswept waiver went on standing past its date")
	}
	if swept.Live(before) {
		t.Error("a recorded expiry was contradicted by the clock")
	}
	// A permanent waiver is the case §15 question 4 keeps: "Login is the
	// German term" does not expire, and the sweep never touches it.
	if !(domain.Waiver{}).Live(after) {
		t.Error("a waiver with no date expired")
	}
}

// Expires is the sweep's own predicate: it retires the dated, unrevoked
// waivers whose date has passed, once each.
func TestOnlyDatedWaiversPastTheirDateAreSwept(t *testing.T) {
	at := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	now := at.Add(time.Hour)
	later := at.Add(48 * time.Hour)
	for what, tc := range map[string]struct {
		w    domain.Waiver
		want bool
	}{
		"a dated waiver past its date": {domain.Waiver{ExpiresAt: &at}, true},
		"one whose date has not come":  {domain.Waiver{ExpiresAt: &later}, false},
		"a permanent one":              {domain.Waiver{}, false},
		"one already recorded":         {domain.Waiver{ExpiresAt: &at, ExpiredAt: &at}, false},
		"one somebody revoked":         {domain.Waiver{ExpiresAt: &at, RevokedAt: &at}, false},
	} {
		if got := tc.w.Expires(now); got != tc.want {
			t.Errorf("%s: Expires = %v, want %v", what, got, tc.want)
		}
	}
}
