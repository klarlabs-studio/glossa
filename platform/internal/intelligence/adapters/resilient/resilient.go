// Package resilient wraps a Provider with fortify (RFC 0003 §3.1): a
// circuit breaker per provider around retries with exponential backoff on
// retryable failures (429, 5xx, overload, timeouts), each attempt bounded
// by a timeout. Client errors (bad request, auth, refusal) are neither
// retried nor counted against the breaker.
package resilient

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.klarlabs.de/fortify/circuitbreaker"
	"go.klarlabs.de/fortify/ferrors"
	"go.klarlabs.de/fortify/retry"
	"go.klarlabs.de/fortify/timeout"

	"go.klarlabs.de/glossa/platform/internal/intelligence/domain"
)

// Config tunes the wrapper. Zero values take the defaults.
type Config struct {
	// Timeout bounds one attempt (default 90s: adaptive thinking on a
	// long message can take a while).
	Timeout time.Duration
	// MaxAttempts is the number of attempts including the first (default 3).
	MaxAttempts  int
	InitialDelay time.Duration // default 500ms
	MaxDelay     time.Duration // default 10s
	// BreakerFailures consecutive outage failures open the circuit
	// (default 5); it half-opens after BreakerCooldown (default 30s).
	BreakerFailures int
	BreakerCooldown time.Duration
	Logger          *slog.Logger
}

func (c *Config) defaults() {
	if c.Timeout <= 0 {
		c.Timeout = 90 * time.Second
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.InitialDelay <= 0 {
		c.InitialDelay = 500 * time.Millisecond
	}
	if c.MaxDelay <= 0 {
		c.MaxDelay = 10 * time.Second
	}
	if c.BreakerFailures <= 0 {
		c.BreakerFailures = 5
	}
	if c.BreakerCooldown <= 0 {
		c.BreakerCooldown = 30 * time.Second
	}
}

// Provider is a resilient provider. It is safe for concurrent use; keep
// one per configured provider so the breaker sees all of its traffic.
type Provider struct {
	next    domain.Provider
	cfg     Config
	breaker circuitbreaker.CircuitBreaker[domain.Completion]
	retry   retry.Retry[domain.Completion]
	timeout timeout.Timeout[domain.Completion]
}

// Wrap returns p guarded by cfg.
func Wrap(p domain.Provider, cfg Config) *Provider {
	cfg.defaults()
	return &Provider{
		next: p,
		cfg:  cfg,
		breaker: circuitbreaker.New[domain.Completion](circuitbreaker.Config{
			MaxRequests: 1,
			Timeout:     cfg.BreakerCooldown,
			ReadyToTrip: circuitbreaker.TripOnConsecutiveFailures(cfg.BreakerFailures),
			// Only outages count: a rejected request says nothing about the
			// provider's health.
			IsSuccessful: func(err error) bool { return err == nil || !domain.IsRetryable(err) },
			Logger:       cfg.Logger,
		}),
		retry: retry.New[domain.Completion](retry.Config{
			MaxAttempts:   cfg.MaxAttempts,
			InitialDelay:  cfg.InitialDelay,
			MaxDelay:      cfg.MaxDelay,
			Multiplier:    2,
			BackoffPolicy: retry.BackoffExponential,
			Jitter:        true,
			IsRetryable:   domain.IsRetryable,
			Logger:        cfg.Logger,
		}),
		timeout: timeout.New[domain.Completion](timeout.Config{DefaultTimeout: cfg.Timeout}),
	}
}

// Name implements domain.Provider.
func (p *Provider) Name() string { return p.next.Name() }

// Complete implements domain.Provider.
func (p *Provider) Complete(ctx context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	out, err := p.breaker.Execute(ctx, func(ctx context.Context) (domain.Completion, error) {
		return p.retry.Execute(ctx, func(ctx context.Context) (domain.Completion, error) {
			return p.attempt(ctx, req)
		})
	})
	if errors.Is(err, circuitbreaker.ErrCircuitOpen) {
		return domain.Completion{}, &domain.ProviderError{Provider: p.Name(), Kind: domain.KindUnavailable, Err: err}
	}
	return out, err
}

func (p *Provider) attempt(ctx context.Context, req domain.CompletionRequest) (domain.Completion, error) {
	out, err := p.timeout.Execute(ctx, p.cfg.Timeout, func(ctx context.Context) (domain.Completion, error) {
		return p.next.Complete(ctx, req)
	})
	if err != nil && ctx.Err() == nil && (errors.Is(err, ferrors.ErrTimeout) || errors.Is(err, context.DeadlineExceeded)) {
		// The attempt timed out, not the caller: an outage, retryable.
		return domain.Completion{}, &domain.ProviderError{Provider: p.Name(), Kind: domain.KindUnavailable, Err: err}
	}
	return out, err
}
