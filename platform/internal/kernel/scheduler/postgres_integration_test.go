//go:build integration

package scheduler_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/db"
	"go.klarlabs.de/glossa/platform/internal/kernel/db/dbtest"
	"go.klarlabs.de/glossa/platform/internal/kernel/scheduler"
)

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

func lease(t *testing.T) *scheduler.PostgresLease {
	t.Helper()
	if err := env.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	return scheduler.NewPostgresLease(db.NewUnitOfWork(env.App))
}

const (
	day  = 24 * time.Hour
	hold = 5 * time.Minute
)

func TestOnlyOneHolderTakesTheLease(t *testing.T) {
	l := lease(t)
	ctx := context.Background()

	if ok, err := l.Acquire(ctx, "context.purge", "replica-a", day, hold); err != nil || !ok {
		t.Fatalf("first Acquire = %v, %v", ok, err)
	}
	if ok, err := l.Acquire(ctx, "context.purge", "replica-b", day, hold); err != nil || ok {
		t.Fatalf("second Acquire = %v, %v; want the lease refused", ok, err)
	}
	// A lease the other replica doesn't hold is not its to release.
	if err := l.Release(ctx, "context.purge", "replica-b"); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.Acquire(ctx, "context.purge", "replica-c", day, hold); err != nil || ok {
		t.Fatalf("Acquire after a foreign release = %v, %v", ok, err)
	}
	// Another job's lease is independent.
	if ok, err := l.Acquire(ctx, "catalog.proposals", "replica-b", day, hold); err != nil || !ok {
		t.Fatalf("Acquire of another job = %v, %v", ok, err)
	}
}

func TestReleasingRecordsTheRunSoTheJobWaitsForItsInterval(t *testing.T) {
	l := lease(t)
	ctx := context.Background()

	if ok, _ := l.Acquire(ctx, "context.purge", "replica-a", day, hold); !ok {
		t.Fatal("Acquire")
	}
	if err := l.Release(ctx, "context.purge", "replica-a"); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.Acquire(ctx, "context.purge", "replica-b", day, hold); err != nil || ok {
		t.Errorf("Acquire within the interval = %v, %v; want it not due", ok, err)
	}
	// With a zero interval the job is due again immediately: the same
	// path a shorter interval takes after it elapses.
	if ok, err := l.Acquire(ctx, "context.purge", "replica-b", 0, hold); err != nil || !ok {
		t.Errorf("Acquire after the interval = %v, %v", ok, err)
	}
}

func TestAnExpiredLeaseIsTakenOverByAnotherReplica(t *testing.T) {
	l := lease(t)
	ctx := context.Background()

	// The holder dies mid-run: nothing releases the lease.
	if ok, _ := l.Acquire(ctx, "context.purge", "replica-a", day, time.Nanosecond); !ok {
		t.Fatal("Acquire")
	}
	time.Sleep(2 * time.Millisecond)
	if ok, err := l.Acquire(ctx, "context.purge", "replica-b", day, hold); err != nil || !ok {
		t.Errorf("Acquire of an expired lease = %v, %v", ok, err)
	}
}

func TestConcurrentReplicasGrantTheLeaseOnce(t *testing.T) {
	l := lease(t)
	const replicas = 8
	var (
		mu    sync.Mutex
		taken int
		wg    sync.WaitGroup
	)
	for i := range replicas {
		wg.Go(func() {
			ok, err := l.Acquire(context.Background(), "context.purge", fmt.Sprintf("replica-%d", i), day, hold)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if ok {
				taken++
			}
		})
	}
	wg.Wait()
	if taken != 1 {
		t.Errorf("%d of %d replicas took the lease, want 1", taken, replicas)
	}
}

func TestTheSchedulerRunsOnceAcrossReplicas(t *testing.T) {
	l := lease(t)
	var (
		mu   sync.Mutex
		runs int
		wg   sync.WaitGroup
	)
	job := scheduler.Job{Name: "context.purge", Run: func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		runs++
		return nil
	}}
	cfg := scheduler.Config{Interval: day, Timeout: time.Minute, Lease: 2 * time.Minute, Poll: time.Second}
	for range 4 {
		s, err := scheduler.New(l, cfg)
		if err != nil {
			t.Fatal(err)
		}
		s.Add(job)
		wg.Go(func() {
			if _, err := s.RunOnce(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if runs != 1 {
		t.Errorf("runs = %d across four replicas, want 1", runs)
	}
}
