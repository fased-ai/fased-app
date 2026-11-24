package marketdata

import (
	"sync"
	"time"
)

type inflightCall struct {
	done chan struct{}
	val  any
	err  error
}

// callGroup coalesces concurrent identical upstream lookups into a single
// in-flight execution so UI grid tiles never stampede REST endpoints.
type callGroup struct {
	mu    sync.Mutex
	calls map[string]*inflightCall
}

func newCallGroup() *callGroup {
	return &callGroup{
		calls: make(map[string]*inflightCall),
	}
}

func (g *callGroup) Do(key string, fn func() (any, error)) (any, error) {
	g.mu.Lock()
	if existing, ok := g.calls[key]; ok {
		g.mu.Unlock()
		<-existing.done
		return existing.val, existing.err
	}
	c := &inflightCall{done: make(chan struct{})}
	g.calls[key] = c
	g.mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			g.mu.Lock()
			delete(g.calls, key)
			g.mu.Unlock()
			close(c.done)
			panic(r)
		}
		g.mu.Lock()
		delete(g.calls, key)
		g.mu.Unlock()
		close(c.done)
	}()

	c.val, c.err = fn()
	return c.val, c.err
}

type cacheEntry[V any] struct {
	value     V
	expiresAt time.Time
}

// ttlCache stores bounded short-lived entries keyed by string with an injected clock.
type ttlCache[V any] struct {
	mu         sync.RWMutex
	maxEntries int
	now        func() time.Time
	items      map[string]cacheEntry[V]
}

func newTTLCache[V any](maxEntries int, now func() time.Time) *ttlCache[V] {
	if maxEntries <= 0 {
		maxEntries = 128
	}
	if now == nil {
		now = time.Now
	}
	return &ttlCache[V]{
		maxEntries: maxEntries,
		now:        now,
		items:      make(map[string]cacheEntry[V]),
	}
}

func (c *ttlCache[V]) get(key string) (V, bool) {
	var zero V
	now := c.now()
	c.mu.RLock()
	entry, ok := c.items[key]
	c.mu.RUnlock()
	if !ok {
		return zero, false
	}
	if !now.Before(entry.expiresAt) {
		c.mu.Lock()
		if cur, still := c.items[key]; still && !now.Before(cur.expiresAt) {
			delete(c.items, key)
		}
		c.mu.Unlock()
		return zero, false
	}
	return entry.value, true
}

func (c *ttlCache[V]) set(key string, value V, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= c.maxEntries {
		for k, entry := range c.items {
			if !now.Before(entry.expiresAt) {
				delete(c.items, k)
			}
		}
		if len(c.items) >= c.maxEntries {
			var oldestKey string
			var oldestTime time.Time
			first := true
			for k, entry := range c.items {
				if first || entry.expiresAt.Before(oldestTime) {
					oldestKey = k
					oldestTime = entry.expiresAt
					first = false
				}
			}
			if oldestKey != "" {
				delete(c.items, oldestKey)
			}
		}
	}
	c.items[key] = cacheEntry[V]{
		value:     value,
		expiresAt: now.Add(ttl),
	}
}
