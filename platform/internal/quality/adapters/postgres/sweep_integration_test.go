//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	qualitypg "go.klarlabs.de/glossa/platform/internal/quality/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/quality/app"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// The two daily sweeps against the real tables (migration 0039,
// RFC 0005 §2.2, §2.3). These are the questions only Postgres can
// answer: which run a `DISTINCT ON` spares, whether a deleted run takes
// its findings with it, whether the rollup survives, and what the
// system scope can and cannot see.

// waiverFor stores one waiver of the project, expiring at expires (nil
// never).
func waiverFor(
	t *testing.T, uow *db.UnitOfWork, tenant tenancy.ID, project uuid.UUID,
	fingerprint, reason string, created time.Time, expires *time.Time,
) domain.Waiver {
	t.Helper()
	w := domain.Waiver{
		ID: uuid.Must(uuid.NewV7()), Project: project, Fingerprint: fingerprint, Reason: reason,
		Scope: domain.WaiverProject, SourceRevision: 7, CreatedBy: "token:seed",
		CreatedAt: created, ExpiresAt: expires,
	}
	err := inTenant(t, uow, tenant, func(ctx context.Context, st app.Store) error {
		stored, _, err := st.UpsertWaiver(ctx, w)
		w = stored
		return err
	})
	if err != nil {
		t.Fatalf("seed a waiver: %v", err)
	}
	return w
}

// TestTheExpirySweepRecordsAndNeverDeletes. A waiver that expires is
// not deleted — it is recorded as expired, the same way revoking never
// deletes (RFC 0005 §2.3) — so the reason somebody wrote is still there
// to read, and the dashboard can still show it.
//
// `expires_at` stays optional (§15 question 4, decided by the owner):
// the permanent waiver in this fixture is the case that keeps it so,
// and the sweep must not touch it.
func TestTheExpirySweepRecordsAndNeverDeletes(t *testing.T) {
	ctx := t.Context()
	if err := env.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	tenant, err := env.SeedTenant(ctx, "expiry")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	project := uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	past, future := now.Add(-24*time.Hour), now.Add(30*24*time.Hour)

	due := waiverFor(t, uow, tenant, project, "f_00000000000000a1",
		"the French button says Payer on purpose", now.Add(-90*24*time.Hour), &past)
	permanent := waiverFor(t, uow, tenant, project, "f_00000000000000a2",
		"Login is the German term", now.Add(-200*24*time.Hour), nil)
	later := waiverFor(t, uow, tenant, project, "f_00000000000000a3",
		"until the redesign ships", now, &future)
	revoked := waiverFor(t, uow, tenant, project, "f_00000000000000a4",
		"wrong call, taken back", now.Add(-40*24*time.Hour), &past)

	err = inTenant(t, uow, tenant, func(ctx context.Context, st app.Store) error {
		if err := st.RevokeWaiver(ctx, project, revoked.ID, now.Add(-time.Hour)); err != nil {
			return err
		}
		n, err := st.ExpireWaivers(ctx, now)
		if err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("expired %d waivers, want only the dated one whose date has passed", n)
		}
		// Running it again is running it once: an expiry already
		// recorded is not recorded twice, so two replicas racing or a
		// retry after a partial failure both land here.
		again, err := st.ExpireWaivers(ctx, now)
		if err != nil {
			return err
		}
		if again != 0 {
			t.Errorf("a second sweep expired %d more", again)
		}

		got, err := st.Waiver(ctx, project, due.ID)
		if err != nil {
			return err
		}
		if got.ExpiredAt == nil {
			t.Error("the expiry was not recorded")
		} else if !got.ExpiredAt.Equal(now) {
			t.Errorf("recorded at %s, want %s", got.ExpiredAt, now)
		}
		if got.Reason != due.Reason {
			t.Errorf("reason = %q, want it still readable after the sweep", got.Reason)
		}
		if got.Live(now) {
			t.Error("an expired waiver still stands")
		}

		for what, id := range map[string]uuid.UUID{
			"a permanent waiver":           permanent.ID,
			"one whose date has not come":  later.ID,
			"one somebody already revoked": revoked.ID,
		} {
			w, err := st.Waiver(ctx, project, id)
			if err != nil {
				return err
			}
			if w.ExpiredAt != nil {
				t.Errorf("%s was expired by the sweep", what)
			}
		}

		// Nothing was deleted: every waiver is still listed, which is
		// what makes the dashboard's staleness numbers possible.
		all, err := st.ListWaivers(ctx, project, app.WaiverFilter{}, nil, 50, now)
		if err != nil {
			return err
		}
		if len(all) != 4 {
			t.Errorf("%d waivers left, want all four", len(all))
		}
		active := true
		standing, err := st.ListWaivers(ctx, project, app.WaiverFilter{Active: &active}, nil, 50, now)
		if err != nil {
			return err
		}
		if len(standing) != 2 {
			t.Errorf("%d standing, want the permanent one and the one not yet due", len(standing))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Re-waiving a fingerprint whose waiver the sweep retired gives it a
// new life rather than a retired row with a future date: the restate
// clears the record of the old expiry.
func TestRewaivingClearsARecordedExpiry(t *testing.T) {
	ctx := t.Context()
	if err := env.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	tenant, err := env.SeedTenant(ctx, "rewaive")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	project := uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	past, future := now.Add(-24*time.Hour), now.Add(60*24*time.Hour)
	first := waiverFor(t, uow, tenant, project, "f_00000000000000b1", "for now", now.Add(-72*time.Hour), &past)

	err = inTenant(t, uow, tenant, func(ctx context.Context, st app.Store) error {
		if _, err := st.ExpireWaivers(ctx, now); err != nil {
			return err
		}
		again := first
		again.ID = uuid.Must(uuid.NewV7())
		again.Reason, again.ExpiresAt, again.CreatedAt = "still true, another quarter", &future, now
		stored, created, err := st.UpsertWaiver(ctx, again)
		if err != nil {
			return err
		}
		if created {
			t.Error("re-waiving made a second live waiver for one fingerprint")
		}
		if stored.ExpiredAt != nil {
			t.Error("a renewed waiver still reads as retired")
		}
		if !stored.Live(now) {
			t.Error("a renewed waiver does not stand")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRetentionSparesTheNewestRunOfEachRef. Runs are kept 90 days
// (RFC 0005 §2.2), with one exception the sweep has to get right: the
// newest run of a ref is what every dashboard, `listFindings` and the
// summary read, so it survives whatever its age. A sweep that deleted
// it would leave a long-quiet branch looking as if it had never been
// checked.
func TestRetentionSparesTheNewestRunOfEachRef(t *testing.T) {
	ctx := t.Context()
	if err := env.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	tenant, err := env.SeedTenant(ctx, "retention")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	project := uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	cutoff := now.Add(-domain.RunRetention)

	f := []domain.Finding{finding(domain.LayerParity, "argument_missing", "checkout.pay", "de", domain.Error)}
	ancientMain := seedRun(t, uow, tenant, project, "main", now.Add(-300*24*time.Hour), f)
	oldMain := seedRun(t, uow, tenant, project, "main", now.Add(-120*24*time.Hour), f)
	freshMain := seedRun(t, uow, tenant, project, "main", now.Add(-time.Hour), f)
	// A branch nobody has pushed to in a year. Its only run is the ref's
	// newest, so it stays.
	quietBranch := seedRun(t, uow, tenant, project, "feat/abandoned", now.Add(-365*24*time.Hour), f)

	// The trend for one of the days about to be deleted, rolled up the
	// way a completed run rolls it up.
	err = inTenant(t, uow, tenant, func(ctx context.Context, st app.Store) error {
		return st.RollUpFindingsByDay(ctx, project, ancientMain.StartedAt)
	})
	if err != nil {
		t.Fatal(err)
	}

	err = inTenant(t, uow, tenant, func(ctx context.Context, st app.Store) error {
		n, err := st.DeleteExpiredCheckRuns(ctx, cutoff, app.MaxRunsSweptPerTenant)
		if err != nil {
			return err
		}
		if n != 2 {
			t.Errorf("deleted %d runs, want the two of main that are past the cutoff and not its newest", n)
		}
		for what, run := range map[string]domain.CheckRun{
			"the ref's newest run":            freshMain,
			"a quiet branch's only, old, run": quietBranch,
		} {
			if _, err := st.CheckRun(ctx, project, run.ID); err != nil {
				t.Errorf("%s was swept: %v", what, err)
			}
		}
		for what, run := range map[string]domain.CheckRun{
			"a run 300 days old": ancientMain,
			"a run 120 days old": oldMain,
		} {
			if _, err := st.CheckRun(ctx, project, run.ID); !errors.Is(err, app.ErrCheckRunNotFound) {
				t.Errorf("%s survived retention: %v", what, err)
			}
		}
		// The findings went with their run, which is what makes
		// retention worth doing at all.
		if got, err := st.ListFindings(ctx, ancientMain, app.FindingFilter{}, "", 10, now); err != nil {
			return err
		} else if len(got) != 0 {
			t.Errorf("%d findings outlived their run", len(got))
		}
		// And the trend did not, which is why retention can be this
		// blunt: the rollup is one row per day, layer and project, and
		// nothing here touches it (migration 0035).
		days, err := st.FindingsByDay(ctx, project, ancientMain.StartedAt.AddDate(0, 0, -1), now)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(days, func(d domain.DailyFindings) bool {
			return d.Day.Year() == ancientMain.StartedAt.Year() && d.Day.YearDay() == ancientMain.StartedAt.YearDay()
		}) {
			t.Errorf("the deleted day's trend went with its runs: %+v", days)
		}
		// A second sweep has nothing left to do.
		if n, err := st.DeleteExpiredCheckRuns(ctx, cutoff, app.MaxRunsSweptPerTenant); err != nil {
			return err
		} else if n != 0 {
			t.Errorf("a second sweep deleted %d more", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The sweep is bounded per run, so a project that has never been swept
// doesn't make one statement unbounded; the next day takes the rest.
func TestRetentionIsBounded(t *testing.T) {
	ctx := t.Context()
	if err := env.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	tenant, err := env.SeedTenant(ctx, "bounded")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	project := uuid.New()
	now := time.Now().UTC()
	for i := range 5 {
		seedRun(t, uow, tenant, project, "main", now.AddDate(0, 0, -200-i), nil)
	}
	seedRun(t, uow, tenant, project, "main", now, nil)

	err = inTenant(t, uow, tenant, func(ctx context.Context, st app.Store) error {
		n, err := st.DeleteExpiredCheckRuns(ctx, now.Add(-domain.RunRetention), 2)
		if err != nil {
			return err
		}
		if n != 2 {
			t.Errorf("deleted %d runs, want the limit of 2", n)
		}
		rest, err := st.DeleteExpiredCheckRuns(ctx, now.Add(-domain.RunRetention), 100)
		if err != nil {
			return err
		}
		if rest != 3 {
			t.Errorf("the next pass deleted %d, want the remaining 3", rest)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestTheScannerFindsTenantsWithWork: the cross-tenant half runs as
// glossa_system, which migration 0039 gives two timestamps and a tenant
// id and nothing else. A tenant with neither an expired waiver nor an
// old run is not visited at all.
func TestTheScannerFindsTenantsWithWork(t *testing.T) {
	ctx := t.Context()
	if err := env.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	busy, err := env.SeedTenant(ctx, "busy")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := env.SeedTenant(ctx, "stale")
	if err != nil {
		t.Fatal(err)
	}
	idle, err := env.SeedTenant(ctx, "idle")
	if err != nil {
		t.Fatal(err)
	}
	uow := db.NewUnitOfWork(env.App)
	now := time.Now().UTC()
	past := now.Add(-time.Hour)

	// One tenant with a waiver past its date …
	waiverFor(t, uow, busy, uuid.New(), "f_00000000000000c1", "expired", now.Add(-40*24*time.Hour), &past)
	// … one with a run past its retention …
	seedRun(t, uow, stale, uuid.New(), "main", now.AddDate(0, 0, -200), nil)
	// … and one with a recent run and a permanent waiver, which is work
	// for nobody.
	seedRun(t, uow, idle, uuid.New(), "main", now, nil)
	waiverFor(t, uow, idle, uuid.New(), "f_00000000000000c2", "Login is the German term", now, nil)

	got, err := qualitypg.NewScanner(uow).TenantsWithSweepWork(ctx, now, now.Add(-domain.RunRetention), 50)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, busy) {
		t.Error("the tenant with an expired waiver was not visited")
	}
	if !slices.Contains(got, stale) {
		t.Error("the tenant with an old run was not visited")
	}
	if slices.Contains(got, idle) {
		t.Errorf("a tenant with nothing to sweep was visited: %v", got)
	}
}
