package edge

import (
	"container/list"
	"sync"
	"time"
)

// lru is a size-bounded least-recently-used cache whose entries carry a
// freshness deadline. A stale entry is kept (until evicted): the edge
// serves it when storage can't answer, the way stale-if-error lets a CDN.
type lru[V any] struct {
	mu    sync.Mutex
	max   int64
	size  int64
	ll    *list.List
	items map[string]*list.Element
	cost  func(key string, v V) int64
}

type lruEntry[V any] struct {
	key   string
	value V
	// fresh until; the zero time means forever (immutable artifacts).
	until time.Time
	cost  int64
}

func newLRU[V any](maxBytes int64, cost func(string, V) int64) *lru[V] {
	return &lru[V]{max: maxBytes, ll: list.New(), items: map[string]*list.Element{}, cost: cost}
}

// get returns the entry for key and whether it is still fresh at now.
func (c *lru[V]) get(key string, now time.Time) (v V, fresh, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return v, false, false
	}
	c.ll.MoveToFront(el)
	e := el.Value.(*lruEntry[V])
	return e.value, e.until.IsZero() || now.Before(e.until), true
}

// put stores value until the deadline; values larger than the whole
// cache are not stored.
func (c *lru[V]) put(key string, value V, until time.Time) {
	cost := c.cost(key, value)
	if cost > c.max {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		e := el.Value.(*lruEntry[V])
		c.size += cost - e.cost
		e.value, e.until, e.cost = value, until, cost
		c.ll.MoveToFront(el)
	} else {
		c.items[key] = c.ll.PushFront(&lruEntry[V]{key: key, value: value, until: until, cost: cost})
		c.size += cost
	}
	for c.size > c.max {
		oldest := c.ll.Back()
		e := oldest.Value.(*lruEntry[V])
		c.ll.Remove(oldest)
		delete(c.items, e.key)
		c.size -= e.cost
	}
}

// len reports the number of entries (tests, metrics).
func (c *lru[V]) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
