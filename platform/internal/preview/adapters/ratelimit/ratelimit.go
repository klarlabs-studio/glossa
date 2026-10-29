// Package ratelimit implements the preview's Limiter with the kernel's
// in-process token bucket per caller: each glossa-server instance
// enforces the limit on its own, which bounds the CPU one caller can
// spend on any instance (the limit's purpose) without a shared store.
package ratelimit

import (
	"time"

	"go.klarlabs.de/glossa/platform/internal/kernel/ratelimit"
	"go.klarlabs.de/glossa/platform/internal/preview/app"
)

// Config is a token bucket per key: Rate tokens per Interval, up to
// Burst at once.
type Config = ratelimit.Config

// Limiter implements app.Limiter.
type Limiter = ratelimit.Limiter

var _ app.Limiter = (*Limiter)(nil)

// Default is the preview's documented limit: 10 a second per person or
// token, bursts of up to 120 (a burst of keystrokes, or a page of
// messages rendered at once).
func Default() Config { return Config{Rate: 10, Interval: time.Second, Burst: 120} }

// New returns a Limiter with cfg.
func New(cfg Config) *Limiter { return ratelimit.New(cfg) }
