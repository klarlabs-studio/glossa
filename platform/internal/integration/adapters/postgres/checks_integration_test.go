//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	integrationpg "go.klarlabs.de/glossa/platform/internal/integration/adapters/postgres"
	"go.klarlabs.de/glossa/platform/internal/integration/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// The check queue's own question, against real Postgres: what the
// upsert does to `opened_at` (migration 0033).
//
// The rule is load-bearing for RFC 0005 §4.3 — a policy's grace is
// measured against when the pull request was opened, so a `synchronize`
// must not make a long-lived pull request younger and push the end of
// its grace a little further out with every push. The flow tests in
// internal/integration/app assert it through an in-memory queue that
// was written to mirror this SQL, and a double agreeing with itself
// proves nothing about the statement. This is the statement.

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

const (
	testInstallation int64 = 4242
	testRepository   int64 = 10101
	testPullRequest        = 7
	firstSHA               = "9f1c2e3d4b5a69788796a5b4c3d2e1f0a9b8c7d6"
	secondSHA              = "0123456789abcdef0123456789abcdef01234567"
)

// queue is a check queue on a freshly reset database, with one tenant.
func queue(t *testing.T) (*integrationpg.Checks, tenancy.ID) {
	t.Helper()
	ctx := t.Context()
	if err := env.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	tenant, err := env.SeedTenant(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	return integrationpg.NewChecks(db.NewUnitOfWork(env.App)), tenant
}

// open is one `pull_request` event reaching the queue: the row, with
// this head SHA and this `created_at`. A zero openedAt is a payload
// that carried none.
func open(t *testing.T, q *integrationpg.Checks, tenant tenancy.ID, headSHA string, openedAt, now time.Time) domain.Check {
	t.Helper()
	c, err := q.Open(t.Context(), domain.Check{
		ID: uuid.Must(uuid.NewV7()), TenantID: tenant.UUID(), InstallationID: testInstallation,
		RepositoryID: testRepository, PullRequest: testPullRequest, Branch: "feature/payment-copy",
		HeadSHA: headSHA, OpenedAt: openedAt,
		State: domain.CheckQueued, RequestedAt: now, AvailableAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("open the check: %v", err)
	}
	return c
}

// storedOpenedAt reads the column past RLS, so the assertion is about
// what is in the table and not about what the last statement happened
// to return.
func storedOpenedAt(t *testing.T) (at time.Time, null bool) {
	t.Helper()
	var v *time.Time
	err := env.Super.QueryRow(t.Context(),
		"SELECT opened_at FROM integration_github_checks WHERE repository_id = $1 AND pull_request = $2",
		testRepository, testPullRequest).Scan(&v)
	if err != nil {
		t.Fatalf("read opened_at: %v", err)
	}
	if v == nil {
		return time.Time{}, true
	}
	return v.UTC(), false
}

// TestTheOpenedAtIsRecordedOnceAndNeverMovedByAPush is the COALESCE in
// OpenCheck, which is what keeps a long-lived pull request pinned
// across its pushes (RFC 0005 §4.3).
func TestTheOpenedAtIsRecordedOnceAndNeverMovedByAPush(t *testing.T) {
	q, tenant := queue(t)
	opened := time.Date(2026, 9, 19, 9, 30, 0, 0, time.UTC)
	first := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

	// The pull request opens: GitHub's own instant lands in the column.
	if got := open(t, q, tenant, firstSHA, opened, first).OpenedAt; !got.Equal(opened) {
		t.Fatalf("opened_at = %s on the first open, want %s", got, opened)
	}
	if at, null := storedOpenedAt(t); null || !at.Equal(opened) {
		t.Fatalf("stored opened_at = %s (null=%v), want %s", at, null, opened)
	}

	// A `synchronize` two days later, and GitHub's payload for it says
	// something else entirely. The pull request is not two days younger.
	later := first.Add(48 * time.Hour)
	row := open(t, q, tenant, secondSHA, later, later)
	if !row.OpenedAt.Equal(opened) {
		t.Fatalf("opened_at = %s after a push, want it unmoved at %s", row.OpenedAt, opened)
	}
	if row.HeadSHA != secondSHA || !row.RequestedAt.Equal(later) {
		t.Fatalf("row = %+v: the new commit and its wait for CI should have landed", row)
	}

	// And a payload that names no `created_at` does not erase it.
	if got := open(t, q, tenant, secondSHA, time.Time{}, later).OpenedAt; !got.Equal(opened) {
		t.Fatalf("opened_at = %s after an event with no created_at, want %s", got, opened)
	}
	if at, null := storedOpenedAt(t); null || !at.Equal(opened) {
		t.Fatalf("stored opened_at = %s (null=%v), want %s", at, null, opened)
	}

	// The row reads back the same way the worker will read it.
	back, err := q.Check(t.Context(), testRepository, testPullRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !back.OpenedAt.Equal(opened) {
		t.Fatalf("opened_at = %s when read back, want %s", back.OpenedAt, opened)
	}
}

// TestARowWithNoOpenedAtLearnsItFromTheNextEvent: a check row written
// before migration 0033 has NULL there, and so does one whose payload
// carried no `created_at`. Such a row must not stay blind — the next
// event that knows when the pull request was opened fills it in, which
// is why the upsert COALESCEs rather than simply keeping what is there.
func TestARowWithNoOpenedAtLearnsItFromTheNextEvent(t *testing.T) {
	q, tenant := queue(t)
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

	// A row as 0033 found them: no opened-at at all.
	if got := open(t, q, tenant, firstSHA, time.Time{}, now).OpenedAt; !got.IsZero() {
		t.Fatalf("opened_at = %s, want the zero time when nothing recorded one", got)
	}
	at, null := storedOpenedAt(t)
	if !null {
		t.Fatalf("stored opened_at = %s, want NULL: the zero time is not a timestamp", at)
	}

	// The next event carries it, and the row learns it.
	opened := time.Date(2026, 9, 19, 9, 30, 0, 0, time.UTC)
	if got := open(t, q, tenant, secondSHA, opened, now).OpenedAt; !got.Equal(opened) {
		t.Fatalf("opened_at = %s, want the row to learn %s", got, opened)
	}
	if at, null := storedOpenedAt(t); null || !at.Equal(opened) {
		t.Fatalf("stored opened_at = %s (null=%v), want %s", at, null, opened)
	}
}
