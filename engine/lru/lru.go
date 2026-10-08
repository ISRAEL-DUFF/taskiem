// Package lru is a small, concurrency-safe cache with a bound on entries
// and an optional time to live. In-process caches keyed by anything a
// tenant controls (workflow versions, code, expressions) use it, so no
// tenant can grow a process's memory without limit (self-review S34).
package lru

import (
	"container/list"
	"sync"
	"time"
)

// DefaultSize bounds a zero Cache.
const DefaultSize = 1024

// Cache holds at most its size in entries, dropping the least recently
// used. With a TTL, an entry older than it is a miss. The zero Cache is
// ready to use: DefaultSize entries, no TTL.
type Cache[K comparable, V any] struct {
	max int
	ttl time.Duration
	now func() time.Time

	mu    sync.Mutex
	order *list.List // front: most recently used
	items map[K]*list.Element
}

type entry[K comparable, V any] struct {
	key K
	val V
	at  time.Time
}

// New makes a cache of at most size entries (at least 1); ttl 0 never
// expires entries by age.
func New[K comparable, V any](size int, ttl time.Duration) *Cache[K, V] {
	c := &Cache[K, V]{max: max(1, size), ttl: ttl}
	c.init()
	return c
}

// init readies a zero Cache; c.mu is held (or c is not yet shared).
func (c *Cache[K, V]) init() {
	if c.items != nil {
		return
	}
	if c.max <= 0 {
		c.max = DefaultSize
	}
	if c.now == nil {
		c.now = time.Now
	}
	c.order, c.items = list.New(), map[K]*list.Element{}
}

// Get returns the value for key, if present and fresh.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	var zero V
	el, ok := c.items[key]
	if !ok {
		return zero, false
	}
	e := el.Value.(*entry[K, V])
	if c.ttl > 0 && c.now().Sub(e.at) > c.ttl {
		c.order.Remove(el)
		delete(c.items, key)
		return zero, false
	}
	c.order.MoveToFront(el)
	return e.val, true
}

// Put stores val under key, evicting the least recently used entry when
// the cache is full.
func (c *Cache[K, V]) Put(key K, val V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	if el, ok := c.items[key]; ok {
		e := el.Value.(*entry[K, V])
		e.val, e.at = val, c.now()
		c.order.MoveToFront(el)
		return
	}
	c.items[key] = c.order.PushFront(&entry[K, V]{key: key, val: val, at: c.now()})
	for c.order.Len() > c.max {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.items, last.Value.(*entry[K, V]).key)
	}
}

// Delete drops key.
func (c *Cache[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	if el, ok := c.items[key]; ok {
		c.order.Remove(el)
		delete(c.items, key)
	}
}

// Clear drops every entry.
func (c *Cache[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	c.order.Init()
	c.items = map[K]*list.Element{}
}

// Len is the number of entries held, fresh or not.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.init()
	return c.order.Len()
}
