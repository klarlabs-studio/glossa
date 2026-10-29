package resilient_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/intelligence/adapters/resilient"
	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

type scripted struct {
	calls atomic.Int32
	errs  []error // error per call; nil or past the end succeeds
	delay time.Duration
}

func (s *scripted) Name() string { return "fake" }

func (s *scripted) Complete(ctx context.Context, _ domain.CompletionRequest) (domain.Completion, error) {
	n := int(s.calls.Add(1)) - 1
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return domain.Completion{}, ctx.Err()
		}
	}
	if n < len(s.errs) && s.errs[n] != nil {
		return domain.Completion{}, s.errs[n]
	}
	return domain.Completion{Provider: "fake", Text: "ok"}, nil
}

func perr(kind domain.ErrorKind) error {
	return &domain.ProviderError{Provider: "fake", Kind: kind, Status: 503}
}

var fast = resilient.Config{InitialDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond, Timeout: time.Second}

func TestRetriesRetryableFailures(t *testing.T) {
	tests := []struct {
		name      string
		errs      []error
		wantCalls int32
		wantKind  domain.ErrorKind // "" = success
	}{
		{name: "success", wantCalls: 1},
		{name: "429 then success", errs: []error{perr(domain.KindRateLimited)}, wantCalls: 2},
		{name: "5xx twice then success", errs: []error{perr(domain.KindUnavailable), perr(domain.KindUnavailable)}, wantCalls: 3},
		{name: "gives up after max attempts", errs: []error{perr(domain.KindUnavailable), perr(domain.KindUnavailable), perr(domain.KindUnavailable)}, wantCalls: 3, wantKind: domain.KindUnavailable},
		{name: "bad request is not retried", errs: []error{perr(domain.KindInvalidRequest)}, wantCalls: 1, wantKind: domain.KindInvalidRequest},
		{name: "auth is not retried", errs: []error{perr(domain.KindAuth)}, wantCalls: 1, wantKind: domain.KindAuth},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &scripted{errs: tc.errs}
			p := resilient.Wrap(s, fast)
			out, err := p.Complete(context.Background(), domain.CompletionRequest{})
			if got := s.calls.Load(); got != tc.wantCalls {
				t.Errorf("calls = %d, want %d", got, tc.wantCalls)
			}
			if tc.wantKind == "" {
				if err != nil || out.Text != "ok" {
					t.Fatalf("got %+v, %v", out, err)
				}
				return
			}
			var pe *domain.ProviderError
			if !errors.As(err, &pe) || pe.Kind != tc.wantKind {
				t.Fatalf("err = %v, want kind %s", err, tc.wantKind)
			}
		})
	}
}

func TestAttemptTimeoutIsRetryableOutage(t *testing.T) {
	s := &scripted{delay: 50 * time.Millisecond}
	cfg := fast
	cfg.Timeout, cfg.MaxAttempts = 5*time.Millisecond, 2
	_, err := resilient.Wrap(s, cfg).Complete(context.Background(), domain.CompletionRequest{})
	if !domain.IsRetryable(err) {
		t.Fatalf("err = %v, want a retryable outage", err)
	}
	if got := s.calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}

func TestCallerCancellationIsNotAnOutage(t *testing.T) {
	s := &scripted{delay: time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := resilient.Wrap(s, fast).Complete(ctx, domain.CompletionRequest{})
	if err == nil || domain.IsRetryable(err) {
		t.Fatalf("err = %v, want the caller's deadline", err)
	}
}

func TestBreakerOpensOnOutagesOnly(t *testing.T) {
	outage := perr(domain.KindUnavailable)
	s := &scripted{errs: []error{outage, outage, outage, outage}}
	cfg := fast
	cfg.MaxAttempts, cfg.BreakerFailures, cfg.BreakerCooldown = 1, 2, time.Hour
	p := resilient.Wrap(s, cfg)
	for range 2 {
		if _, err := p.Complete(context.Background(), domain.CompletionRequest{}); err == nil {
			t.Fatal("want failure")
		}
	}
	_, err := p.Complete(context.Background(), domain.CompletionRequest{})
	var pe *domain.ProviderError
	if !errors.As(err, &pe) || pe.Kind != domain.KindUnavailable || s.calls.Load() != 2 {
		t.Fatalf("err = %v after %d calls, want the open circuit without a call", err, s.calls.Load())
	}

	// Client errors never trip it.
	bad := perr(domain.KindInvalidRequest)
	s2 := &scripted{errs: []error{bad, bad, bad, bad}}
	p2 := resilient.Wrap(s2, cfg)
	for range 4 {
		_, _ = p2.Complete(context.Background(), domain.CompletionRequest{})
	}
	if s2.calls.Load() != 4 {
		t.Errorf("client errors tripped the breaker: %d calls", s2.calls.Load())
	}
	if p.Name() != "fake" {
		t.Errorf("Name = %q", p.Name())
	}
}
