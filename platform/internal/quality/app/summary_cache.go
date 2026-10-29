package app

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
)

// The quality summary's cache (RFC 0005 §8: "computed from the owning
// context's own tables on read, cached 60 s, behind one endpoint").
//
// It is in memory and per process, which is the right size for what it
// protects: seven cross-context reads that a dashboard would otherwise
// repeat on every poll, on every open tab. A second replica computing
// its own copy costs one extra round of reads a minute and needs no
// shared store, no invalidation protocol and no new failure mode — and
// a summary is a measurement of a minute ago either way.
//
// Nothing invalidates an entry. It expires, and that is the contract
// the response states with `expires_at`: a number that is at most a
// minute old, not a number that is current. Invalidation would have to
// listen to five other contexts to be right, and being wrong about it
// is how a cache starts lying.

// maxSummaries bounds the cache. A summary is a few kilobytes and the
// key includes the caller's permissions, so the bound is what keeps a
// tenant with ten thousand projects from holding the process's memory.
// Over it, the whole map is dropped: the entries live a minute, the
// next reads recompute, and an eviction policy that had to be tuned
// would be more machinery than the thing it manages.
const maxSummaries = 2048

// summaryKey identifies one computed summary. What the caller may read
// is part of it because two callers may hold different permissions, and
// a number one of them was allowed to compute must not be handed to the
// other from a cache.
type summaryKey struct {
	project     uuid.UUID
	locale      string
	environment string
	since       int64
	permissions uint
}

// summaryPermissions are the permissions that decide which of the seven
// numbers a caller can be shown, as a bitmask. Only these matter: two
// principals that differ in anything else compute the same summary, and
// keying on the whole grant would split the cache for no reason.
func summaryPermissions(ctx context.Context) uint {
	p, ok := authz.From(ctx)
	if !ok {
		return 0
	}
	var mask uint
	for i, perm := range []authz.Permission{
		authz.CatalogRead, authz.TranslationsRead, authz.IntelligenceRead,
		authz.ReleasesRead, authz.IntegrationRead,
	} {
		if p.Grant.Allows(perm) {
			mask |= 1 << uint(i) //nolint:gosec // i < 5
		}
	}
	return mask
}

type summaryEntry struct {
	summary Summary
	expires time.Time
}

// summaryCache is a bounded, expiring map of computed summaries.
type summaryCache struct {
	mu      sync.Mutex
	entries map[summaryKey]summaryEntry
	ttl     time.Duration
}

func newSummaryCache(ttl time.Duration) *summaryCache {
	return &summaryCache{entries: map[summaryKey]summaryEntry{}, ttl: ttl}
}

// get returns a summary that has not expired at now.
func (c *summaryCache) get(k summaryKey, now time.Time) (Summary, bool) {
	if c == nil || c.ttl <= 0 {
		return Summary{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok || !now.Before(e.expires) {
		delete(c.entries, k)
		return Summary{}, false
	}
	return e.summary, true
}

// put stores a summary for the TTL.
func (c *summaryCache) put(k summaryKey, s Summary, now time.Time) {
	if c == nil || c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= maxSummaries {
		c.entries = make(map[summaryKey]summaryEntry, maxSummaries)
	}
	c.entries[k] = summaryEntry{summary: s, expires: now.Add(c.ttl)}
}
