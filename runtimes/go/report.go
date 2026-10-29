package glossa

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// The error channel (runtimes/SPEC.md §6): load, verification and format
// errors go to a callback, never into application code, and repeats are
// rate-limited.

const maxTrackedErrors = 4096

// Error is one runtime error reported to Config.OnError.
type Error struct {
	Type      ErrorType `json:"type"`
	Detail    string    `json:"detail"`
	MessageID string    `json:"messageId,omitempty"`
	Locale    string    `json:"locale,omitempty"`
	ReleaseID string    `json:"releaseId,omitempty"`
	// Repeated counts identical errors suppressed since this one was last
	// reported.
	Repeated int `json:"repeated,omitempty"`
}

func (e Error) Error() string {
	s := "glossa: " + string(e.Type) + ": "
	if e.MessageID != "" {
		s += e.MessageID
		if e.Locale != "" {
			s += " (" + e.Locale + ")"
		}
		s += ": "
	}
	return s + e.Detail
}

type reportKey struct {
	typ                                  ErrorType
	detail, messageID, locale, releaseID string
}

type reportEntry struct {
	last       time.Time
	suppressed int
}

type reporter struct {
	mu     sync.Mutex
	handle func(Error)
	window time.Duration
	now    func() time.Time
	seen   map[reportKey]*reportEntry
}

func newReporter(handle func(Error), window time.Duration) *reporter {
	return &reporter{handle: handle, window: window, now: time.Now, seen: map[reportKey]*reportEntry{}}
}

// report passes e to the handler unless the same error was reported within
// the window.
func (r *reporter) report(e Error) {
	if !r.admit(&e) {
		return
	}
	defer func() { _ = recover() }() // a broken handler must not break rendering
	r.handle(e)
}

func (r *reporter) admit(e *Error) bool {
	key := reportKey{e.Type, e.Detail, e.MessageID, e.Locale, e.ReleaseID}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry, ok := r.seen[key]; ok && now.Sub(entry.last) < r.window {
		entry.suppressed++
		return false
	} else if ok {
		e.Repeated = entry.suppressed
	}
	r.evict(now)
	r.seen[key] = &reportEntry{last: now}
	return true
}

// evict keeps the tracking map bounded: expired entries go first, and if
// the map is still full it starts over.
func (r *reporter) evict(now time.Time) {
	if len(r.seen) < maxTrackedErrors {
		return
	}
	for k, entry := range r.seen {
		if now.Sub(entry.last) >= r.window {
			delete(r.seen, k)
		}
	}
	if len(r.seen) >= maxTrackedErrors {
		clear(r.seen)
	}
}

// logHandler is the default error channel: one structured warning per
// reported error.
func logHandler(logger *slog.Logger) func(Error) {
	return func(e Error) {
		attrs := []slog.Attr{slog.String("type", string(e.Type)), slog.String("detail", e.Detail)}
		for _, a := range []struct{ k, v string }{{"message_id", e.MessageID}, {"locale", e.Locale}, {"release", e.ReleaseID}} {
			if a.v != "" {
				attrs = append(attrs, slog.String(a.k, a.v))
			}
		}
		if e.Repeated > 0 {
			attrs = append(attrs, slog.Int("repeated", e.Repeated))
		}
		logger.LogAttrs(context.Background(), slog.LevelWarn, "glossa runtime error", attrs...)
	}
}
