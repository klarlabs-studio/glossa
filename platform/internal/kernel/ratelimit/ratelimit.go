// Package ratelimit is a fortify token bucket per key, held in process:
// each glossa-server instance enforces a limit on its own, which bounds
// what one caller or tenant can spend on any instance without a shared
// store. Contexts wrap it in their own Limiter ports (the message
// preview per caller, Context's uploads per tenant).
package ratelimit

import (
	"context"
	"time"

	"go.klarlabs.de/fortify/ratelimit"
)

// Config is a token bucket per key: Rate tokens per Interval, up to
// Burst at once.
type Config struct {
	Rate     int
	Interval time.Duration
	Burst    int
}

// Bounds on the in-memory state: idle keys' buckets expire, and the
// number of keys tracked at once is capped.
const (
	maxKeys  = 100_000
	entryTTL = 10 * time.Minute
)

// Limiter limits calls per key.
type Limiter struct{ rl ratelimit.RateLimiter }

// New returns a Limiter with cfg.
func New(cfg Config) *Limiter {
	store := ratelimit.NewMemoryStoreWithOptions(
		ratelimit.WithMaxKeys(maxKeys), ratelimit.WithEntryTTL(max(entryTTL, cfg.Interval)), ratelimit.WithCleanupInterval(time.Minute),
	)
	return &Limiter{rl: ratelimit.New(ratelimit.Config{
		Rate: cfg.Rate, Interval: cfg.Interval, Burst: cfg.Burst, Store: store,
	})}
}

// Allow consumes one token of key's bucket and reports whether there
// was one.
func (l *Limiter) Allow(ctx context.Context, key string) bool { return l.rl.Allow(ctx, key) }

// Close stops the store's cleanup.
func (l *Limiter) Close() error { return l.rl.Close() }
