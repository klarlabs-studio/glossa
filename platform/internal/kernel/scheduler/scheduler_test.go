package scheduler_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/scheduler"
)

// leases is an in-memory Lease with the real one's semantics: a job is
// taken only when no live lease is held and its last run finished at or
// before now−interval.
type leases struct {
	mu     sync.Mutex
	now    func() time.Time
	held   map[string]string    // name → holder
	until  map[string]time.Time // name → lease expiry
	ran    map[string]time.Time // name → last completed run
	err    error
	grants int
}

func newLeases(now func() time.Time) *leases {
	return &leases{now: now, held: map[string]string{}, until: map[string]time.Time{}, ran: map[string]time.Time{}}
}

func (l *leases) Acquire(_ context.Context, name, holder string, interval, ttl time.Duration) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err != nil {
		return false, l.err
	}
	now := l.now()
	if expiry, ok := l.until[name]; ok && now.Before(expiry) {
		return false, nil
	}
	if last, ok := l.ran[name]; ok && last.After(now.Add(-interval)) {
		return false, nil
	}
	l.held[name], l.until[name] = holder, now.Add(ttl)
	l.grants++
	return true, nil
}

func (l *leases) Release(_ context.Context, name, holder string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held[name] != holder {
		return nil
	}
	delete(l.held, name)
	delete(l.until, name)
	l.ran[name] = l.now()
	return nil
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func config() scheduler.Config {
	return scheduler.Config{Interval: 24 * time.Hour, Timeout: time.Minute, Lease: 2 * time.Minute, Poll: time.Second}
}

func TestRunOnceRunsADueJobAndThenWaitsForTheInterval(t *testing.T) {
	c := &clock{t: time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)}
	l := newLeases(c.now)
	runs := 0
	s, err := scheduler.New(l, config(), scheduler.WithClock(c.now))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(scheduler.Job{Name: "context.purge", Run: func(context.Context) error { runs++; return nil }})

	for range 3 {
		if _, err := s.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if runs != 1 {
		t.Errorf("runs = %d after three checks within the interval, want 1", runs)
	}
	c.advance(25 * time.Hour)
	if n, err := s.RunOnce(context.Background()); err != nil || n != 1 {
		t.Fatalf("RunOnce after the interval = %d, %v", n, err)
	}
	if runs != 2 {
		t.Errorf("runs = %d, want 2", runs)
	}
}

func TestTheLeaseKeepsTwoReplicasFromBothPurging(t *testing.T) {
	c := &clock{t: time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)}
	l := newLeases(c.now)
	var mu sync.Mutex
	runs := 0
	job := scheduler.Job{Name: "context.purge", Run: func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		runs++
		return nil
	}}
	var replicas []*scheduler.Scheduler
	for range 3 {
		s, err := scheduler.New(l, config(), scheduler.WithClock(c.now))
		if err != nil {
			t.Fatal(err)
		}
		s.Add(job)
		replicas = append(replicas, s)
	}

	var wg sync.WaitGroup
	for _, s := range replicas {
		wg.Go(func() {
			if _, err := s.RunOnce(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()

	if runs != 1 {
		t.Errorf("runs = %d across three replicas, want 1", runs)
	}
	if l.grants != 1 {
		t.Errorf("leases granted = %d, want 1", l.grants)
	}
}

func TestAFailingJobReleasesItsLeaseAndIsRetriedNextInterval(t *testing.T) {
	c := &clock{t: time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)}
	l := newLeases(c.now)
	boom := errors.New("purge: storage unavailable")
	runs := 0
	s, err := scheduler.New(l, config(), scheduler.WithClock(c.now))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(scheduler.Job{Name: "context.purge", Run: func(context.Context) error { runs++; return boom }})

	if _, err := s.RunOnce(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("RunOnce = %v, want the job's error", err)
	}
	// The lease is released whatever the job did: another replica may
	// take it at the next interval.
	if _, err := s.RunOnce(context.Background()); err != nil || runs != 1 {
		t.Errorf("within the interval: runs = %d, err = %v", runs, err)
	}
	c.advance(25 * time.Hour)
	if _, err := s.RunOnce(context.Background()); !errors.Is(err, boom) || runs != 2 {
		t.Errorf("after the interval: runs = %d, err = %v", runs, err)
	}
}

func TestOneJobsFailureDoesNotStopTheOthers(t *testing.T) {
	c := &clock{t: time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)}
	l := newLeases(c.now)
	boom := errors.New("boom")
	swept := false
	s, err := scheduler.New(l, config(), scheduler.WithClock(c.now))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(scheduler.Job{Name: "context.purge", Run: func(context.Context) error { return boom }})
	s.Add(scheduler.Job{Name: "catalog.proposals", Run: func(context.Context) error { swept = true; return nil }})

	n, err := s.RunOnce(context.Background())
	if n != 2 || !errors.Is(err, boom) {
		t.Errorf("RunOnce = %d, %v; want both jobs run and the failure reported", n, err)
	}
	if !swept {
		t.Error("the second job did not run")
	}
}

func TestAJobIsBoundedByTheTimeout(t *testing.T) {
	c := &clock{t: time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)}
	l := newLeases(c.now)
	cfg := config()
	cfg.Timeout = 20 * time.Millisecond
	cfg.Lease = time.Second
	s, err := scheduler.New(l, cfg, scheduler.WithClock(c.now))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(scheduler.Job{Name: "context.purge", Run: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}})

	if _, err := s.RunOnce(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("RunOnce = %v, want the timeout", err)
	}
}

func TestRunStopsWithTheContext(t *testing.T) {
	c := &clock{t: time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)}
	l := newLeases(c.now)
	cfg := config()
	cfg.Poll = time.Millisecond
	ran := make(chan struct{}, 1)
	s, err := scheduler.New(l, cfg, scheduler.WithClock(c.now))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(scheduler.Job{Name: "context.purge", Run: func(context.Context) error {
		select {
		case ran <- struct{}{}:
		default:
		}
		return nil
	}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("the job never ran")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop with its context")
	}
}

func TestConfigIsValidated(t *testing.T) {
	for name, cfg := range map[string]scheduler.Config{
		"no interval":             {Timeout: time.Minute, Lease: 2 * time.Minute, Poll: time.Second},
		"lease at most timeout":   {Interval: time.Hour, Timeout: time.Minute, Lease: time.Minute, Poll: time.Second},
		"negative jitter":         {Interval: time.Hour, Timeout: time.Minute, Lease: 2 * time.Minute, Poll: time.Second, Jitter: -0.1},
		"jitter above one":        {Interval: time.Hour, Timeout: time.Minute, Lease: 2 * time.Minute, Poll: time.Second, Jitter: 1.5},
		"timeout beyond interval": {Interval: time.Minute, Timeout: time.Hour, Lease: 2 * time.Hour, Poll: time.Second},
		"poll beyond interval":    {Interval: time.Minute, Timeout: time.Second, Lease: 2 * time.Second, Poll: time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := scheduler.New(newLeases(time.Now), cfg); err == nil {
				t.Error("New accepted an invalid config")
			}
		})
	}
}

func TestAJobNameMustBeALeaseName(t *testing.T) {
	s, err := scheduler.New(newLeases(time.Now), config())
	if err != nil {
		t.Fatal(err)
	}
	s.Add(scheduler.Job{Name: "Not A Lease Name", Run: func(context.Context) error { return nil }})
	if _, err := s.RunOnce(context.Background()); err == nil {
		t.Error("RunOnce accepted an invalid job name")
	}
}
