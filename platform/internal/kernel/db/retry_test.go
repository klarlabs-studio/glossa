package db

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// fakeClock records the backoff delays and advances virtual time, so the
// retry runs without sleeping.
type fakeClock struct {
	delays  []time.Duration
	elapsed time.Duration
	limit   time.Duration
}

func (c *fakeClock) sleep(context.Context, time.Duration) error { return nil }

func (c *fakeClock) sleepUntil(ctx context.Context, d time.Duration) error {
	c.delays = append(c.delays, d)
	c.elapsed += d
	if c.elapsed >= c.limit {
		return context.DeadlineExceeded
	}
	return nil
}

var errRefused = errors.New("dial tcp 10.0.0.1:5432: connect: connection refused")

func TestRetrySucceedsAfterRefusals(t *testing.T) {
	var logs bytes.Buffer
	clock := &fakeClock{limit: time.Minute}
	r := Retry{Deadline: time.Minute, Logger: slog.New(slog.NewTextHandler(&logs, nil)), Sleep: clock.sleepUntil}

	calls := 0
	err := r.Do(context.Background(), "application pool", func(context.Context) error {
		calls++
		if calls < 4 {
			return errRefused
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if calls != 4 {
		t.Errorf("attempts = %d, want 4", calls)
	}
	want := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second}
	if !equalDurations(clock.delays, want) {
		t.Errorf("delays = %v, want %v", clock.delays, want)
	}
	out := logs.String()
	if n := strings.Count(out, "database not reachable, retrying"); n != 3 {
		t.Errorf("logged %d retries, want 3:\n%s", n, out)
	}
	if !strings.Contains(out, "attempt=3") || !strings.Contains(out, "connection refused") || !strings.Contains(out, "database connected") {
		t.Errorf("log lacks attempt, cause or success:\n%s", out)
	}
}

func TestRetryBackoffIsCapped(t *testing.T) {
	clock := &fakeClock{limit: 20 * time.Second}
	r := Retry{Deadline: 20 * time.Second, Sleep: clock.sleepUntil}
	err := r.Do(context.Background(), "migrations", func(context.Context) error { return errRefused })
	if !errors.Is(err, errRefused) {
		t.Fatalf("err = %v, want the last attempt's error", err)
	}
	if !strings.Contains(err.Error(), "gave up after") {
		t.Errorf("err = %v, want it to say it gave up", err)
	}
	for _, d := range clock.delays {
		if d > retryMaxDelay {
			t.Errorf("delay %v exceeds the cap %v", d, retryMaxDelay)
		}
	}
	if got := clock.delays[len(clock.delays)-1]; got != retryMaxDelay {
		t.Errorf("last delay = %v, want the cap %v", got, retryMaxDelay)
	}
}

func TestRetryStopsOnPermanentError(t *testing.T) {
	clock := &fakeClock{limit: time.Minute}
	r := Retry{Deadline: time.Minute, Sleep: clock.sleepUntil}
	bad := errors.New("db: parse DSN: bad")
	calls := 0
	err := r.Do(context.Background(), "application pool", func(context.Context) error {
		calls++
		return permanent(bad)
	})
	if !errors.Is(err, bad) || calls != 1 || len(clock.delays) != 0 {
		t.Errorf("err = %v after %d attempts, %d sleeps; want the parse error at once", err, calls, len(clock.delays))
	}
}

func TestRetryZeroDeadlineTriesOnce(t *testing.T) {
	clock := &fakeClock{limit: time.Minute}
	calls := 0
	err := Retry{Sleep: clock.sleep}.Do(context.Background(), "x", func(context.Context) error {
		calls++
		return errRefused
	})
	if !errors.Is(err, errRefused) || calls != 1 {
		t.Errorf("err = %v after %d attempts, want one failed attempt", err, calls)
	}
}

func TestRetryHonoursDeadlineWithRealTimer(t *testing.T) {
	start := time.Now()
	err := Retry{Deadline: 300 * time.Millisecond}.Do(context.Background(), "x", func(context.Context) error { return errRefused })
	if !errors.Is(err, errRefused) {
		t.Fatalf("err = %v", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("took %v, want about the 300ms deadline", took)
	}
}

func TestOpenPoolRetryRejectsBadDSNAtOnce(t *testing.T) {
	start := time.Now()
	_, err := Retry{Deadline: time.Minute}.OpenPool(context.Background(), "postgres://%zz", "t")
	if err == nil || !strings.Contains(err.Error(), "parse DSN") {
		t.Fatalf("err = %v, want a parse error", err)
	}
	if time.Since(start) > time.Second {
		t.Error("a DSN that does not parse was retried")
	}
}

func equalDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
