//go:build integration

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	integrationpg "go.klarlabs.de/glossa/platform/internal/integration/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The check queue on real Postgres with row-level security on (RFC 0004
// §6.4). The in-memory tests pin the flow; these pin what only the
// database can enforce: one row per pull request, so one claim per pull
// request and therefore one writer of the sticky comment; a claim
// fenced by its token; a new commit discarding the check runs while the
// comment stays; and the timeout sweep.

func newChecks(t *testing.T) (*integrationpg.Checks, tenancy.ID) {
	t.Helper()
	ctx := context.Background()
	if err := env.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	tenant, err := env.SeedTenant(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	return integrationpg.NewChecks(db.NewUnitOfWork(env.App)), tenant
}

func aCheck(tenant tenancy.ID, repository int64, pr int, sha string, now time.Time) domain.Check {
	return domain.Check{
		ID: uuid.Must(uuid.NewV7()), TenantID: tenant.UUID(), InstallationID: 4242,
		RepositoryID: repository, PullRequest: pr, Branch: "feature/copy", HeadSHA: sha,
		State: domain.CheckQueued, RequestedAt: now, AvailableAt: now, UpdatedAt: now,
	}
}

const (
	shaOne = "9f1c2e3d4b5a69788796a5b4c3d2e1f0a9b8c7d6"
	shaTwo = "0123456789abcdef0123456789abcdef01234567"
)

func TestAPullRequestHasExactlyOneCheckRow(t *testing.T) {
	q, tenant := newChecks(t)
	ctx := context.Background()
	// Postgres keeps microseconds; a nanosecond here would never come back.
	now := time.Now().UTC().Truncate(time.Microsecond)

	first, err := q.Open(ctx, aCheck(tenant, 10101, 7, shaOne, now))
	if err != nil {
		t.Fatal(err)
	}
	// The worker writes a comment id onto it.
	first.CommentID = 555
	first.Targets = map[uuid.UUID]domain.CheckTarget{uuid.New(): {CheckRunID: 1, Annotations: []string{"aa"}}}
	claimed, ok, err := q.Claim(ctx, now, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: %v, ok=%v", err, ok)
	}
	first.ClaimToken, first.UpdatedAt = claimed.ClaimToken, now
	if err := q.Save(ctx, first, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	// A new commit on the same pull request: the row stays, the comment
	// stays, the check runs and their annotation ledgers do not.
	again, err := q.Open(ctx, aCheck(tenant, 10101, 7, shaTwo, now.Add(time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID {
		t.Fatalf("a second pull_request event made a second row: %s vs %s", again.ID, first.ID)
	}
	if again.CommentID != 555 {
		t.Fatalf("comment id = %d, want the pull request's own comment kept", again.CommentID)
	}
	if len(again.Targets) != 0 {
		t.Fatalf("targets = %+v, want a new commit's check runs to start empty", again.Targets)
	}
	if !again.RequestedAt.Equal(now.Add(time.Minute).UTC()) {
		t.Fatalf("requested_at = %s, want the new commit's own wait to start", again.RequestedAt)
	}
}

// TestAForkIsRememberedOnTheRow: the worker concludes a fork's check
// without waiting, so where the head lives has to survive the claim —
// and stop being true if the pull request is retargeted at a branch in
// this repository (migration 0025).
func TestAForkIsRememberedOnTheRow(t *testing.T) {
	q, tenant := newChecks(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	fork := aCheck(tenant, 10101, 7, shaOne, now)
	fork.FromFork = true
	if _, err := q.Open(ctx, fork); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := q.Claim(ctx, now, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: %v, ok=%v", err, ok)
	}
	if !claimed.FromFork {
		t.Fatal("the claimed check does not say the pull request came from a fork")
	}

	// The head moves into this repository: the check waits for CI again.
	if _, err := q.Open(ctx, aCheck(tenant, 10101, 7, shaTwo, now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	got, err := q.Check(ctx, 10101, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.FromFork {
		t.Fatal("the row still calls the pull request a fork's after its head moved into this repository")
	}
}

func TestOnlyOneWorkerClaimsAPullRequestAtATime(t *testing.T) {
	q, tenant := newChecks(t)
	ctx := context.Background()
	// Postgres keeps microseconds; a nanosecond here would never come back.
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := q.Open(ctx, aCheck(tenant, 10101, 7, shaOne, now)); err != nil {
		t.Fatal(err)
	}
	first, ok, err := q.Claim(ctx, now, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: %v ok=%v", err, ok)
	}
	if _, ok, err := q.Claim(ctx, now, time.Minute); err != nil || ok {
		t.Fatalf("a second worker claimed the same pull request: ok=%v err=%v", ok, err)
	}
	// A save with the wrong token writes nothing: the lease fences it.
	stale := first
	stale.ClaimToken, stale.CommentID, stale.UpdatedAt = uuid.New(), 999, now
	if err := q.Save(ctx, stale, now); err != nil {
		t.Fatal(err)
	}
	got, err := q.Check(ctx, 10101, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.CommentID != 0 {
		t.Fatalf("a worker whose lease was gone still wrote: comment id = %d", got.CommentID)
	}
}

func TestExpireMakesAWaitingCheckDueAgain(t *testing.T) {
	q, tenant := newChecks(t)
	ctx := context.Background()
	// Postgres keeps microseconds; a nanosecond here would never come back.
	now := time.Now().UTC().Truncate(time.Microsecond)
	waiting := aCheck(tenant, 10101, 7, shaOne, now.Add(-domain.CheckWait-time.Minute))
	// It is waiting for CI, so it looks again at its deadline.
	waiting.AvailableAt = now.Add(time.Hour)
	if _, err := q.Open(ctx, waiting); err != nil {
		t.Fatal(err)
	}
	// Open makes it due now; push it back to its deadline as the worker
	// does when it finds nothing ingested.
	claimed, _, err := q.Claim(ctx, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claimed.UpdatedAt = now
	if err := q.Save(ctx, claimed, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := q.Claim(ctx, now, time.Minute); err != nil || ok {
		t.Fatalf("a check waiting for CI was claimable: ok=%v err=%v", ok, err)
	}
	n, err := q.Expire(ctx, now, now)
	if err != nil || n != 1 {
		t.Fatalf("expire = %d, %v; want the one check past its wait", n, err)
	}
	if _, ok, err := q.Claim(ctx, now, time.Minute); err != nil || !ok {
		t.Fatalf("the expired check was not due again: ok=%v err=%v", ok, err)
	}
}

func TestWakeAndDropTouchOnlyTheirRepositories(t *testing.T) {
	q, tenant := newChecks(t)
	ctx := context.Background()
	// Postgres keeps microseconds; a nanosecond here would never come back.
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, c := range []domain.Check{
		aCheck(tenant, 10101, 7, shaOne, now),
		aCheck(tenant, 20202, 3, shaTwo, now),
	} {
		if _, err := q.Open(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	// Settle both, so waking them has something to do.
	for range 2 {
		claimed, ok, err := q.Claim(ctx, now, time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim: %v ok=%v", err, ok)
		}
		claimed.State, claimed.Conclusion, claimed.UpdatedAt = domain.CheckCompleted, "success", now
		if err := q.Save(ctx, claimed, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	n, err := q.Wake(ctx, []int64{10101}, "feature/copy", now)
	if err != nil || n != 1 {
		t.Fatalf("wake = %d, %v; want only the named repository's check", n, err)
	}
	if n, err := q.Wake(ctx, []int64{10101, 20202}, "", now); err != nil || n != 2 {
		t.Fatalf("wake with no branch = %d, %v; want every open check of both", n, err)
	}
	if n, err := q.DropRepository(ctx, 20202); err != nil || n != 1 {
		t.Fatalf("drop = %d, %v", n, err)
	}
	if _, err := q.Check(ctx, 20202, 3); err == nil {
		t.Fatal("a dropped repository kept its check")
	}
	if _, err := q.Check(ctx, 10101, 7); err != nil {
		t.Fatalf("the other repository's check went with it: %v", err)
	}
}

// TestTheQueueKeepsTheAppsClock is the M3 exit test's lost check: a
// check made due by the app's clock was claimed against Postgres's
// now(), so an app clock ahead of the database's left it unclaimable
// for the difference. Claim and retry measure on the clock the rows
// were written with.
func TestTheQueueKeepsTheAppsClock(t *testing.T) {
	q, tenant := newChecks(t)
	ctx := context.Background()
	ahead := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)

	if _, err := q.Open(ctx, aCheck(tenant, 10101, 7, shaOne, ahead)); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := q.Claim(ctx, ahead, time.Minute)
	if err != nil || !ok {
		t.Fatalf("a check due on the app's clock was not claimed: ok=%v err=%v", ok, err)
	}
	if err := q.Retry(ctx, claimed, ahead, 30*time.Second, "boom"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := q.Claim(ctx, ahead.Add(29*time.Second), time.Minute); err != nil || ok {
		t.Fatalf("a retried check was claimable before its delay: ok=%v err=%v", ok, err)
	}
	if _, ok, err := q.Claim(ctx, ahead.Add(31*time.Second), time.Minute); err != nil || !ok {
		t.Fatalf("a retried check was not claimable after its delay: ok=%v err=%v", ok, err)
	}
}
