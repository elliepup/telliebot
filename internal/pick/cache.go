package pick

import (
	"container/list"
	"sync"
	"time"
)

// Page-1 cache defaults. Every pick fetches page 1 to learn how many pages
// there are, so caching it about halves requests for repeated settings
// (rerolls, votes). Entries are small (one page of results).
const (
	DefaultCacheSize = 100
	DefaultCacheTTL  = 10 * time.Minute
)

// FirstPage is a cached page 1: its items and how many pages there are.
type FirstPage[T any] struct {
	LastPage int
	Items    []T
}

// Cache is a small, concurrency-safe LRU cache with a fixed TTL. Callers must
// not modify values they get from it.
type Cache[V any] struct {
	mu    sync.Mutex
	max   int
	ttl   time.Duration
	now   func() time.Time
	items map[string]*list.Element
	order *list.List // front = most recently used
}

type cacheEntry[V any] struct {
	key     string
	val     V
	expires time.Time
}

// NewCache returns a cache holding at most max entries, each for ttl.
func NewCache[V any](max int, ttl time.Duration) *Cache[V] {
	return &Cache[V]{max: max, ttl: ttl, now: time.Now, items: map[string]*list.Element{}, order: list.New()}
}

// Get returns the value for key if it's present and not expired.
func (c *Cache[V]) Get(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	el, ok := c.items[key]
	if !ok {
		return zero, false
	}
	e := el.Value.(*cacheEntry[V])
	if !c.now().Before(e.expires) {
		c.order.Remove(el)
		delete(c.items, key)
		return zero, false
	}
	c.order.MoveToFront(el)
	return e.val, true
}

// Put stores val under key, evicting the least recently used entry if full.
func (c *Cache[V]) Put(key string, val V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	expires := c.now().Add(c.ttl)
	if el, ok := c.items[key]; ok {
		e := el.Value.(*cacheEntry[V])
		e.val, e.expires = val, expires
		c.order.MoveToFront(el)
		return
	}
	c.items[key] = c.order.PushFront(&cacheEntry[V]{key: key, val: val, expires: expires})
	for c.order.Len() > c.max {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*cacheEntry[V]).key)
	}
}

// Len reports how many entries are stored, including expired ones not yet
// evicted.
func (c *Cache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
